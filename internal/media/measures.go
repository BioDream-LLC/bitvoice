package media

// Talk is how a two-sided conversation went, measured from the sound alone: no model, no score.
type Talk struct {
	Seconds       float64 `json:"seconds"`       // length of the recording
	CallerTalk    float64 `json:"callerTalk"`    // seconds the caller spoke
	CalleeTalk    float64 `json:"calleeTalk"`    // seconds the other side spoke
	Overlap       float64 `json:"overlap"`       // seconds both spoke at once
	Silence       float64 `json:"silence"`       // seconds nobody spoke
	LongestCaller float64 `json:"longestCaller"` // the caller's longest stretch
	LongestCallee float64 `json:"longestCallee"` // the other side's longest stretch
	// CallerCutIn and CalleeCutIn count each side starting to talk while the other was still talking
	// (for more than a second's overlap).
	CallerCutIn int `json:"callerCutIn"`
	CalleeCutIn int `json:"calleeCutIn"`
	// LongestSilence is the longest stretch nobody spoke (a long hold or dead air).
	LongestSilence float64 `json:"longestSilence"`
}

// Measure measures a conversation from its two sides.
func Measure(left, right []int16, rate int) Talk {
	n := max(len(left), len(right))
	t := Talk{Seconds: float64(n) / float64(rate)}
	if n == 0 {
		return t
	}
	a, b := SpeechSegments(left, rate), SpeechSegments(right, rate)
	sec := func(s Segment) float64 { return float64(s.End-s.Start) / float64(rate) }
	// Speech splits at 30 s, so join pieces that touch before taking the longest stretch.
	longest := func(ss []Segment) float64 {
		best, cur, end := 0.0, 0.0, -1
		for _, s := range ss {
			if s.Start <= end+rate/10 {
				cur += sec(s)
			} else {
				cur = sec(s)
			}
			end = s.End
			best = max(best, cur)
		}
		return best
	}
	for _, s := range a {
		t.CallerTalk += sec(s)
	}
	for _, s := range b {
		t.CalleeTalk += sec(s)
	}
	t.LongestCaller, t.LongestCallee = longest(a), longest(b)
	// A per-20 ms map of who is talking, for overlap and silence.
	frame := rate / 50
	frames := n/frame + 1
	mark := func(ss []Segment, bit byte, m []byte) {
		for _, s := range ss {
			for f := s.Start / frame; f < s.End/frame && f < frames; f++ {
				m[f] |= bit
			}
		}
	}
	m := make([]byte, frames)
	mark(a, 1, m)
	mark(b, 2, m)
	run := 0
	for _, x := range m {
		switch x {
		case 3:
			t.Overlap += 0.02
		case 0:
			t.Silence += 0.02
		}
		if x == 0 {
			run++
			t.LongestSilence = max(t.LongestSilence, float64(run)*0.02)
		} else {
			run = 0
		}
	}
	cut := func(starts, other []Segment) int {
		c := 0
		for _, s := range starts {
			for _, o := range other {
				if o.Start < s.Start && s.Start < o.End && min(o.End, s.End)-s.Start > rate {
					c++
					break
				}
			}
		}
		return c
	}
	t.CallerCutIn, t.CalleeCutIn = cut(a, b), cut(b, a)
	round := func(f *float64) { *f = float64(int(*f*10+0.5)) / 10 }
	for _, f := range []*float64{&t.Seconds, &t.CallerTalk, &t.CalleeTalk, &t.Overlap, &t.Silence, &t.LongestCaller, &t.LongestCallee, &t.LongestSilence} {
		round(f)
	}
	return t
}
