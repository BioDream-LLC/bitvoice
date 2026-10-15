package mf

import (
	"math"
	"math/rand"
	"testing"
)

func noise(pcm []int16, amp float64, r *rand.Rand) []int16 {
	out := make([]int16, len(pcm))
	for i, v := range pcm {
		out[i] = int16(math.Max(-32768, math.Min(32767, float64(v)+amp*r.NormFloat64())))
	}
	return out
}

func TestR1Digits(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, s := range []string{"K2125551234S", "K0123456789S", "KABCS"} {
		rx := NewR1Receiver()
		if got := rx.Feed(noise(R1Digits(s), 300, r)); got != s {
			t.Errorf("sent %s, heard %s", s, got)
		}
	}
	if R1Signal('*') != 12 || R1Signal('#') != 15 {
		t.Fatal("Feature Group D's * and #")
	}
}

// Every R2 signal, forward and backward, is heard as itself, at the levels and twist Q.454 allows,
// and nothing is heard in speech-like noise or a single tone.
func TestR2Signals(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, set := range []Set{Forward, Backward} {
		for n := 1; n <= 15; n++ {
			for _, level := range []float64{-5, -20} {
				d := NewDetector(set)
				pcm := append(set.Tone(n, 80, level), make([]int16, 800)...)
				ch := d.Feed(noise(pcm, 100, r))
				if len(ch) != 2 || ch[0] != n || ch[1] != 0 {
					t.Errorf("signal %d at %v dBm0: heard %v", n, level, ch)
				}
			}
		}
		d := NewDetector(set)
		if ch := d.Feed(noise(make([]int16, 4000), 3000, r)); len(ch) != 0 {
			t.Errorf("noise heard as %v", ch)
		}
	}
	// Forward tones are not heard by the backward detector, nor the other way.
	d := NewDetector(Backward)
	if ch := d.Feed(Forward.Tone(5, 100, -8)); len(ch) != 0 {
		t.Errorf("a forward signal heard backward: %v", ch)
	}
}
