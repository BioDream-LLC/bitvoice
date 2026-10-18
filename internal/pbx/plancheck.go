package pbx

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The call-plan checker tries every situation a configuration can be in, not a sample: every combination of
// open and closed that the schedules really reach over the coming year, every combination of day/night toggles,
// every incoming route, every path a caller can choose through menus and every no-answer branch, and every
// extension dialling every emergency number. It reports calls that end nowhere, loops, things nothing routes to,
// branches no situation ever takes, and emergency calls that rest on a single trunk. Each finding names the
// exact situation that shows it.

// PlanFinding is one thing the checker found.
type PlanFinding struct {
	Severity string `json:"severity"` // error (a call goes nowhere), warning (works, but look), info
	Kind     string `json:"kind"`     // dead-end, loop, warning, unreachable, never-taken, single-trunk
	Title    string `json:"title"`
	Says     string `json:"says"`
	// Where is the configuration item to open (kind and ID or number) when there is one.
	Where PlanWhere `json:"where"`
	// Situations are up to three situations that show it (of Count in all).
	Situations []PlanSituation `json:"situations"`
	Count      int             `json:"count"`
}

// PlanWhere names a configuration item.
type PlanWhere struct {
	Kind   string `json:"kind,omitempty"`
	Target string `json:"target,omitempty"`
	Name   string `json:"name,omitempty"`
}

// PlanSituation is one situation: the call, the time, the toggles, and the caller's choices along the way.
type PlanSituation struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Trunk   string          `json:"trunk,omitempty"`
	At      string          `json:"at"`      // the time used, RFC 3339 in the system's zone (for the simulator)
	When    string          `json:"when"`    // in words: "Mon 09:00 (Office hours open)"
	Toggles map[string]bool `json:"toggles"` // by toggle ID
	Path    []string        `json:"path"`    // "Press 2 (Billing)", "No answer after 20 seconds"
}

// PlanReport is the checker's answer.
type PlanReport struct {
	Situations int           `json:"situations"` // simulated calls
	Times      int           `json:"times"`      // distinct combinations of schedules found over the year
	Findings   []PlanFinding `json:"findings"`
	Errors     int           `json:"errors"`
	Warnings   int           `json:"warnings"`
	Truncated  bool          `json:"truncated,omitempty"`
}

// planMaxSims bounds the work: past it, the report says it stopped early.
const planMaxSims = 6000

type timeSlot struct {
	at   time.Time
	when string
}

// planTimes finds one moment for each combination of open and closed the schedules reach over the next year,
// in 15-minute steps.
func planTimes(cfg *config.Config, from time.Time) []timeSlot {
	loc := cfg.System.Location()
	start := from.In(loc).Truncate(15 * time.Minute)
	if len(cfg.Schedules) == 0 {
		return []timeSlot{{at: start, when: start.Format("Mon 2 Jan 15:04")}}
	}
	seen := map[string]bool{}
	var out []timeSlot
	for t := start; t.Before(start.AddDate(1, 0, 0)); t = t.Add(15 * time.Minute) {
		var key strings.Builder
		var parts []string
		for _, s := range cfg.Schedules {
			open := s.IsOpen(t)
			key.WriteByte("01"[boolInt(open)])
			state := "closed"
			if open {
				state = "open"
			}
			parts = append(parts, s.Name+" "+state)
		}
		if seen[key.String()] {
			continue
		}
		seen[key.String()] = true
		out = append(out, timeSlot{at: t, when: t.Format("Mon 2 Jan 15:04") + " (" + strings.Join(parts, ", ") + ")"})
	}
	return out
}

// toggleSets is every combination of the toggles (or, past six toggles, each one switched alone).
func toggleSets(cfg *config.Config) []map[string]bool {
	n := len(cfg.Toggles)
	if n == 0 {
		return []map[string]bool{{}}
	}
	var out []map[string]bool
	if n <= 6 {
		for mask := 0; mask < 1<<n; mask++ {
			m := map[string]bool{}
			for i, t := range cfg.Toggles {
				m[t.ID] = mask&(1<<i) != 0
			}
			out = append(out, m)
		}
		return out
	}
	out = append(out, map[string]bool{})
	for _, t := range cfg.Toggles {
		out = append(out, map[string]bool{t.ID: true})
	}
	return out
}

