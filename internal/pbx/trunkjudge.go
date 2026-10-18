package pbx

import (
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/reason"
)

// The trunk judge: every 15 seconds each enabled trunk's keep-alive checks and the fate of its recent calls are
// put to the trunk-health rulebook, which says whether the trunk is used in its place in the outbound routes or
// tried after the others. It tells a provider that is down from this system's own link being down, and does not
// trust a keep-alive that fails while calls still get through. A trunk is never taken out altogether: it is only
// moved behind the others, so it is still tried when they fail. A change needs two judgements in a row.

// TrunkJudgement is what the judge decided about a trunk.
type TrunkJudgement struct {
	Order   string    `json:"order"` // use or last
	Text    string    `json:"text"`
	How     string    `json:"how"`
	Reasons []string  `json:"reasons"`
	Since   time.Time `json:"since"`
}

type trunkOutcome struct {
	at          time.Time
	ok          bool // the provider took the call (answered, rang, or a person-side answer like busy)
	carrierFail bool // the provider failed it: no answer, a server error, refused
}

type trunkJudged struct {
	applied  TrunkJudgement
	pending  string // the order the last judgement said, waiting for a second
	outcomes []trunkOutcome
}

// trunkOutcome records how a call out through a trunk ended, for the judge.
func (e *Engine) noteTrunkOutcome(trunkID string, code int) {
	if trunkID == "" {
		return
	}
	o := trunkOutcome{at: time.Now()}
	switch {
	case code < 300, code == 486, code == 600, code == 404, code == 484, code == 480, code == 487, code == 603, code == 410:
		o.ok = true // the provider answered, or handed back the called person's answer
	case code == 408, code >= 500, code == 403:
		o.carrierFail = true
	default:
		return // 401 and the like say nothing about the provider's health
	}
	e.judgeMu.Lock()
	defer e.judgeMu.Unlock()
	j := e.judged[trunkID]
	if j == nil {
		j = &trunkJudged{applied: TrunkJudgement{Order: "use"}}
		e.judged[trunkID] = j
	}
	j.outcomes = append(j.outcomes, o)
	if len(j.outcomes) > 50 {
		j.outcomes = j.outcomes[len(j.outcomes)-50:]
	}
}

func keepaliveTruth(state string) reason.Truth {
	switch state {
	case "unreachable", "failed":
		return reason.Yes
	case "registered", "reachable", "ready":
		return reason.No
	}
	return reason.Unknown // starting, registering: not measured yet
}

// failing reports a trunk's recent calls failing on the provider's side, one after another.
func failing(j *trunkJudged, n int, window time.Duration, now time.Time) bool {
	if j == nil || len(j.outcomes) < n {
		return false
	}
	last := j.outcomes[len(j.outcomes)-n:]
	for _, o := range last {
		if !o.carrierFail || now.Sub(o.at) > window {
			return false
		}
	}
	return true
}

func okRecently(j *trunkJudged, now time.Time) bool {
	if j == nil {
		return false
	}
	for _, o := range j.outcomes {
		if o.ok && now.Sub(o.at) <= 10*time.Minute {
			return true
		}
	}
	return false
}

// judgeTrunks puts every enabled trunk to the rulebook once.
func (e *Engine) judgeTrunks() {
	cfg := e.Config()
	jc := cfg.System.TrunkJudging
	now := time.Now()
	states := map[string]string{}
	for _, s := range e.TrunkStatus() {
		states[s.ID] = s.State
	}
	var trunks []config.Trunk
	for _, t := range cfg.Trunks {
		if t.Enabled {
			trunks = append(trunks, t)
		}
	}
	e.judgeMu.Lock()
	defer e.judgeMu.Unlock()
	healthy := func(t config.Trunk) bool {
		return keepaliveTruth(states[t.ID]) == reason.No && !failing(e.judged[t.ID], jc.FailuresOrDefault(), jc.WindowOrDefault(), now)
	}
	broken := func(t config.Trunk) bool {
		return keepaliveTruth(states[t.ID]) == reason.Yes || failing(e.judged[t.ID], jc.FailuresOrDefault(), jc.WindowOrDefault(), now)
	}
	for _, t := range trunks {
		j := e.judged[t.ID]
		if j == nil {
			j = &trunkJudged{applied: TrunkJudgement{Order: "use", Since: now}}
			e.judged[t.ID] = j
		}
		facts := map[string]reason.Truth{
			"keepalive_failed": keepaliveTruth(states[t.ID]),
			"calls_failing":    truth(failing(j, jc.FailuresOrDefault(), jc.WindowOrDefault(), now)),
			"call_ok_recently": truth(okRecently(j, now)),
		}
		others, otherOK, allBad := 0, false, true
		for _, o := range trunks {
			if o.ID == t.ID {
				continue
			}
			others++
			otherOK = otherOK || healthy(o)
			allBad = allBad && broken(o)
		}
		facts["other_way_out"] = truth(otherOK)
		facts["all_failing"] = truth(others > 0 && allBad)
		v := reason.Trunk.Decide(facts)
		if jc.Off {
			v = reason.Verdict{Action: "use", Text: "Use it in its place", How: "off", Reasons: []string{"Trunk judging is switched off (Trunks)."}}
		}
		if v.Action == j.applied.Order {
			j.pending = ""
			j.applied.Text, j.applied.How, j.applied.Reasons = v.Text, v.How, v.Reasons
			continue
		}
		if j.pending != v.Action && !jc.Off {
			j.pending = v.Action // a change needs a second judgement in a row
			continue
		}
		j.pending = ""
		j.applied = TrunkJudgement{Order: v.Action, Text: v.Text, How: v.How, Reasons: v.Reasons, Since: now}
		e.Log.Info("trunk judged", "trunk", t.Name, "order", v.Action, "why", strings.Join(v.Reasons, " "))
		e.publish("trunk.judged", map[string]any{"id": t.ID, "name": t.Name, "judgement": j.applied})
	}
	for id := range e.judged {
		if cfg.FindTrunk(id) == nil {
			delete(e.judged, id)
		}
	}
}

func truth(b bool) reason.Truth {
	if b {
		return reason.Yes
	}
	return reason.No
}

// TrunkJudgements returns what the judge decided about each trunk, by ID.
func (e *Engine) TrunkJudgements() map[string]TrunkJudgement {
	e.judgeMu.Lock()
	defer e.judgeMu.Unlock()
	out := map[string]TrunkJudgement{}
	for id, j := range e.judged {
		out[id] = j.applied
	}
	return out
}

// trunkLast reports a trunk the judge moved behind the others, with why.
func (e *Engine) trunkLast(id string) (bool, []string) {
	e.judgeMu.Lock()
	defer e.judgeMu.Unlock()
	if j := e.judged[id]; j != nil && j.applied.Order == "last" {
		return true, j.applied.Reasons
	}
	return false, nil
}

// orderTrunks puts the trunks the judge moved behind the others last, keeping each group's order.
func (e *Engine) orderTrunks(cfg *config.Config, ids []string) ([]string, []string) {
	var first, last, why []string
	for _, id := range ids {
		if l, r := e.trunkLast(id); l {
			last = append(last, id)
			name := id
			if t := cfg.FindTrunk(id); t != nil {
				name = t.Name
			}
			why = append(why, name+" was tried after the others: "+strings.TrimSuffix(strings.Join(r, " "), "."))
			continue
		}
		first = append(first, id)
	}
	if len(last) == 0 || len(first) == 0 {
		return ids, nil
	}
	return slices.Concat(first, last), why
}

func (e *Engine) trunkJudgeLoop(stop <-chan struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			e.judgeTrunks()
		}
	}
}
