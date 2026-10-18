// Package reason is BitVoice's judge: a rulebook of facts, conclusions and actions, with the exceptions that knock
// them down, decided under grounded semantics (internal/af) into one action and the reasons for it.
//
// The rules a judge keeps, whatever its rulebook says:
//   - Every answer comes with its reasons: the conclusions that held, what was argued against each and what
//     defeated it, and the facts it rested on.
//   - A fact nobody measured is unknown, never false. When an unknown fact could change the answer, the judge says
//     so and takes the rulebook's cautious action: silence is never taken for safety.
//   - "Cannot determine" is an answer. A rulebook whose rules leave a situation undecided takes its cautious action
//     there too, and says it could not decide.
//   - A rulebook is checked against every combination of its facts before it is used (Check): never two actions
//     at once, every action reachable.
package reason

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/af"
)

// Kind is what an argument is.
type Kind string

const (
	Fact       Kind = "fact"       // something measured: present or not
	Conclusion Kind = "conclusion" // follows from its premises unless something defeats it
	Action     Kind = "action"     // what to do; exactly one should hold
)

// Rule is one argument of a rulebook.
type Rule struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	Text string `json:"text"` // in plain words, as a person reads it
	// Needs are the facts and conclusions it rests on, all of them. An argument resting on a conclusion is
	// attacked by whatever attacks that conclusion.
	Needs []string `json:"needs,omitempty"`
	// Does is, for an action, the choice it makes (one of the book's Choices). Several actions may make the
	// same choice for different reasons; two different choices holding at once is a conflict.
	Does string `json:"does,omitempty"`
}

// Choice is something the judge can decide to do.
type Choice struct {
	Does string `json:"does"`
	Text string `json:"text"`
}

// Attack says that one argument, when it holds, knocks another down.
type Attack struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Book is a rulebook.
type Book struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	About   string   `json:"about"`
	Rules   []Rule   `json:"rules"`
	Hits    []Attack `json:"attacks"`
	Choices []Choice `json:"choices"`
	// Cautious is the choice made when the judge cannot decide, or when a fact nobody measured could change
	// the answer. Default is the choice when no action holds at all.
	Cautious string `json:"cautious"`
	Default  string `json:"default"`
}

func (b *Book) choice(does string) (Choice, bool) {
	for _, c := range b.Choices {
		if c.Does == does {
			return c, true
		}
	}
	return Choice{}, false
}

// Truth is what is known about a fact.
type Truth int8

const (
	Unknown Truth = iota
	No
	Yes
)

// Node is a conclusion or action that held, with what it rested on and what was argued against it.
type Node struct {
	Name    string   `json:"name"`
	Kind    Kind     `json:"kind"`
	Text    string   `json:"text"`
	Rests   []*Node  `json:"rests,omitempty"`
	Defeats []Defeat `json:"defeats,omitempty"`
}

// Defeat is something argued against a node and what knocked it down.
type Defeat struct {
	Attacker string `json:"attacker"`
	Text     string `json:"text"`
	By       *Node  `json:"by"`
}

// Verdict is the judge's answer.
type Verdict struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	// How: decided (the rules settled it), cautious (a fact nobody measured could change it, or the rules
	// could not decide), or default (no action applied).
	How   string `json:"how"`
	Proof *Node  `json:"proof,omitempty"`
	// Facts lists the facts that held, Assumed the measured-absent facts the answer relied on, and Unknown the
	// facts nobody measured that could change it.
	Facts   []string `json:"facts"`
	Assumed []string `json:"assumed,omitempty"`
	Unknown []string `json:"unknown,omitempty"`
	// Overruled lists the conclusions that were argued and knocked down, with what knocked each down.
	Overruled []Defeat `json:"overruled,omitempty"`
	// Reasons is the proof in plain sentences, for a log line or an alert.
	Reasons []string `json:"reasons"`
}

