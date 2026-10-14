package dahdi

import "math"

// DTMF detection on 8 kHz audio, for analogue phones dialling on a card's ports: the Goertzel
// algorithm over 205-sample blocks at the eight DTMF frequencies, with the usual checks (energy,
// twist, each tone well above its row or column neighbours and the signal's other energy), and a key
// reported once it is heard in two blocks running and released after two blocks without it.

var (
	rowFreqs = [4]float64{697, 770, 852, 941}
	colFreqs = [4]float64{1209, 1336, 1477, 1633}
	keys     = [4][4]byte{{'1', '2', '3', 'A'}, {'4', '5', '6', 'B'}, {'7', '8', '9', 'C'}, {'*', '0', '#', 'D'}}
)

const dtmfBlock = 205

var rowCoef, colCoef = coefs(rowFreqs), coefs(colFreqs)

func coefs(f [4]float64) (c [4]float64) {
	for i, v := range f {
		k := math.Round(dtmfBlock * v / 8000)
		c[i] = 2 * math.Cos(2*math.Pi*k/dtmfBlock)
	}
	return c
}

// Detector finds DTMF keys in a stream of samples.
type Detector struct {
	buf     []float64
	last    byte // the key heard in the last block, 0 for none
	held    byte // the key reported and not yet released
	silence int
}

// Feed takes samples and returns the keys newly pressed in them.
func (d *Detector) Feed(pcm []int16) []byte {
	var out []byte
	for _, s := range pcm {
		d.buf = append(d.buf, float64(s))
		if len(d.buf) < dtmfBlock {
			continue
		}
		k := detectBlock(d.buf)
		d.buf = d.buf[:0]
		switch {
		case k != 0 && k == d.last && d.held != k:
			d.held = k
			out = append(out, k)
			d.silence = 0
		case k == 0:
			d.silence++
			if d.silence >= 2 {
				d.held = 0
			}
		default:
			d.silence = 0
		}
		d.last = k
	}
	return out
}

func goertzel(x []float64, coef float64) float64 {
	var s1, s2 float64
	for _, v := range x {
		s := v + coef*s1 - s2
		s2, s1 = s1, s
	}
	return s1*s1 + s2*s2 - coef*s1*s2
}

func detectBlock(x []float64) byte {
	var rows, cols [4]float64
	for i := range rows {
		rows[i] = goertzel(x, rowCoef[i])
		cols[i] = goertzel(x, colCoef[i])
	}
	r, c := best(rows), best(cols)
	er, ec := rows[r], cols[c]
	var total float64
	for _, v := range x {
		total += v * v
	}
	total *= dtmfBlock / 2 // Goertzel power of a sine of amplitude a is about (N a / 2)^2; its mean square is a^2 / 2
	switch {
	case er < 8e8 || ec < 8e8: // too quiet: under about -40 dBFS per tone
		return 0
	case ec > er*6.3 || er > ec*6.3: // twist over 8 dB
		return 0
	case er+ec < 0.42*total: // much of the signal is something else: speech, music
		return 0
	}
	for i := range rows {
		if i != r && rows[i]*6.3 > er {
			return 0
		}
		if i != c && cols[i]*6.3 > ec {
			return 0
		}
	}
	return keys[r][c]
}

func best(v [4]float64) int {
	b := 0
	for i := range v {
		if v[i] > v[b] {
			b = i
		}
	}
	return b
}