type entry struct {
	from, to, trunk, label string
}

// CheckPlan checks the configuration in force.
func (e *Engine) CheckPlan() PlanReport { return e.CheckPlanOf(e.Config()) }

// CheckPlanOf checks a configuration (a proposed one, before it is saved).
func (e *Engine) CheckPlanOf(cfg *config.Config) PlanReport {
	rep := PlanReport{Findings: []PlanFinding{}}
	times := planTimes(cfg, time.Now())
	rep.Times = len(times)
	toggles := toggleSets(cfg)

	var entries []entry
	outside := "+15555550123" // an invented caller
	for _, r := range cfg.InboundRoutes {
		did := r.DID
		label := "a call to " + did
		if did == "" {
			did, label = "+15555550100", "a call to any number ("+r.Name+")"
		}
		entries = append(entries, entry{from: outside, to: did, trunk: r.Trunk, label: label})
	}
	found := map[string]*PlanFinding{}
	var order []string
	add := func(f PlanFinding, sit PlanSituation) {
		key := f.Kind + "\x00" + f.Where.Kind + "\x00" + f.Where.Target + "\x00" + f.Says
		x := found[key]
		if x == nil {
			f.Situations = []PlanSituation{}
			found[key] = &f
			order = append(order, key)
			x = &f
		}
		x.Count++
		if len(x.Situations) < 3 {
			x.Situations = append(x.Situations, sit)
		}
	}
	reached := map[string]bool{}
	taken := map[string]map[string]bool{} // schedule or toggle ID: the branches some situation took

	var walk func(st *SimStep, path []string, sit PlanSituation)
	walk = func(st *SimStep, path []string, sit PlanSituation) {
		if st == nil {
			return
		}
		if st.Target != "" {
			reached[st.Kind+"\x00"+st.Target] = true
		}
		s := sit
		s.Path = slices.Clone(path)
		where := PlanWhere{Kind: st.Kind, Target: st.Target, Name: st.Name}
		switch {
		case st.Kind == "loop":
			add(PlanFinding{Severity: "error", Kind: "loop", Title: "A call goes round in a loop", Says: st.Says, Where: where}, s)
		case st.Status == "fail":
			add(PlanFinding{Severity: "error", Kind: "dead-end", Title: "A call goes nowhere", Says: st.Says, Where: where}, s)
		case st.Status == "warn":
			add(PlanFinding{Severity: "warning", Kind: "warning", Title: "Works, but look at it", Says: st.Says, Where: where}, s)
		}
		for _, b := range st.Branches {
			if (st.Kind == "schedule" || st.Kind == "toggle") && b.Taken {
				if taken[st.Target] == nil {
					taken[st.Target] = map[string]bool{}
				}
				taken[st.Target][b.When] = true
			}
			// A schedule or toggle decides by the situation: only the branch it takes is a path a call can follow.
			if (st.Kind == "schedule" || st.Kind == "toggle") && !b.Taken {
				continue
			}
			walk(b.Step, append(slices.Clone(path), b.When), sit)
		}
	}

	sims := 0
	for _, en := range entries {
		for _, ts := range times {
			for _, tg := range toggles {
				if sims >= planMaxSims {
					rep.Truncated = true
					break
				}
				sims++
				res := e.simulateWith(cfg, SimInput{From: en.from, To: en.to, Trunk: en.trunk, At: ts.at, AssumePhones: true, Toggles: tg})
				sit := PlanSituation{From: en.from, To: en.to, Trunk: en.trunk, At: ts.at.Format(time.RFC3339), When: ts.when, Toggles: tg}
				walk(res.Start, []string{en.label}, sit)
			}
		}
	}

	// Emergency calls from every extension: they must get out, and on more than one trunk.
	at := time.Now().In(cfg.System.Location())
	single := map[string][]string{} // trunk name: extensions
	for _, x := range cfg.Extensions {
		if !x.Enabled {
			continue
		}
		for _, n := range cfg.System.EmergencyNumbers {
			sims++
			res := e.simulateWith(cfg, SimInput{From: x.Number, To: n, At: at, AssumePhones: true})
			sit := PlanSituation{From: x.Number, To: n, At: at.Format(time.RFC3339), When: at.Format("Mon 2 Jan 15:04"), Toggles: map[string]bool{}}
			walk(res.Start, []string{x.Number + " " + x.Name + " dials " + n}, sit)
			p := e.route(cfg, &x, n)
			if p.reject != 0 {
				continue
			}
			trunks := map[string]bool{}
			for _, st := range p.stages {
				for _, t := range st.targets {
					if t.trunk != nil {
						trunks[t.trunk.Name] = true
					} else if t.viaTrunk != "" {
						trunks[t.viaTrunk] = true
					}
				}
			}
			if len(trunks) == 1 {
				for name := range trunks {
					single[name] = appendOnce(single[name], x.Number)
				}
			}
		}
	}
	for name, exts := range single {
		sort.Strings(exts)
		shown := exts
		if len(shown) > 8 {
			shown = append(slices.Clone(shown[:8]), fmt.Sprintf("and %d more", len(exts)-8))
		}
		add(PlanFinding{Severity: "warning", Kind: "single-trunk", Title: "Emergency calls rest on one trunk",
			Says:  "Emergency calls from " + strings.Join(shown, ", ") + " go out through " + name + " only: if it fails, they cannot get out. Add a second trunk to the emergency route.",
			Where: PlanWhere{Kind: "outbound", Name: name}},
			PlanSituation{From: exts[0], To: cfg.System.EmergencyNumbers[0], When: "any time", Toggles: map[string]bool{}, Path: []string{name + " fails"}})
	}

	// Things nothing routes to. Menus with a number of their own can still be dialled.
	if len(entries) > 0 {
		for _, m := range cfg.Menus {
			if !reached["menu\x00"+m.ID] && m.Number == "" {
				add(PlanFinding{Severity: "info", Kind: "unreachable", Title: "Nothing routes to it", Says: "No incoming call, in any situation, reaches the menu " + m.Name + ", and it has no number to dial.", Where: PlanWhere{Kind: "menu", Target: m.ID, Name: m.Name}}, PlanSituation{Toggles: map[string]bool{}})
			}
		}
		for _, s := range cfg.Schedules {
			if !reached["schedule\x00"+s.ID] {
				add(PlanFinding{Severity: "info", Kind: "unreachable", Title: "Nothing routes to it", Says: "No incoming call, in any situation, reaches the schedule " + s.Name + ".", Where: PlanWhere{Kind: "schedule", Target: s.ID, Name: s.Name}}, PlanSituation{Toggles: map[string]bool{}})
			}
		}
	}
	for _, s := range cfg.Schedules {
		got := taken[s.ID]
		if got == nil || len(got) == 2 {
			continue
		}
		never := "Closed"
		if !got["Open"] {
			never = "Open"
		}
		add(PlanFinding{Severity: "warning", Kind: "never-taken", Title: "A branch no situation takes",
			Says:  fmt.Sprintf("%s is never %s at any time in the coming year, so its %s destination is never used. Check its hours and holidays.", s.Name, strings.ToLower(never), strings.ToLower(never)),
			Where: PlanWhere{Kind: "schedule", Target: s.ID, Name: s.Name}}, PlanSituation{When: "the coming year", Toggles: map[string]bool{}})
	}

	rep.Situations = sims
	rank := map[string]int{"error": 0, "warning": 1, "info": 2}
	for _, k := range order {
		f := found[k]
		rep.Findings = append(rep.Findings, *f)
		switch f.Severity {
		case "error":
			rep.Errors++
		case "warning":
			rep.Warnings++
		}
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return rank[rep.Findings[i].Severity] < rank[rep.Findings[j].Severity] })
	return rep
}

func appendOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