func (b *Book) rule(name string) (Rule, bool) {
	for _, r := range b.Rules {
		if r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

// Facts lists the rulebook's facts in order.
func (b *Book) Facts() []string {
	var out []string
	for _, r := range b.Rules {
		if r.Kind == Fact {
			out = append(out, r.Name)
		}
	}
	return out
}

// Validate checks that the rulebook is well formed: unique names, known references, facts with no premises,
// and cautious and default actions that exist.
func (b *Book) Validate() error {
	seen := map[string]Kind{}
	for _, r := range b.Rules {
		if r.Name == "" || strings.ContainsAny(r.Name, " \t") {
			return fmt.Errorf("%s: rule name %q", b.ID, r.Name)
		}
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("%s: %s declared twice", b.ID, r.Name)
		}
		seen[r.Name] = r.Kind
		if r.Kind == Fact && len(r.Needs) > 0 {
			return fmt.Errorf("%s: fact %s cannot rest on anything", b.ID, r.Name)
		}
		if r.Kind != Fact && r.Kind != Conclusion && r.Kind != Action {
			return fmt.Errorf("%s: %s has kind %q", b.ID, r.Name, r.Kind)
		}
		if r.Kind == Action {
			if _, ok := b.choice(r.Does); !ok {
				return fmt.Errorf("%s: action %s makes the unknown choice %q", b.ID, r.Name, r.Does)
			}
		} else if r.Does != "" {
			return fmt.Errorf("%s: only actions make a choice (%s)", b.ID, r.Name)
		}
	}
	for _, r := range b.Rules {
		for _, n := range r.Needs {
			k, ok := seen[n]
			if !ok {
				return fmt.Errorf("%s: %s rests on unknown %s", b.ID, r.Name, n)
			}
			if k == Action {
				return fmt.Errorf("%s: %s rests on the action %s", b.ID, r.Name, n)
			}
		}
	}
	for _, h := range b.Hits {
		if _, ok := seen[h.From]; !ok {
			return fmt.Errorf("%s: attack from unknown %s", b.ID, h.From)
		}
		if k, ok := seen[h.To]; !ok || k == Fact {
			return fmt.Errorf("%s: attack on %s (facts are measured, not argued)", b.ID, h.To)
		}
	}
	for _, a := range []string{b.Cautious, b.Default} {
		if _, ok := b.choice(a); !ok {
			return fmt.Errorf("%s: %q is not a choice", b.ID, a)
		}
	}
	if len(b.Facts()) > 20 {
		return fmt.Errorf("%s: more than 20 facts cannot be checked in every combination", b.ID)
	}
	return nil
}

// solved is one grounded solve over the facts that hold.
type solved struct {
	b    *Book
	f    *af.Framework
	lab  af.Labelling
	held map[string]bool
}

func (b *Book) solve(held map[string]bool) *solved {
	built := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, r := range b.Rules {
			if built[r.Name] {
				continue
			}
			ok := r.Kind != Fact || held[r.Name]
			for _, n := range r.Needs {
				ok = ok && built[n]
			}
			if ok {
				built[r.Name], changed = true, true
			}
		}
	}
	f := af.New()
	for _, r := range b.Rules {
		if built[r.Name] {
			_, _ = f.Add(r.Name)
		}
	}
	// Who attacks what: the explicit attacks, and an argument is attacked by whatever attacks what it rests on.
	direct := map[string][]string{}
	for _, h := range b.Hits {
		direct[h.To] = append(direct[h.To], h.From)
	}
	var attackersOf func(name string, seen map[string]bool) []string
	attackersOf = func(name string, seen map[string]bool) []string {
		if seen[name] {
			return nil
		}
		seen[name] = true
		out := slices.Clone(direct[name])
		r, _ := b.rule(name)
		for _, n := range r.Needs {
			out = append(out, attackersOf(n, seen)...)
		}
		return out
	}
	for _, r := range b.Rules {
		to, ok := f.Lookup(r.Name)
		if !ok {
			continue
		}
		done := map[string]bool{}
		for _, a := range attackersOf(r.Name, map[string]bool{}) {
			if from, ok := f.Lookup(a); ok && !done[a] {
				done[a] = true
				f.Attack(from, to)
			}
		}
	}
	return &solved{b: b, f: f, lab: af.Grounded(f), held: held}
}

// actions returns the choices of the actions that hold (each once), the first action holding for each, and
// whether any action is left undecided.
func (s *solved) actions() (in []string, by map[string]string, undecided bool) {
	by = map[string]string{}
	for _, r := range s.b.Rules {
		if r.Kind != Action {
			continue
		}
		id, ok := s.f.Lookup(r.Name)
		if !ok {
			continue
		}
		switch s.lab.Label[id] {
		case af.In:
			if _, dup := by[r.Does]; !dup {
				in = append(in, r.Does)
				by[r.Does] = r.Name
			}
		case af.Undec:
			undecided = true
		}
	}
	return in, by, undecided
}

