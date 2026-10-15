// Package tty speaks the TTY (TDD) text telephones deaf callers use on voice lines: 5-bit Baudot at
// 45.45 bit/s, frequency shift keyed with mark at 1400 Hz and space at 1800 Hz (TIA-825-A, ITU-T V.18's
// "Weitbrecht" mode). It decodes what a TTY sends on a call's audio and makes the audio that sends text
// back. Audio is 8 kHz linear PCM.
package tty

import (
	"math"
	"strings"
)

const (
	rate       = 8000
	baud       = 45.45
	samplesBit = rate / baud // 176.02 samples a bit
	markHz     = 1400
	spaceHz    = 1800
)

// Codes with a meaning in both cases.
const (
	codeNull  = 0x00
	codeLF    = 0x02
	codeSpace = 0x04
	codeCR    = 0x08
	codeFigs  = 0x1B
	codeLtrs  = 0x1F
)

// letters and figures are the US TTY character sets (TIA-825-A, as spandsp's V.18 has them), indexed by
// the 5-bit code. Code 0 is a backspace on TTYs that send one; the figures shift of S rings a bell.
var letters = [32]byte{
	0, 'E', '\n', 'A', ' ', 'S', 'I', 'U', '\r', 'D', 'R', 'J', 'N', 'F', 'C', 'K',
	'T', 'Z', 'L', 'W', 'H', 'Y', 'P', 'Q', 'O', 'B', 'G', 0, 'M', 'X', 'V', 0,
}

var figures = [32]byte{
	0, '3', '\n', '-', ' ', '\a', '8', '7', '\r', '$', '4', '\'', ',', '!', ':', '(',
	'5', '"', ')', '2', '=', '6', '0', '1', '9', '?', '+', 0, '.', '/', ';', 0,
}

// codeOf finds a character's code and whether it needs the figures shift; ok is false for a character a
// TTY cannot send.
func codeOf(ch byte) (code byte, figs, both, ok bool) {
	switch ch {
	case ' ':
		return codeSpace, false, true, true
	case '\n':
		return codeLF, false, true, true
	case '\r':
		return codeCR, false, true, true
	}
	for i, c := range letters {
		if c == ch && c != 0 {
			return byte(i), false, false, true
		}
	}
	for i, c := range figures {
		if c == ch && c != 0 {
			return byte(i), true, false, true
		}
	}
	return 0, false, false, false
}

