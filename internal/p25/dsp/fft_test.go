package dsp

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestFFTAgainstDFT(t *testing.T) {
	n := 256
	in := make([]complex128, n)
	for i := range in {
		in[i] = complex(math.Sin(float64(i)*0.3)+0.2*float64(i%7), math.Cos(float64(i)*0.11))
	}
	p := New(n)
	got := p.Forward(nil, in)
	for k := 0; k < n; k += 17 {
		var want complex128
		for j, x := range in {
			want += x * cmplx.Exp(complex(0, -2*math.Pi*float64(k*j)/float64(n)))
		}
		if cmplx.Abs(got[k]-want) > 1e-9 {
			t.Fatalf("bin %d: %v, want %v", k, got[k], want)
		}
	}
	back := p.Inverse(nil, got)
	for i := range in {
		if cmplx.Abs(back[i]-in[i]) > 1e-9 {
			t.Fatalf("inverse at %d", i)
		}
	}
}
