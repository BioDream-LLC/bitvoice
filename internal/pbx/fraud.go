package pbx

import (
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/reason"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The toll-fraud guard: every call out of the country is put to the fraud rulebook before it is placed, with what
// is known about it (a first call to that country, many calls in a short time, outside business hours, placed from
// outside, an attack in progress, an approved exception), and is let through, reported, asked for a PIN or refused,
// with the reasons on the call record and on the Security screen. What cannot be measured (a call history that
// cannot be read) is never taken for safety: the guard is then cautious and asks for a PIN.

// FraudDecision is one call the guard judged.
type FraudDecision struct {
	At      time.Time `json:"at"`
	Ext     string    `json:"ext"`
	Number  string    `json:"number"`
	Country string    `json:"country"` // the calling code it went to
	Action  string    `json:"action"`  // allow, alert, challenge, block
	How     string    `json:"how"`
	Outside bool      `json:"outside"`
	Reasons []string  `json:"reasons"`
}

type cdrReader interface {
	CDRs(store.CDRQuery) ([]store.CDR, error)
}

type fraudState struct {
	loaded  bool
	history map[string]map[string]time.Time // extension: calling code: last call
	recent  map[string][]time.Time          // extension: calls abroad judged lately
	log     []FraudDecision
}

// abroadOf says whether a call leaves the country and where to, from the number dialled or, failing that, the
// number as sent to the provider.
func abroadOf(cfg *config.Config, dialled, sent string) (bool, string) {
	if ok, code := cfg.System.Abroad(dialled); ok {
		return true, code
	}
	return cfg.System.Abroad(sent)
}

// loadFraudHistory reads 90 days of calls out from the call records, once. It reports whether it could.
func (e *Engine) loadFraudHistory(cfg *config.Config) bool {
	if e.fraud.loaded {
		return true
	}
	r, ok := e.Recorder.(cdrReader)
	if !ok {
		return false
	}
	rs, err := r.CDRs(store.CDRQuery{Since: time.Now().AddDate(0, 0, -90), Direction: "outbound", Limit: 100000})
	if err != nil {
		return false
	}
	for _, c := range rs {
		if c.Ext == "" {
			continue
		}
		if ok, code := abroadOf(cfg, c.To, c.To); ok {
			e.rememberCountry(c.Ext, code, c.Start)
		}
	}
	e.fraud.loaded = true
	return true
}

func (e *Engine) rememberCountry(ext, code string, at time.Time) {
	if e.fraud.history == nil {
		e.fraud.history = map[string]map[string]time.Time{}
	}
	if e.fraud.history[ext] == nil {
		e.fraud.history[ext] = map[string]time.Time{}
	}
	if at.After(e.fraud.history[ext][code]) {
		e.fraud.history[ext][code] = at
	}
}

// fraudGate judges a call out before it is placed and returns the plan to follow: unchanged, asking for a PIN,
// or refused. outside is a call placed from outside the office (a dial-in, a call forwarded out).
func (e *Engine) fraudGate(cfg *config.Config, x *config.Extension, dialled string, outside bool, p plan) plan {
	g := cfg.Security.FraudGuard
	if !g.On || x == nil || p.reject != 0 || p.local != "" || len(p.stages) == 0 || len(p.stages[0].targets) == 0 {
		return p
	}
	sent := p.stages[0].targets[0].number
	abroad, code := abroadOf(cfg, cleanNumber(dialled), sent)
	if !abroad {
		return p
	}
	now := time.Now().In(cfg.System.Location())
	e.fraudMu.Lock()
	defer e.fraudMu.Unlock()
	facts := map[string]reason.Truth{
		"emergency":       truth(slices.Contains(cfg.System.EmergencyNumbers, cleanNumber(dialled))),
		"international":   reason.Yes,
		"from_outside":    truth(outside),
		"approved":        truth(g.Excused(x.Number, now)),
		"allowed_country": truth(slices.Contains(g.AllowedCountries, code)),
		"after_hours":     reason.No,
		"attack_seen":     truth(e.bans != nil && e.bans.attackedWithin(time.Hour)),
	}
	if s := cfg.FindSchedule(g.Schedule); s != nil {
		facts["after_hours"] = truth(!s.IsOpen(now))
	}
	if e.loadFraudHistory(cfg) {
		last, ok := e.fraud.history[x.Number][code]
		facts["new_country"] = truth(!ok || now.Sub(last) > 90*24*time.Hour)
	} // else unknown: the call history could not be read
	var recent []time.Time
	for _, t := range e.fraud.recent[x.Number] {
		if now.Sub(t) <= 10*time.Minute {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	if e.fraud.recent == nil {
		e.fraud.recent = map[string][]time.Time{}
	}
	e.fraud.recent[x.Number] = recent
	facts["burst"] = truth(len(recent) > g.BurstOrDefault())

	v := reason.Fraud.Decide(facts)
	d := FraudDecision{At: now.UTC(), Ext: x.Number, Number: sent, Country: code, Action: v.Action, How: v.How, Outside: outside, Reasons: v.Reasons}
	if v.Action == "challenge" && cfg.FindPINSet(g.PINSet) == nil && p.pinSet == "" {
		d.Action = "block"
		d.Reasons = append(d.Reasons, "No PIN set is chosen for the toll-fraud guard, so the call is refused instead.")
	}
	e.fraud.log = append(e.fraud.log, d)
	if len(e.fraud.log) > 200 {
		e.fraud.log = e.fraud.log[len(e.fraud.log)-200:]
	}
	why := "Toll-fraud guard: " + strings.Join(d.Reasons, " ")
	switch d.Action {
	case "allow", "alert":
		e.rememberCountry(x.Number, code, now)
	}
	if d.Action != "allow" {
		e.Log.Warn("toll-fraud guard", "ext", x.Number, "to", sent, "action", d.Action, "why", strings.Join(d.Reasons, " "))
		e.publish("security.fraud", d)
	}
	switch d.Action {
	case "block":
		return plan{reject: 403, reason: "Refused By Toll-Fraud Guard", why: append(p.why, why)}
	case "challenge":
		if p.pinSet == "" {
			p.pinSet = g.PINSet
		}
	}
	p.why = append(p.why, why)
	return p
}

// FraudDecisions are the calls the guard judged lately, newest first.
func (e *Engine) FraudDecisions() []FraudDecision {
	e.fraudMu.Lock()
	defer e.fraudMu.Unlock()
	out := slices.Clone(e.fraud.log)
	slices.Reverse(out)
	if out == nil {
		out = []FraudDecision{}
	}
	return out
}
