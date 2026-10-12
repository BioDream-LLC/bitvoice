package media

import (
	"math"
	"math/cmplx"
)

// Audio cleanup: noise suppression by spectral subtraction (short-time Fourier transform, a noise
// estimate that follows the quietest level of each frequency, and a smoothed gain per frequency so the
// remaining hiss does not warble), then level control that brings quiet or loud talkers to a steady level
// with a soft limiter. It works on 8 kHz or 16 kHz audio, in a stream (live calls) or all at once
// (voicemail and greetings).

const cleanFrame = 256 // samples per analysis frame: 32 ms at 8 kHz

// Cleaner processes one direction of one stream; keep one per stream.
type Cleaner struct {
	n, hop    int
	win       []float64
	in        []float64 // samples waiting to be analysed
	out       []float64 // overlap-add accumulator
	ready     []int16   // processed samples waiting to be handed out
	power     []float64 // smoothed power per bin
	noise     []float64 // noise estimate per bin
	gain      []float64 // last gain per bin
	frames    int
	agc       float64
	Suppress  bool // noise suppression
	Level     bool // level control
	gateLevel float64
	floor     float64 // background level, followed like the noise estimate
}

// NewCleaner returns a cleaner for a stream at rate (8000 or 16000). Processed audio comes out
// cleanFrame/2 samples late (16 ms at 8 kHz): the stream starts with that much silence.
func NewCleaner(rate int) *Cleaner {
	n := cleanFrame
	if rate >= 16000 {
		n = 2 * cleanFrame
	}
	c := &Cleaner{n: n, hop: n / 2, win: make([]float64, n), out: make([]float64, n),
		power: make([]float64, n/2+1), noise: make([]float64, n/2+1), gain: make([]float64, n/2+1),
		agc: 1, Suppress: true, Level: true, gateLevel: 0.003}
	for i := range c.win {
		c.win[i] = math.Sqrt(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))) // root-Hann: analysis and synthesis
	}
	for i := range c.gain {
		c.gain[i] = 1
	}
	c.ready = make([]int16, c.hop) // the latency, as silence
	return c
}

// Process takes samples and returns as many processed ones.
func (c *Cleaner) Process(in []int16) []int16 {
	for _, v := range in {
		c.in = append(c.in, float64(v)/32768)
		if len(c.in) == c.n {
			c.frame()
			c.in = append(c.in[:0], c.in[c.hop:]...)
		}
	}
	k := min(len(in), len(c.ready))
	out := make([]int16, len(in))
	copy(out[len(in)-k:], c.ready[:k])
	c.ready = c.ready[k:]
	return out
}

func (c *Cleaner) frame() {
	x := make([]complex128, c.n)
	for i, v := range c.in {
		x[i] = complex(v*c.win[i], 0)
	}
	fft(x, false)
	bins := c.n/2 + 1
	if c.Suppress {
		c.frames++
		for k := 0; k < bins; k++ {
			p := real(x[k])*real(x[k]) + imag(x[k])*imag(x[k])
			if c.frames == 1 {
				c.power[k], c.noise[k] = p, p
			}
			c.power[k] = 0.7*c.power[k] + 0.3*p
			// Minimum tracking: falls at once to a quieter level, rises slowly (about 3 dB a second).
			if c.power[k] < c.noise[k] {
				c.noise[k] = c.power[k]
			} else {
				c.noise[k] *= 1.012
			}
			g := 1.0
			if c.power[k] > 0 {
				g = 1 - 2.0*c.noise[k]/c.power[k]
			}
			g = math.Max(g, 0.1) // keep a little of the background: total silence sounds like a dropped call
			g = 0.5*c.gain[k] + 0.5*g
			c.gain[k] = g
			x[k] *= complex(g, 0)
			if k > 0 && k < c.n/2 {
				x[c.n-k] = cmplx.Conj(x[k])
			}
		}
	}
	fft(x, true)
	for i := 0; i < c.n; i++ {
		c.out[i] += real(x[i]) * c.win[i]
	}
	// The first hop is now complete.
	seg := c.out[:c.hop]
	if c.Level {
		c.level(seg)
	}
	for _, v := range seg {
		c.ready = append(c.ready, int16(math.Round(clampf(softLimit(v), -1, 1)*32767)))
	}
	copy(c.out, c.out[c.hop:])
	for i := c.n - c.hop; i < c.n; i++ {
		c.out[i] = 0
	}
}

// level brings speech towards -20 dBFS: gain from -6 to +12 dB, rising slowly and falling fast. Only
// what stands well above the background (12 dB) counts as speech, so hiss is never turned up.
func (c *Cleaner) level(seg []float64) {
	var sum float64
	for _, v := range seg {
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(seg)))
	if c.floor == 0 || rms < c.floor {
		c.floor = math.Max(rms, 1e-5)
	} else {
		c.floor *= 1.01
	}
	if rms > c.gateLevel && rms > 4*c.floor {
		want := clampf(0.1/rms, 0.5, 4)
		rate := 0.02 // slow to raise
		if want < c.agc {
			rate = 0.3 // quick to lower, so a shout does not blast
		}
		c.agc += (want - c.agc) * rate
	}
	for i := range seg {
		seg[i] *= c.agc
	}
}

func softLimit(v float64) float64 {
	const knee = 0.8
	a := math.Abs(v)
	if a <= knee {
		return v
	}
	s := knee + (1-knee)*math.Tanh((a-knee)/(1-knee))
	return math.Copysign(s, v)
}

func clampf(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// fft is an in-place radix-2 Fourier transform; len(x) must be a power of two. inverse also scales by 1/n.
func fft(x []complex128, inverse bool) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Rect(1, sign*2*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				a, b := x[start+k], x[start+k+size/2]*wk
				x[start+k], x[start+k+size/2] = a+b, a-b
				wk *= w
			}
		}
	}
	if inverse {
		for i := range x {
			x[i] /= complex(float64(n), 0)
		}
	}
}

// Clean processes a whole recording (voicemail, a greeting): noise suppression and level control, with
// the latency removed so it lines up with the original.
func Clean(pcm []int16, rate int) []int16 {
	if len(pcm) == 0 {
		return pcm
	}
	c := NewCleaner(rate)
	// Learn the background first from the opening half second, so the start is cleaned too.
	lead := min(len(pcm), rate/2)
	c.Process(pcm[:lead])
	c.ready = make([]int16, c.hop)
	c.in = c.in[:0]
	c.out = make([]float64, c.n)
	c.agc = 1
	padded := append(append([]int16(nil), pcm...), make([]int16, c.hop)...)
	out := c.Process(padded)
	return out[c.hop:]
}
