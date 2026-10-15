// Package mf makes and hears multifrequency signalling: two tones out of six, as MF R1 (North American
// trunks, Bellcore GR-506: digits, KP and ST) and MFC-R2 (ITU-T Q.441: fifteen forward signals from
// 1380 to 1980 Hz, fifteen backward from 1140 down to 540 Hz) use. Linear 16-bit samples at 8 kHz.
package mf

import (
	"math"
)

// Rate is the sample rate.
const Rate = 8000

// A Set is one family of six frequencies; signal n (1 to 15) is the pair Q.441 and GR-506 give:
// 1 = f0+f1, 2 = f0+f2, 3 = f1+f2, 4 = f0+f3, 5 = f1+f3, 6 = f2+f3, 7 = f0+f4, 8 = f1+f4, 9 = f2+f4,
// 10 = f3+f4, 11 = f0+f5, 12 = f1+f5, 13 = f2+f5, 14 = f3+f5, 15 = f4+f5.
type Set [6]float64

var (
	R1       = Set{700, 900, 1100, 1300, 1500, 1700}
	Forward  = Set{1380, 1500, 1620, 1740, 1860, 1980} // MFC-R2 forward (from the caller's end)
	Backward = Set{1140, 1020, 900, 780, 660, 540}     // MFC-R2 backward
)

var pairs = [16][2]int{{}, {0, 1}, {0, 2}, {1, 2}, {0, 3}, {1, 3}, {2, 3}, {0, 4}, {1, 4}, {2, 4}, {3, 4}, {0, 5}, {1, 5}, {2, 5}, {3, 5}, {4, 5}}

// Signal of a pair, 0 when it is not one.
func signalOf(a, b int) int {
	if a > b {
		a, b = b, a
	}
	for n := 1; n < 16; n++ {
		if pairs[n][0] == a && pairs[n][1] == b {
			return n
		}
	}
	return 0
}

// MF R1 signals as characters: digits, K for KP, S for ST (and ST' ST” ST”' as A, B, C).
var r1Char = [16]byte{0, '1', '2', '3', '4', '5', '6', '7', '8', '9', '0', 'C', 'K', 'B', 'A', 'S'}

// R1Signal is the signal number for an MF R1 character (0 for none).
func R1Signal(c byte) int {
	for n, x := range r1Char {
		if x == c && n > 0 {
			return n
		}
	}
	if c == '*' {
		return 12 // KP, as Asterisk writes Feature Group D
	}
	if c == '#' {
		return 15 // ST
	}
	return 0
}

// R1Char is the character of an MF R1 signal.
func R1Char(n int) byte {
	if n < 1 || n > 15 {
		return 0
	}
	return r1Char[n]
}

// Tone makes a signal for ms milliseconds at level dBm0 per frequency (MF R1: -7; R2: -8 to -11).
func (s Set) Tone(n, ms int, level float64) []int16 {
	out := make([]int16, ms*Rate/1000)
	if n < 1 || n > 15 {
		return out
	}
	// 0 dBm0 is a sine of 3.17 dB below full scale in G.711 (peak 22786 of 32767 for µ-law's 8159
	// scaled to 16 bits): amplitude = 32767 * 10^((level - 3.17) / 20).
	a := 32767 * math.Pow(10, (level-3.17)/20)
	f1, f2 := s[pairs[n][0]], s[pairs[n][1]]
	for i := range out {
		t := float64(i) / Rate
		out[i] = int16(a * (math.Sin(2*math.Pi*f1*t) + math.Sin(2*math.Pi*f2*t)))
	}
	return out
}

// R1Digits is an MF R1 string as sound: KP 100 ms, other signals 68 ms, 68 ms of silence after each.
func R1Digits(s string) []int16 {
	var out []int16
	for i := 0; i < len(s); i++ {
		n := R1Signal(s[i])
		if n == 0 {
			continue
		}
		ms := 68
		if n == 12 {
			ms = 100
		}
		out = append(out, R1.Tone(n, ms, -7)...)
		out = append(out, make([]int16, 68*Rate/1000)...)
	}
	return out
}

// Detector hears a Set's signals, block by block. Feed it audio; it calls back on each change: a
// signal starting (n) or ending (0), after it has held for MinBlocks blocks.
type Detector struct {
	set       Set
	n         int // samples a block: the six frequencies fall on (or next to) its bins
	coef      [6]float64
	buf       []float64
	cur       int // the signal now heard (0: none)
	cand      int
	candCount int
	// MinBlocks is how many 10 ms blocks a signal (or the silence after it) must last: 3 for MF R1
	// (30 ms; tones last 68), 2 for compelled R2.
	MinBlocks int
	// OffBlocks is how many blocks of no signal end one (0: MinBlocks). R2 uses 3 (40 ms), so a
	// frame lost on the line does not split one signal into two.
	OffBlocks int
}

