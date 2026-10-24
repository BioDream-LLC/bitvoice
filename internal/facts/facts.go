// Package facts reads a call's transcript and writes down what was said, without a language model:
// the incident, where, who is hurt and how, hazards, vehicles, who the caller is to the patient and
// a callback number. Every fact carries the sentence it came from and the side that said it, and
// nothing is added that no one said: a denial ("nobody's hurt") is a fact of its own, a report of
// what someone else said is marked as reported, a correction ("no, 42") replaces what it corrects,
// and a bare "no" or "yes" answers the question asked just before it.
//
// It is faithful rather than complete: rules and word lists find what they know and miss the rest.
// What it finds on three sets of invented calls is measured by the tests and published in the
// manual; nothing here was written from a real caller's words.
package facts

import (
	"regexp"
	"slices"
	"strings"
)

// Line is one stretch of a transcript: who said it (caller or callee, the side that answered) and
// the words.
type Line struct {
	Side string  `json:"side"`
	Text string  `json:"text"`
	At   float64 `json:"at,omitempty"` // seconds into the call
}

// Fact is one thing a call said, with the sentence it came from.
type Fact struct {
	Kind     string  `json:"kind"`  // incident, location, injured, condition, hazard, vehicle, relation, callback
	Value    string  `json:"value"` // lower case; injured is a count or "none"; callback is digits
	Quote    string  `json:"quote"`
	Side     string  `json:"side"`
	Line     int     `json:"line"`
	At       float64 `json:"at,omitempty"`
	Reported bool    `json:"reported,omitempty"` // the caller passes on what someone else said
}

// Kinds are the kinds of facts, in the order a summary gives them.
var Kinds = []string{"incident", "location", "injured", "condition", "hazard", "vehicle", "relation", "callback"}

type sentence struct {
	text, low string
	line      int
	side      string
	at        float64
	question  string // for a caller's sentence: the other side's last question before it, lower case
	reported  bool
}

var (
	sentSplit = regexp.MustCompile(`[^.!?]+[.!?]*`)
	words     = regexp.MustCompile(`[a-z0-9']+`)
)

func sentences(lines []Line) []sentence {
	var out []sentence
	lastQ := ""
	for i, l := range lines {
		side := strings.ToLower(strings.TrimSpace(l.Side))
		for _, s := range sentSplit.FindAllString(l.Text, -1) {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			low := strings.ToLower(s)
			if side != "caller" {
				if strings.HasSuffix(s, "?") {
					lastQ = low
				}
				out = append(out, sentence{text: s, low: low, line: i, side: side, at: l.At})
				continue
			}
			rep := reportedRe.MatchString(low)
			out = append(out, sentence{text: s, low: low, line: i, side: side, at: l.At, question: lastQ, reported: rep})
		}
		if side == "caller" {
			// A question answered once is not the context of the caller's next answer.
		} else if !strings.HasSuffix(strings.TrimSpace(l.Text), "?") {
			lastQ = ""
		}
	}
	return out
}

// reportedRe: the caller passes on someone else's words.
var reportedRe = regexp.MustCompile(`\b(she|he|they|my \w+|the \w+|someone|somebody) (says|said|tells me|told me)\b`)

// negatedRe: a clause that denies (no, not, nobody, doesn't ...).
var negatedRe = regexp.MustCompile(`\b(no|not|nobody|no one|none|never|isn't|aren't|wasn't|doesn't|don't|didn't|won't|can't|without)\b|n't\b`)

func has(low string, any ...string) bool {
	for _, a := range any {
		if strings.Contains(low, a) {
			return true
		}
	}
	return false
}