// outcome is the choice a solve makes on its own, how, and the action that made it (when one did).
func (s *solved) outcome() (string, string, string) {
	in, by, undecided := s.actions()
	switch {
	case len(in) == 1 && !undecided:
		return in[0], "decided", by[in[0]]
	case len(in) > 0 || undecided:
		return s.b.Cautious, "cautious", ""
	}
	return s.b.Default, "default", ""
}

func (s *solved) node(name string, depth int) *Node {
	r, _ := s.b.rule(name)
	n := &Node{Name: r.Name, Kind: r.Kind, Text: r.Text}
	if depth > 32 {
		return n
	}
	for _, p := range r.Needs {
		if s.held[p] || r.Kind != Fact {
			n.Rests = append(n.Rests, s.node(p, depth+1))
		}
	}
	id, _ := s.f.Lookup(name)
	for _, a := range s.f.Attackers(id) {
		def, ok := af.Defender(s.f, s.lab, a)
		if !ok {
			continue
		}
		ar, _ := s.b.rule(s.f.Name(a))
		n.Defeats = append(n.Defeats, Defeat{Attacker: ar.Name, Text: ar.Text, By: s.node(s.f.Name(def), depth+1)})
	}
	return n
}

// Decide judges the facts. Facts not in the map are unknown.
func (b *Book) Decide(facts map[string]Truth) Verdict {
	held := map[string]bool{}
	var known, unknown, absent []string
	for _, f := range b.Facts() {
		switch facts[f] {
		case Yes:
			held[f] = true
			known = append(known, f)
		case No:
			absent = append(absent, f)
		default:
			unknown = append(unknown, f)
		}
	}
	s := b.solve(held)
	action, how, by := s.outcome()
	v := Verdict{Action: action, How: how, Facts: known}
	// An unknown fact that could change the answer makes the judge cautious (combinations of up to three
	// unknowns are tried; a rulebook has few facts).
	var changes []string
	for _, set := range subsets(unknown, 3) {
		h := map[string]bool{}
		for k := range held {
			h[k] = true
		}
		for _, u := range set {
			h[u] = true
		}
		if a, _, _ := b.solve(h).outcome(); a != action {
			for _, u := range set {
				if !slices.Contains(changes, u) {
					changes = append(changes, u)
				}
			}
		}
	}
	if len(changes) > 0 {
		v.Unknown = changes
		if action != b.Cautious {
			v.Action, v.How = b.Cautious, "cautious"
		}
	}
	// The measured-absent facts the answer relied on: each one alone would have changed it.
	for _, f := range absent {
		h := map[string]bool{f: true}
		for k := range held {
			h[k] = true
		}
		if a, _, _ := b.solve(h).outcome(); a != action {
			v.Assumed = append(v.Assumed, f)
		}
	}
	c, _ := b.choice(v.Action)
	v.Text = c.Text
	if by != "" && v.Action == action {
		v.Proof = s.node(by, 0)
	}
	for _, r := range b.Rules {
		if r.Kind != Conclusion {
			continue
		}
		id, ok := s.f.Lookup(r.Name)
		if !ok || s.lab.Label[id] != af.Out {
			continue
		}
		for _, a := range s.f.Attackers(id) {
			if s.lab.Label[a] == af.In {
				v.Overruled = append(v.Overruled, Defeat{Attacker: r.Name, Text: r.Text, By: s.node(s.f.Name(a), 0)})
				break
			}
		}
	}
	v.Reasons = b.sentences(v)
	return v
}

func (b *Book) text(name string) string {
	r, _ := b.rule(name)
	return r.Text
}

