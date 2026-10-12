// Package dsp holds the signal processing the IMBE vocoder needs: a radix-2 FFT and a biquad
// filter.
package dsp

import (
	"math"
	"math/bits"
)

// Plan is a complex FFT of one size. Forward computes X[k] = sum x[n] e^(-2 pi i k n / N);
// Inverse its inverse, normalised (divided by N).
type Plan interface {
	Size() int
	Forward(out, in []complex128) []complex128
	Inverse(out, in []complex128) []complex128
}

type radix2 struct {
	n   int
	rev []int
	tw  []complex128 // e^(-2 pi i k / n), k < n/2
}

// New makes a plan for a power-of-two size.
func New(n int) Plan {
	if n < 1 || n&(n-1) != 0 {
		panic("fft: size must be a power of two")
	}
	p := &radix2{n: n, rev: make([]int, n), tw: make([]complex128, n/2)}
	shift := 64 - bits.Len(uint(n-1))
	for i := range p.rev {
		if n > 1 {
			p.rev[i] = int(bits.Reverse64(uint64(i)) >> shift)
		}
	}
	for k := range p.tw {
		s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		p.tw[k] = complex(c, s)
	}
	return p
}

func (p *radix2) Size() int { return p.n }

func (p *radix2) transform(out, in []complex128, inverse bool) []complex128 {
	if len(in) != p.n {
		panic("fft: input length != plan size")
	}
	if cap(out) < p.n {
		out = make([]complex128, p.n)
	} else {
		out = out[:p.n]
	}
	if &out[0] == &in[0] {
		tmp := append([]complex128(nil), in...)
		in = tmp
	}
	for i, r := range p.rev {
		out[r] = in[i]
	}
	for size := 2; size <= p.n; size <<= 1 {
		half, step := size/2, p.n/size
		for start := 0; start < p.n; start += size {
			for k := 0; k < half; k++ {
				w := p.tw[k*step]
				if inverse {
					w = complex(real(w), -imag(w))
				}
				a, b := out[start+k], out[start+k+half]*w
				out[start+k], out[start+k+half] = a+b, a-b
			}
		}
	}
	if inverse {
		s := complex(1/float64(p.n), 0)
		for i := range out {
			out[i] *= s
		}
	}
	return out
}

func (p *radix2) Forward(out, in []complex128) []complex128 { return p.transform(out, in, false) }
func (p *radix2) Inverse(out, in []complex128) []complex128 { return p.transform(out, in, true) }
