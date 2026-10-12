package media

import (
	"math"
	"testing"
)

func TestMeasure(t *testing.T) {
	const rate = 8000
	tone := func(sec float64) []int16 {
		p := make([]int16, int(sec*rate))
		for i := range p {
			p[i] = int16(6000 * math.Sin(float64(i)/4))
		}
		return p
	}
	quiet := func(sec float64) []int16 { return make([]int16, int(sec*rate)) }
	cat := func(parts ...[]int16) []int16 {
		var out []int16
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	// The caller talks 0-4 s; the agent cuts in at 3 s and talks to 9 s; then 5 s of nothing; caller 14-16 s.
	left := cat(tone(4), quiet(10), tone(2))
	right := cat(quiet(3), tone(6), quiet(7))
	m := Measure(left, right, rate)
	near := func(got, want float64) bool { return math.Abs(got-want) < 0.6 }
	if !near(m.Seconds, 16) || !near(m.CallerTalk, 6) || !near(m.CalleeTalk, 6) || !near(m.Overlap, 1) || !near(m.LongestCallee, 6) || !near(m.LongestSilence, 5) {
		t.Fatalf("%+v", m)
	}
	if m.CalleeCutIn != 1 || m.CallerCutIn != 0 {
		t.Fatalf("cut-ins %+v", m)
	}
	if z := Measure(nil, nil, rate); z.Seconds != 0 {
		t.Fatal("empty")
	}
}