// NewDetector hears one set: MF R1 in 10 ms blocks (its tones are 200 Hz apart, on 100 Hz bins), R2
// in 133-sample blocks (60 Hz bins; its tones are 120 Hz apart, on multiples of 60 Hz).
func NewDetector(s Set) *Detector {
	d := &Detector{set: s, MinBlocks: 3, n: 80}
	if s != R1 {
		d.n, d.MinBlocks, d.OffBlocks = 133, 2, 3
	}
	for i, f := range s {
		d.coef[i] = 2 * math.Cos(2*math.Pi*f/Rate)
	}
	return d
}

// Signal is the signal heard now (0 for none).
func (d *Detector) Signal() int { return d.cur }

// Feed takes samples and returns the changes in what is heard: n when signal n starts, 0 when it ends.
func (d *Detector) Feed(pcm []int16) []int {
	var out []int
	for _, v := range pcm {
		d.buf = append(d.buf, float64(v))
		if len(d.buf) < d.n {
			continue
		}
		n := d.blockSignal(d.buf)
		d.buf = d.buf[:0]
		if n == d.cand {
			d.candCount++
		} else {
			d.cand, d.candCount = n, 1
		}
		need := d.MinBlocks
		if d.cand == 0 && d.OffBlocks > 0 {
			need = d.OffBlocks
		}
		if d.candCount == need && d.cand != d.cur {
			d.cur = d.cand
			out = append(out, d.cur)
		}
	}
	return out
}

// blockSignal finds the signal in one block: the two strongest of the six well above the rest,
// within 6 dB of each other, and loud enough.
func (d *Detector) blockSignal(x []float64) int {
	var e float64
	for _, v := range x {
		e += v * v
	}
	var p [6]float64
	for i, c := range d.coef {
		p[i] = goertzel(x, c)
	}
	a, b := -1, -1
	for i := range p {
		switch {
		case a < 0 || p[i] > p[a]:
			a, b = i, a
		case b < 0 || p[i] > p[b]:
			b = i
		}
	}
	if a < 0 || b < 0 {
		return 0
	}
	// Loudness: each tone at least about -30 dBm0 (amplitude ~ 800).
	if p[b] < 2.0e8 {
		return 0
	}
	if p[a] > 4*p[b] { // twist beyond 6 dB
		return 0
	}
	for i := range p {
		if i != a && i != b && p[i]*10 > p[b] { // a third tone within 10 dB: not a clean signal
			return 0
		}
	}
	// Most of the block's energy must be in the two tones (speech is not MF).
	if (p[a]+p[b])*2/float64(len(x)) < 0.5*e {
		return 0
	}
	return signalOf(a, b)
}

func goertzel(x []float64, coef float64) float64 {
	var s1, s2 float64
	for _, v := range x {
		s := v + coef*s1 - s2
		s2, s1 = s1, s
	}
	return s1*s1 + s2*s2 - coef*s1*s2
}

// R1Receiver turns MF R1 signals into characters: one per tone, on its start.
type R1Receiver struct{ d *Detector }

func NewR1Receiver() *R1Receiver { return &R1Receiver{d: NewDetector(R1)} }

// Feed returns the characters that started in the audio.
func (r *R1Receiver) Feed(pcm []int16) string {
	var s []byte
	for _, n := range r.d.Feed(pcm) {
		if n != 0 {
			s = append(s, R1Char(n))
		}
	}
	return string(s)
}

// Generator makes a set's signals continuously, block by block, keeping the phase across blocks.
type Generator struct {
	set   Set
	level float64
	n     int
	t     int // samples since the signal began
}

// NewGenerator makes signals of a set at a level in dBm0 per frequency.
func NewGenerator(s Set, level float64) *Generator { return &Generator{set: s, level: level} }

// Set chooses the signal (0 for silence).
func (g *Generator) Set(n int) {
	if n != g.n {
		g.n, g.t = n, 0
	}
}

// Signal is the signal being made.
func (g *Generator) Signal() int { return g.n }

// Fill writes the next len(out) samples.
func (g *Generator) Fill(out []int16) {
	if g.n < 1 || g.n > 15 {
		clear(out)
		return
	}
	a := 32767 * math.Pow(10, (g.level-3.17)/20)
	f1, f2 := g.set[pairs[g.n][0]], g.set[pairs[g.n][1]]
	for i := range out {
		t := float64(g.t+i) / Rate
		out[i] = int16(a * (math.Sin(2*math.Pi*f1*t) + math.Sin(2*math.Pi*f2*t)))
	}
	g.t += len(out)
}
