package sip

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// History-Info (RFC 7044) and Diversion (RFC 5806): how a call was forwarded before it got here.

// Hop is one entry of a call's history: a number it was sent to, why it left there (a forward, a
// no-answer, a busy line), and its place in the history.
type Hop struct {
	// Index is History-Info's dotted index (1, 1.1, 1.1.1); for Diversion, the order it was listed in.
	Index string
	URI   *URI
	// Cause is the SIP code the call left this target with (302 moved, 486 busy, 408 no answer ...),
	// from the next entry's cause (RFC 4458) or a Reason header (Q.850 or SIP) inside the URI.
	Cause int
	// Reason is the diversion reason in words (unconditional, user-busy, no-answer, deflection, ...).
	Reason string
	// Tag is rc, mp or np (RFC 7044): retargeted to the same user, mapped to another user, unchanged.
	Tag string
	// Privacy says the entry is not to be shown (Privacy=history inside the URI).
	Privacy bool
}

// Number is the hop's user part: the number the call was for.
func (h Hop) Number() string {
	if h.URI == nil {
		return ""
	}
	return h.URI.User
}

// diversionReasons turn RFC 5806 reasons into the SIP causes RFC 7044 uses, and back (RFC 7544, RFC 4458).
var diversionReasons = map[string]int{"unconditional": 302, "user-busy": 486, "no-answer": 408, "deflection": 480,
	"unavailable": 503, "time-of-day": 404, "do-not-disturb": 487, "follow-me": 302, "out-of-service": 503, "away": 302, "unknown": 404}

var causeReasons = map[int]string{302: "unconditional", 486: "user-busy", 408: "no-answer", 480: "deflection", 487: "do-not-disturb",
	503: "unavailable", 404: "unknown"}

// ReasonCause is the SIP cause of a Diversion reason (302 when unknown).
func ReasonCause(reason string) int {
	if c, ok := diversionReasons[strings.ToLower(strings.TrimSpace(reason))]; ok {
		return c
	}
	return 302
}

// CauseReason is the Diversion reason of a SIP cause.
func CauseReason(cause int) string {
	if r, ok := causeReasons[cause]; ok {
		return r
	}
	return "unknown"
}

// ParseHistoryInfo reads every History-Info entry of a message, in index order.
func ParseHistoryInfo(m *Message) []Hop {
	var out []Hop
	for _, v := range m.GetAll("History-Info") {
		for _, e := range splitList(v) {
			a, err := ParseAddress(e)
			if err != nil || a.URI == nil {
				continue
			}
			h := Hop{URI: a.URI}
			h.Index, _ = a.Params.Get("index")
			for _, t := range []string{"rc", "mp", "np"} {
				if _, ok := a.Params.Get(t); ok {
					h.Tag = t
				}
			}
			if c, ok := a.URI.Params.Get("cause"); ok {
				h.Cause, _ = strconv.Atoi(c)
			}
			// The URI's own headers: Reason (why the request left this target) and Privacy.
			hdrs, _ := url.ParseQuery(a.URI.Headers)
			if r := hdrs.Get("Reason"); r != "" && h.Cause == 0 {
				h.Cause = reasonCause(r)
			}
			if p := hdrs.Get("Privacy"); strings.Contains(strings.ToLower(p), "history") {
				h.Privacy = true
			}
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return indexLess(out[i].Index, out[j].Index) })
	return out
}

// reasonCause reads a Reason header value: SIP ;cause=486, or Q.850 ;cause=17 (busy) and others.
func reasonCause(v string) int {
	proto, params, _ := strings.Cut(v, ";")
	ps := parseParams(params)
	c, _ := ps.Get("cause")
	n, _ := strconv.Atoi(c)
	switch strings.ToUpper(strings.TrimSpace(proto)) {
	case "SIP":
		return n
	case "Q.850":
		switch n {
		case 17:
			return 486
		case 18, 19:
			return 408
		case 21:
			return 603
		}
		return 480
	}
	return 0
}