func (b *Book) sentences(v Verdict) []string {
	var out []string
	switch v.How {
	case "decided":
		out = append(out, v.Text+".")
	case "cautious":
		if len(v.Unknown) > 0 {
			out = append(out, "Cautious: "+v.Text+", because something not measured could change the answer.")
		} else {
			out = append(out, "Cannot determine, so the cautious choice: "+v.Text+".")
		}
	default:
		out = append(out, "Nothing applied, so the usual: "+v.Text+".")
	}
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		if n.Kind != Fact {
			out = append(out, "Because "+n.Text+".")
		}
		for _, d := range n.Defeats {
			out = append(out, fmt.Sprintf("Not \"%s\": %s.", d.Text, d.By.Text))
		}
		for _, r := range n.Rests {
			walk(r, depth+1)
		}
	}
	if v.Proof != nil {
		walk(v.Proof, 0)
	}
	for _, o := range v.Overruled {
		s := fmt.Sprintf("Not \"%s\": %s.", o.Text, o.By.Text)
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(v.Facts) > 0 {
		var ts []string
		for _, f := range v.Facts {
			ts = append(ts, b.text(f))
		}
		out = append(out, "Measured: "+strings.Join(ts, "; ")+".")
	}
	for _, f := range v.Unknown {
		out = append(out, "Not measured: "+b.text(f)+".")
	}
	return out
}

// subsets returns the non-empty subsets of list of at most n elements.
func subsets(list []string, n int) [][]string {
	var out [][]string
	var rec func(start int, cur []string)
	rec = func(start int, cur []string) {
		if len(cur) > 0 {
			out = append(out, slices.Clone(cur))
		}
		if len(cur) == n {
			return
		}
		for i := start; i < len(list); i++ {
			rec(i+1, append(cur, list[i]))
		}
	}
	rec(0, nil)
	return out
}

// Problem is something Check found in a rulebook.
type Problem struct {
	Kind      string   `json:"kind"` // conflict (two actions), undecided, unreachable
	Says      string   `json:"says"`
	Situation []string `json:"situation,omitempty"` // the facts that hold in the situation that shows it
}

// Report is the result of checking a rulebook against every combination of its facts.
type Report struct {
	Situations int            `json:"situations"`
	Problems   []Problem      `json:"problems"`
	Reached    map[string]int `json:"reached"` // how many situations end in each action
}

// Check decides every combination of the rulebook's facts and reports two actions at once, situations the rules
// leave undecided, and actions or conclusions no situation reaches.
func (b *Book) Check() Report {
	facts := b.Facts()
	rep := Report{Reached: map[string]int{}, Problems: []Problem{}}
	reached := map[string]bool{}
	firstUndecided := true
	for mask := 0; mask < 1<<len(facts); mask++ {
		held := map[string]bool{}
		var sit []string
		for i, f := range facts {
			if mask&(1<<i) != 0 {
				held[f] = true
				sit = append(sit, f)
			}
		}
		s := b.solve(held)
		rep.Situations++
		in, _, undecided := s.actions()
		for _, r := range b.Rules {
			if id, ok := s.f.Lookup(r.Name); ok && s.lab.Label[id] == af.In {
				reached[r.Name] = true
			}
		}
		if len(in) > 1 {
			rep.Problems = append(rep.Problems, Problem{Kind: "conflict", Situation: sit,
				Says: "two actions at once: " + strings.Join(in, " and ")})
		}
		if undecided && len(in) == 0 && firstUndecided {
			firstUndecided = false
			rep.Problems = append(rep.Problems, Problem{Kind: "undecided", Situation: sit,
				Says: "the rules cannot settle this situation; the cautious action is taken"})
		}
		a, _, _ := s.outcome()
		rep.Reached[a]++
	}
	for _, r := range b.Rules {
		if r.Kind == Fact || reached[r.Name] {
			continue
		}
		rep.Problems = append(rep.Problems, Problem{Kind: "unreachable", Says: string(r.Kind) + " " + r.Name + " never holds"})
	}
	for _, c := range b.Choices {
		if rep.Reached[c.Does] == 0 && !(c.Does == b.Cautious && !b.chosenByAction(c.Does)) {
			rep.Problems = append(rep.Problems, Problem{Kind: "unreachable", Says: "no situation ends in " + c.Does})
		}
	}
	sort.SliceStable(rep.Problems, func(i, j int) bool { return rep.Problems[i].Kind < rep.Problems[j].Kind })
	return rep
}

// chosenByAction reports whether some action makes the choice (a cautious choice may have none: it is what the
// judge does when it cannot decide).
func (b *Book) chosenByAction(does string) bool {
	for _, r := range b.Rules {
		if r.Kind == Action && r.Does == does {
			return true
		}
	}
	return false
}
