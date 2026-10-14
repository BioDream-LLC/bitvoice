package dahdi

import (
	"math"
	"math/rand/v2"
	"testing"
)

func keyTone(k byte, ms int, level float64) []int16 {
	var r, c float64
	for i, row := range keys {
		for j, x := range row {
			if x == k {
				r, c = rowFreqs[i], colFreqs[j]
			}
		}
	}
	out := make([]int16, 8*ms)
	for n := range out {
		t := float64(n) / 8000
		out[n] = int16(level * 16000 * (math.Sin(2*math.Pi*r*t) + math.Sin(2*math.Pi*c*t)) / 2)
	}
	return out
}

func TestDTMF(t *testing.T) {
	var d Detector
	var got []byte
	gap := make([]int16, 8*60)
	for _, k := range []byte("2125550199*#") {
		got = append(got, d.Feed(keyTone(k, 80, 0.5))...)
		got = append(got, d.Feed(gap)...)
	}
	if string(got) != "2125550199*#" {
		t.Fatalf("heard %q", got)
	}
	// Quiet keys still count; a long press counts once.
	got = append(d.Feed(keyTone('5', 400, 0.05)), d.Feed(gap)...)
	if string(got) != "5" {
		t.Fatalf("quiet or long key heard as %q", got)
	}
	// Speech-like noise and a single tone are not keys.
	rng := rand.New(rand.NewPCG(1, 2))
	noise := make([]int16, 8000)
	for i := range noise {
		noise[i] = int16(rng.NormFloat64() * 4000)
	}
	one := make([]int16, 8000)
	for n := range one {
		one[n] = int16(8000 * math.Sin(2*math.Pi*1000*float64(n)/8000))
	}
	if k := append(d.Feed(noise), d.Feed(one)...); len(k) != 0 {
		t.Fatalf("heard %q in noise and a 1 kHz tone", k)
	}
}