// indexLess orders dotted indexes: 1 < 1.1 < 1.1.1 < 1.2 < 2.
func indexLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// ParseDiversion reads Diversion headers (RFC 5806), newest first as they are listed.
func ParseDiversion(m *Message) []Hop {
	var out []Hop
	for _, v := range m.GetAll("Diversion") {
		for _, e := range splitList(v) {
			a, err := ParseAddress(e)
			if err != nil || a.URI == nil {
				continue
			}
			r, _ := a.Params.Get("reason")
			r = strings.Trim(r, `"`)
			h := Hop{Index: strconv.Itoa(len(out) + 1), URI: a.URI, Reason: r, Cause: ReasonCause(r)}
			if p, _ := a.Params.Get("privacy"); strings.EqualFold(strings.Trim(p, `"`), "full") {
				h.Privacy = true
			}
			out = append(out, h)
		}
	}
	return out
}

// Forward is what BitVoice needs to know about how a call reached it: the number it was first for
// (the original called number), the last number that forwarded it to here, and why.
type Forward struct {
	Original string // the number first dialled
	From     string // the last number that forwarded the call here
	Reason   string // why it was forwarded from there
	Hidden   bool   // the history asked not to be shown
	Hops     int
}

// ForwardOf reads how a call was forwarded: History-Info when the message has it (the newest
// standard, which Teams and many carriers send), else Diversion. ok is false when it was not forwarded.
func ForwardOf(m *Message) (Forward, bool) {
	if hi := ParseHistoryInfo(m); len(hi) >= 2 {
		// The entries are the targets in turn; the last one is where the call is now, and the one
		// before it is the target that sent it here (its cause says why it left there).
		f := Forward{Original: hi[0].Number(), Hops: len(hi) - 1}
		last := hi[len(hi)-1]
		prev := hi[len(hi)-2]
		f.From = prev.Number()
		cause := last.Cause
		if cause == 0 {
			cause = prev.Cause
		}
		if cause == 0 {
			cause = 302
		}
		f.Reason = CauseReason(cause)
		for _, h := range hi {
			f.Hidden = f.Hidden || h.Privacy
		}
		return f, f.From != "" && !strings.EqualFold(f.From, last.Number())
	}
	if dv := ParseDiversion(m); len(dv) > 0 {
		f := Forward{From: dv[0].Number(), Reason: dv[0].Reason, Original: dv[len(dv)-1].Number(), Hops: len(dv)}
		if f.Reason == "" {
			f.Reason = "unknown"
		}
		for _, h := range dv {
			f.Hidden = f.Hidden || h.Privacy
		}
		return f, f.From != ""
	}
	return Forward{}, false
}

// HistoryInfo builds History-Info entries for a call forwarded from a number to a target, on top of
// the entries the call arrived with (whose indexes are kept): the forwarding target gets the next
// index, and the new target a child of it with the cause of the forward (RFC 7044 with RFC 4458).
func HistoryInfo(earlier []Hop, from, to *URI, reason string) []string {
	var out []string
	base := "1"
	if len(earlier) > 0 {
		for _, h := range earlier {
			out = append(out, hiEntry(h.URI, h.Index, h.Tag, h.Cause))
		}
		base = earlier[len(earlier)-1].Index
		// The forwarding number is already the last entry when the call came in for it.
		if earlier[len(earlier)-1].Number() != from.User {
			base = nextSibling(base)
			out = append(out, hiEntry(from, base, "rc", 0))
		}
	} else {
		out = append(out, hiEntry(from, base, "", 0))
	}
	out = append(out, hiEntry(to, base+".1", "rc", ReasonCause(reason)))
	return out
}

func nextSibling(idx string) string {
	parts := strings.Split(idx, ".")
	n, _ := strconv.Atoi(parts[len(parts)-1])
	parts[len(parts)-1] = strconv.Itoa(n + 1)
	return strings.Join(parts, ".")
}

func hiEntry(u *URI, index, tag string, cause int) string {
	c := *u
	c.Params = append(Params(nil), u.Params...)
	c.Params.Del("cause")
	if cause != 0 {
		c.Params.Set("cause", strconv.Itoa(cause))
	}
	s := "<" + c.String() + ">;index=" + index
	if tag != "" {
		s += ";" + tag + "=" + parentIndex(index)
	}
	return s
}

// parentIndex is the index an rc/mp tag points to: the entry this one was retargeted from.
func parentIndex(idx string) string {
	if i := strings.LastIndexByte(idx, '.'); i > 0 {
		return idx[:i]
	}
	return idx
}