// clauses splits a sentence at commas, "but" and "and" where a new clause begins, so a denial in
// one part does not reach the other ("he doesn't have a gun, but there are knives").
func clauses(low string) []string {
	out := []string{}
	for _, p := range regexp.MustCompile(`,|;| but | and then `).Split(low, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// answer reads a short answer to a yes/no question: +1 yes, -1 no, 0 not a bare answer.
func answer(low string) int {
	w := words.FindAllString(low, -1)
	if len(w) == 0 {
		return 0
	}
	switch w[0] {
	case "yes", "yeah", "yep", "uh-huh":
		return 1
	case "no", "nope", "nah":
		return -1
	}
	if strings.HasPrefix(low, "not that i") || strings.HasPrefix(low, "i don't think so") {
		return -1
	}
	return 0
}

// Extract reads the facts of a transcript, in the order the call gave them.
func Extract(lines []Line) []Fact {
	ss := sentences(lines)
	var out []Fact
	add := func(s sentence, kind, value string) {
		for i, f := range out {
			if f.Kind == kind && f.Value == value {
				_ = i
				return
			}
		}
		out = append(out, Fact{Kind: kind, Value: value, Quote: s.text, Side: s.side, Line: s.line, At: s.at, Reported: s.reported})
	}
	caller := ss[:0:0]
	for _, s := range ss {
		if s.side == "caller" {
			caller = append(caller, s)
		}
	}
	location(caller, &out)
	incident(caller, add)
	people(caller, add, &out)
	hazards(caller, add)
	vehicles(caller, add)
	relation(caller, add)
	callback(caller, &out)
	order := map[string]int{}
	for i, k := range Kinds {
		order[k] = i
	}
	slices.SortStableFunc(out, func(a, b Fact) int { return order[a.Kind] - order[b.Kind] })
	return out
}

var (
	suffix = `(?:street|st|avenue|ave|road|rd|lane|ln|drive|dr|court|ct|boulevard|blvd|way|place|pl|turnpike|square|pike|parkway|highway|circle|terrace|trail)`
	name   = `(?:[a-z0-9]+ ){0,3}?`
	// 418 maple avenue
	numbered = regexp.MustCompile(`\b(\d{1,6}) (` + name + `[a-z0-9]+ ` + suffix + `)\b`)
	// brambleton avenue and grandin road
	crossing = regexp.MustCompile(`\b(` + name + `[a-z0-9]+ ` + suffix + `) (?:and|&|at) (` + name + `[a-z0-9]+ ` + suffix + `)\b`)
	// route 220 near mile marker 12; interstate 81 northbound at exit 140
	highway = regexp.MustCompile(`\b((?:route|interstate|highway|i-|us) ?\d+(?: (?:north|south|east|west)bound)?(?: (?:near|at) (?:mile marker|exit) \d+)?)`)
	// on grove avenue by the park
	onStreet  = regexp.MustCompile(`\b(?:on|at) (` + name + `[a-z]+ ` + suffix + `)(?: (by|near|behind|outside) the ([a-z]+))?`)
	correctRe = regexp.MustCompile(`\b(no,? sorry|no wait|no,? wait|sorry|i mean|actually)\b`)
)

// streetOnly drops the words before a street's name that are not part of it ("a crash at",
// "corner of").
func streetOnly(s string) string {
	w := strings.Fields(s)
	for i := len(w) - 2; i >= 0; i-- {
		switch w[i] {
		case "at", "on", "of", "near", "by", "in", "the", "a", "an", "from", "to":
			return strings.Join(w[i+1:], " ")
		}
	}
	return s
}

// location keeps the first place the caller gives, unless a later sentence corrects it.
func location(ss []sentence, out *[]Fact) {
	found := -1
	for _, s := range ss {
		var vals []string
		for _, m := range numbered.FindAllStringSubmatch(s.low, -1) {
			vals = append(vals, m[1]+" "+m[2])
		}
		if len(vals) == 0 {
			if m := crossing.FindStringSubmatch(s.low); m != nil {
				vals = append(vals, streetOnly(m[1])+" and "+m[2])
			} else if m := highway.FindStringSubmatch(s.low); m != nil {
				vals = append(vals, m[1])
			} else if m := onStreet.FindStringSubmatch(s.low); m != nil {
				v := m[1]
				if m[2] != "" {
					v += " " + m[2] + " the " + m[3]
				}
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		v := vals[len(vals)-1] // within a sentence, the last said (a correction comes after)
		f := Fact{Kind: "location", Value: v, Quote: s.text, Side: s.side, Line: s.line, At: s.at}
		switch {
		case found < 0:
			*out = append(*out, f)
			found = len(*out) - 1
		case correctRe.MatchString(s.low):
			(*out)[found] = f
		}
	}
}

// incidents, most specific first: the first that the caller's words match is the call's.
var incidents = []struct {
	value string
	match func(low string) bool
}{
	{"vehicle fire", func(l string) bool {
		return has(l, "car is smoking", "car is on fire", "car on fire", "truck is on fire", "vehicle fire", "car fire")
	}},
	{"gas leak", func(l string) bool { return has(l, "smell of gas", "smells of gas", "smell gas", "gas leak") }},
	{"fire", func(l string) bool { return has(l, "fire", "flames", "on fire") }},
	{"crash", func(l string) bool {
		return has(l, "crash", "wreck", "collision", "accident") || (has(l, " hit ") && has(l, "car", "truck", "van", "vehicle"))
	}},
	{"burglary", func(l string) bool { return has(l, "broke into", "break-in", "breaking into", "broken into") }},
	{"assault", func(l string) bool { return has(l, "hit another", "punched", "attacked", "beating", "stabbed", "fight") }},
	{"medical", func(l string) bool {
		return has(l, "collapsed", "chest pain", "not breathing", "isn't breathing", "unconscious", "heart attack", "passed out")
	}},
}

func incident(ss []sentence, add func(sentence, string, string)) {
	for _, in := range incidents {
		for _, s := range ss {
			for _, c := range clauses(s.low) {
				if in.match(" "+c+" ") && !(negatedRe.MatchString(c) && in.value != "medical") {
					add(s, "incident", in.value)
					return
				}
			}
		}
	}
}

// conditions: what a patient's state is, as the caller says it.
var conditions = []struct {
	value string
	re    *regexp.Regexp
}{
	{"not breathing", regexp.MustCompile(`\b(not breathing|isn't breathing|stopped breathing|can't breathe)\b`)},
	{"breathing", regexp.MustCompile(`\b(i can breathe|he's breathing|she's breathing|is breathing)\b`)},
	{"unconscious", regexp.MustCompile(`\b(unconscious|unresponsive|passed out|won't wake up)\b`)},
	{"conscious", regexp.MustCompile(`\b(awake|conscious|talking to me)\b`)},
	{"bleeding", regexp.MustCompile(`\bbleeding\b`)},
	{"chest pain", regexp.MustCompile(`\bchest pain|pain in (my|his|her) chest\b`)},
}

// people finds conditions, and from them and the caller's denials, how many are hurt.
func people(ss []sentence, add func(sentence, string, string), out *[]Fact) {
	hurt, none := 0, -1
	var hurtAt, noneAt sentence
	for _, s := range ss {
		q := s.question
		yn := answer(s.low)
		// A bare yes or no answers the question before it.
		if yn != 0 {
			switch {
			case has(q, "trouble breathing", "difficulty breathing", "short of breath", "hard to breathe"):
				if yn < 0 { // "no" to trouble breathing: breathing
					add(s, "condition", "breathing")
				}
			case has(q, "breathing"):
				if yn > 0 {
					add(s, "condition", "breathing")
				} else {
					add(s, "condition", "not breathing")
				}
			case has(q, "awake", "conscious"):
				if yn > 0 {
					add(s, "condition", "conscious")
				} else {
					add(s, "condition", "unconscious")
				}
			case has(q, "hurt", "injured", "injuries"):
				if yn < 0 && none < 0 {
					none, noneAt = 0, s
				}
			}
		}
		for _, c := range conditions {
			if m := c.re.FindString(s.low); m != "" {
				if c.value == "breathing" && regexp.MustCompile(`\bnot\b`).MatchString(s.low) {
					continue
				}
				if c.value == "conscious" && has(s.low, "unconscious") {
					continue
				}
				add(s, "condition", c.value)
				if hurt == 0 {
					hurt, hurtAt = 1, s
				}
			}
		}
		for _, c := range clauses(s.low) {
			if has(c, "nobody's hurt", "nobody is hurt", "no one is hurt", "no one's hurt", "nobody's injured", "not hurt", "nobody hurt", "i'm okay", "he's fine", "she's fine") {
				if none < 0 {
					none, noneAt = 0, s
				}
				continue
			}
			if n := countHurt(c); n > hurt {
				hurt, hurtAt = n, s
			}
		}
		if has(s.low, "collapsed", "fell", "is having", "i'm having", "is on the ground") && hurt == 0 {
			hurt, hurtAt = 1, s
		}
	}
	switch {
	case hurt > 0:
		add(hurtAt, "injured", itoa(hurt))
	case none == 0:
		add(noneAt, "injured", "none")
	}
}

var countRe = regexp.MustCompile(`\b(one|two|three|four|five|six|\d+) (people|persons|of them|kids|children|guys|men|women)? ?(are|is)? ?(hurt|injured)\b`)

func countHurt(c string) int {
	m := countRe.FindStringSubmatch(c)
	if m == nil {
		return 0
	}
	if negatedRe.MatchString(c) {
		return 0
	}
	return number(m[1])
}

func number(w string) int {
	switch w {
	case "one", "a":
		return 1
	case "two":
		return 2
	case "three":
		return 3
	case "four":
		return 4
	case "five":
		return 5
	case "six":
		return 6
	}
	n := 0
	for _, r := range w {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

var hazardWords = []struct {
	value string
	re    *regexp.Regexp
}{
	{"fire", regexp.MustCompile(`\b(fire|flames)\b`)},
	{"smoke", regexp.MustCompile(`\b(smoke|smoking|smoky)\b`)},
	{"gas", regexp.MustCompile(`\b(smells? of gas|smell gas|gas leak)\b`)},
	{"knife", regexp.MustCompile(`\b(knife|knives)\b`)},
	{"gun", regexp.MustCompile(`\b(gun|pistol|rifle|shotgun|firearm)\b`)},
}

func hazards(ss []sentence, add func(sentence, string, string)) {
	for _, s := range ss {
		for _, c := range clauses(s.low) {
			for _, h := range hazardWords {
				if h.re.MatchString(c) && !negatedRe.MatchString(c) {
					add(s, "hazard", h.value)
				}
			}
		}
		// "Yes" to "does he have a weapon?" with the weapon named in the question.
		if answer(s.low) > 0 {
			for _, h := range hazardWords {
				if h.re.MatchString(s.question) {
					add(s, "hazard", h.value)
				}
			}
		}
	}
}

var vehicleRe = regexp.MustCompile(`\b(blue|white|black|red|green|silver|grey|gray|dark|yellow|tan|brown|orange|gold) (sedan|van|minivan|car|truck|pickup truck|pickup|suv|motorcycle|bus|honda|toyota|ford|chevy|jeep|nissan)\b`)

func vehicles(ss []sentence, add func(sentence, string, string)) {
	for _, s := range ss {
		for _, m := range vehicleRe.FindAllStringSubmatch(s.low, -1) {
			add(s, "vehicle", m[1]+" "+m[2])
		}
	}
}

var relations = []struct {
	value string
	re    *regexp.Regexp
}{
	{"neighbour", regexp.MustCompile(`\bmy (neighbou?r)\b|\bnext door\b`)},
	{"family", regexp.MustCompile(`\bmy (mother|mom|mum|father|dad|husband|wife|son|daughter|baby|child|brother|sister|grandmother|grandfather|grandma|grandpa)\b`)},
	{"friend", regexp.MustCompile(`\bmy (friend|roommate)\b`)},
	{"coworker", regexp.MustCompile(`\b(a|my) (coworker|co-worker|colleague)\b`)},
	{"self", regexp.MustCompile(`^i'm having\b|^i (burned|cut|fell|was hit)\b|\bi'm hurt\b`)},
}

// relation: who the caller is to the person in need, the first said.
func relation(ss []sentence, add func(sentence, string, string)) {
	for _, s := range ss {
		for _, r := range relations {
			if r.re.MatchString(s.low) {
				add(s, "relation", r.value)
				return
			}
		}
	}
}

var phoneRe = regexp.MustCompile(`(?:\+?1[ .-]?)?\(?(\d{3})\)?[ .-]?(\d{3})[ .-]?(\d{4})\b`)
var lastFour = regexp.MustCompile(`\b(?:no,? wait|no,? sorry|sorry|i mean),? (\d{4})\b`)

// callback: the number the caller gives; "no wait, 0171" mends its last four digits.
func callback(ss []sentence, out *[]Fact) {
	at := -1
	for _, s := range ss {
		if m := phoneRe.FindStringSubmatch(s.low); m != nil {
			v := m[1] + m[2] + m[3]
			if fix := lastFour.FindStringSubmatch(s.low); fix != nil {
				v = v[:6] + fix[1]
			}
			f := Fact{Kind: "callback", Value: v, Quote: s.text, Side: s.side, Line: s.line, At: s.at}
			if at < 0 {
				*out = append(*out, f)
				at = len(*out) - 1
			} else if correctRe.MatchString(s.low) {
				(*out)[at] = f
			}
			continue
		}
		if fix := lastFour.FindStringSubmatch(s.low); fix != nil && at >= 0 {
			(*out)[at].Value = (*out)[at].Value[:6] + fix[1]
		}
	}
}