// Sendable reports whether text can be sent to a TTY as it is (TTYs have capitals, digits and some
// punctuation only).
func Sendable(text string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(text) {
		if r < 128 {
			if _, _, _, ok := codeOf(byte(r)); ok && r != '\a' {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// Level is the transmit amplitude, about -13 dBm0.
const Level = 5000

// Encode makes the audio that sends text to a TTY: 150 ms of mark tone, the shift the first character
// needs, the characters (one start bit, five data bits, one and a half stop bits), and 300 ms of mark
// held after, as TTYs expect. Characters a TTY lacks are left out; text is sent in capitals.
func Encode(text string) []int16 {
	var e encoder
	e.tone(markHz, 0.150*rate)
	figs := false
	first, unsure := true, false
	for _, r := range strings.ToUpper(text) {
		if r >= 128 {
			continue
		}
		code, f, both, ok := codeOf(byte(r))
		if !ok {
			continue
		}
		if !both && (first || unsure || f != figs) {
			if f {
				e.char(codeFigs)
			} else {
				e.char(codeLtrs)
			}
			figs, unsure = f, false
		}
		if first && both {
			e.char(codeLtrs) // set a known case first
			figs = false
		}
		first = false
		e.char(code)
		if code == codeSpace {
			unsure = true // some TTYs return to letters after a space, some do not: say the case again
		}
	}
	e.tone(markHz, 0.300*rate)
	return e.out
}

type encoder struct {
	out   []int16
	phase float64
	frac  float64 // the fraction of a sample the bit timing has drifted
}

func (e *encoder) tone(hz float64, n float64) {
	e.frac += n
	k := int(e.frac)
	e.frac -= float64(k)
	step := 2 * math.Pi * hz / rate
	for range k {
		e.out = append(e.out, int16(Level*math.Sin(e.phase)))
		e.phase = math.Mod(e.phase+step, 2*math.Pi)
	}
}

func (e *encoder) char(code byte) {
	e.tone(spaceHz, samplesBit)
	for i := range 5 {
		if code>>i&1 == 1 {
			e.tone(markHz, samplesBit)
		} else {
			e.tone(spaceHz, samplesBit)
		}
	}
	e.tone(markHz, 1.5*samplesBit)
}

// window is the length of the tone filters: 10 ms, under half a bit.
const window = 80

// Decoder turns a call's audio into the text a TTY sent. It ignores speech, music and other tones: a
// character counts only when every bit of it is a clean mark or space tone, and text is passed on once
// two characters in a row have been heard.
type Decoder struct {
	n       int // samples seen
	ring    [window]float64
	cm, sm  [window][2]float64 // each sample's mark and space products, kept to remove later
	mark    [2]float64
	space   [2]float64
	energy  float64
	pm, ps  float64 // oscillator phases
	tones   []tone  // the decision for each sample still needed
	state   int     // 0 idle, 1 in a character
	at      float64 // in a character: sample index of the next bit's middle
	bit     int
	code    byte
	clean   bool
	figs    bool
	heard   bool
	pending []byte // characters held until TTY is certain
	lastAt  int    // when the last character ended
	prev    tone   // the last clean tone heard
}

type tone byte

const (
	none tone = iota
	markTone
	spaceTone
)

// NewDecoder starts a decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Heard reports whether a TTY has been heard on the call.
func (d *Decoder) Heard() bool { return d.heard }

// Feed takes the next audio and returns the text decoded from it (possibly empty).
func (d *Decoder) Feed(pcm []int16) string {
	var out []byte
	stepM, stepS := 2*math.Pi*markHz/rate, 2*math.Pi*spaceHz/rate
	for _, v := range pcm {
		x := float64(v)
		i := d.n % window
		// slide the filters: add this sample's products, remove those of the sample a window ago
		cm := [2]float64{x * math.Cos(d.pm), x * math.Sin(d.pm)}
		sm := [2]float64{x * math.Cos(d.ps), x * math.Sin(d.ps)}
		for k := range 2 {
			d.mark[k] += cm[k] - d.cm[i][k]
			d.space[k] += sm[k] - d.sm[i][k]
		}
		d.energy += x*x - d.ring[i]*d.ring[i]
		d.ring[i], d.cm[i], d.sm[i] = x, cm, sm
		d.pm = math.Mod(d.pm+stepM, 2*math.Pi)
		d.ps = math.Mod(d.ps+stepS, 2*math.Pi)
		d.n++
		if d.n < window {
			continue
		}
		if c, ok := d.step(d.decide()); ok {
			out = append(out, c...)
		}
	}
	return string(out)
}

// decide judges the last window: mark, space, or neither (too quiet, or not a clean tone).
func (d *Decoder) decide() tone {
	em := d.mark[0]*d.mark[0] + d.mark[1]*d.mark[1]
	es := d.space[0]*d.space[0] + d.space[1]*d.space[1]
	// a pure tone of amplitude A gives (A·N/2)² from its filter and A²·N/2 of energy
	norm := 2 / float64(window) / max(d.energy, 1)
	if d.energy/window < 300*300 { // quieter than about -40 dBm0
		return none
	}
	switch {
	case em > es && em*norm > 0.6:
		return markTone
	case es > em && es*norm > 0.6:
		return spaceTone
	}
	return none
}

// step runs the character framing on one sample's decision (made over the window that ends at it, so
// centred half a window earlier; the delay is the same for every sample and so does not matter).
func (d *Decoder) step(t tone) ([]byte, bool) {
	now := d.n
	prev := d.prev // the last clean tone before this sample
	if t != none {
		d.prev = t
	}
	switch d.state {
	case 0:
		if t == spaceTone && prev == markTone { // between two tones the filters may see neither for a moment
			d.state, d.bit, d.code, d.clean = 1, 0, 0, true
			d.at = float64(now) + samplesBit/2 // the start bit's middle
		}
		return nil, false
	}
	if float64(now) < d.at { // bits are judged at their middles only: tone changes blur their edges
		return nil, false
	}
	switch {
	case d.bit == 0: // the start bit, still space in its middle
		if t != spaceTone {
			d.state = 0
			return nil, false
		}
	case d.bit <= 5:
		if t == markTone {
			d.code |= 1 << (d.bit - 1)
		} else if t != spaceTone {
			d.clean = false
		}
	default: // the stop bit
		d.state = 0
		if t != markTone || !d.clean {
			return nil, false
		}
		return d.char(d.code, now)
	}
	d.bit++
	d.at += samplesBit
	return nil, false
}

// char interprets one received code.
func (d *Decoder) char(code byte, now int) ([]byte, bool) {
	gap := now - d.lastAt
	d.lastAt = now
	if !d.heard && gap > 2*rate {
		d.pending = d.pending[:0] // too far apart to count together
	}
	var c byte
	switch code {
	case codeFigs:
		d.figs = true
	case codeLtrs:
		d.figs = false
	case codeNull:
	default:
		if d.figs {
			c = figures[code]
		} else {
			c = letters[code]
		}
	}
	if !d.heard {
		d.pending = append(d.pending, code)
		if len(d.pending) < 2 {
			return nil, false
		}
		d.heard = true
		// replay what was held, with the shifts it carried
		held := d.pending
		d.pending = nil
		var out []byte
		figs := false
		for _, k := range held {
			switch k {
			case codeFigs:
				figs = true
			case codeLtrs:
				figs = false
			case codeNull:
			default:
				ch := letters[k]
				if figs {
					ch = figures[k]
				}
				if ch != 0 && ch != '\a' && ch != '\r' {
					out = append(out, ch)
				}
			}
		}
		return out, len(out) > 0
	}
	if code == codeNull {
		return []byte{'\b'}, true // a backspace: the caller corrected a typing error
	}
	if c == 0 || c == '\a' || c == '\r' {
		return nil, false
	}
	return []byte{c}, true
}
