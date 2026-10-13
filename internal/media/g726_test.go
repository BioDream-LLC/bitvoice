package media

import (
	"math"
	"testing"
)

// g726Speech is a test signal with the spectrum of a voice more than of a pure tone: three partials
// that glide, under a syllable-like envelope.
func g726Speech(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		t := float64(i) / 8000
		env := 0.5 + 0.5*math.Sin(2*math.Pi*3*t)
		f := 180 + 40*math.Sin(2*math.Pi*0.7*t)
		v := math.Sin(2*math.Pi*f*t) + 0.5*math.Sin(2*math.Pi*2.7*f*t) + 0.25*math.Sin(2*math.Pi*5.3*f*t)
		out[i] = int16(9000 * env * v)
	}
	return out
}

// snr compares two signals, skipping the codec's start-up.
func snr(a, b []int16, skip int) float64 {
	var sig, noise float64
	for i := skip; i < len(a) && i < len(b); i++ {
		sig += float64(a[i]) * float64(a[i])
		d := float64(a[i]) - float64(b[i])
		noise += d * d
	}
	return 10 * math.Log10(sig/max(noise, 1))
}

func TestG726RoundTrip(t *testing.T) {
	in := g726Speech(8000 * 2)
	for _, c := range []struct {
		kbps  int
		bytes int // per 20 ms
		min   float64
	}{{16, 40, 14}, {32, 80, 30}} {
		enc, dec := NewG726Encoder(c.kbps), NewG726Decoder(c.kbps)
		var out []int16
		for i := 0; i+160 <= len(in); i += 160 {
			p := enc.Encode(in[i : i+160])
			if len(p) != c.bytes {
				t.Fatalf("G.726-%d: %d bytes for 20 ms, want %d", c.kbps, len(p), c.bytes)
			}
			out = append(out, dec.Decode(p)...)
		}
		if got := snr(in, out, 800); got < c.min {
			t.Errorf("G.726-%d: signal to noise %.1f dB, want at least %.0f", c.kbps, got, c.min)
		} else {
			t.Logf("G.726-%d: %.1f dB", c.kbps, got)
		}
	}
}

func TestG726Silence(t *testing.T) {
	enc, dec := NewG726Encoder(32), NewG726Decoder(32)
	for range 50 {
		for _, v := range dec.Decode(enc.Encode(make([]int16, 160))) {
			if v > 64 || v < -64 {
				t.Fatalf("silence decodes to %d", v)
			}
		}
	}
}

func TestG726AAL2Order(t *testing.T) {
	in := g726Speech(1600)
	lsb, msb := NewG726Encoder(32), NewG726Encoder(32).MSBFirst()
	a, b := lsb.Encode(in[:160]), msb.Encode(in[:160])
	// The same codewords, nibbles swapped in each octet.
	for i := range a {
		if a[i] != (b[i]>>4 | b[i]<<4) {
			t.Fatalf("octet %d: %02x and %02x are not the same codewords in the other order", i, a[i], b[i])
		}
	}
	dec := NewG726Decoder(32).MSBFirst()
	if got := snr(in[:160], dec.Decode(b), 40); got < 20 {
		t.Fatalf("AAL2 order round trip %.1f dB", got)
	}
}
