package imbe

import (
	"math"
	"testing"
)

// voice is a voiced signal with a moving pitch and loudness, with pauses, like speech.
func voice(n int, f0 float64) []int16 {
	out := make([]int16, n)
	ph := 0.0
	for i := range out {
		t := float64(i) / 8000
		f := f0 * (1 + 0.15*math.Sin(2*math.Pi*1.5*t))
		ph += 2 * math.Pi * f / 8000
		env := math.Max(0, math.Sin(2*math.Pi*1.2*t)) // on and off
		v := 0.0
		for h := 1; h <= 12; h++ {
			// a formant-ish slope: loud low harmonics, a bump near 700 Hz
			a := 1 / float64(h)
			if fh := f * float64(h); fh > 500 && fh < 900 {
				a *= 2.5
			}
			v += a * math.Sin(float64(h)*ph)
		}
		out[i] = int16(4000 * env * v)
	}
	return out
}

// periodOf is the strongest autocorrelation period (samples) of a stretch of audio.
func periodOf(x []float64) float64 {
	best, bestR := 0, -1.0
	for p := 20; p <= 130; p++ {
		var num, e0, e1 float64
		for n := 0; n+p < len(x); n++ {
			num += x[n] * x[n+p]
			e0 += x[n] * x[n]
			e1 += x[n+p] * x[n+p]
		}
		if r := num / math.Sqrt(e0*e1+1e-9); r > bestR {
			best, bestR = p, r
		}
	}
	return float64(best)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, f0 := range []float64{110, 210} { // a low and a high voice
		in := voice(8000*3, f0)
		enc, dec := NewEncoder(), New()
		var out []int16
		for i := 0; i+160 <= len(in); i += 160 {
			frame := enc.Encode(in[i : i+160])
			pcm, err := dec.Decode(frame)
			if err != nil {
				t.Fatalf("frame %d refused: %v", i/160, err)
			}
			out = append(out, pcm...)
		}
		// Loudness contour: 20 ms frames, input against output, aligned by the best lag.
		frames := func(x []int16) []float64 {
			var e []float64
			for i := 0; i+160 <= len(x); i += 160 {
				s := 0.0
				for _, v := range x[i : i+160] {
					s += float64(v) * float64(v)
				}
				e = append(e, math.Log10(s+1))
			}
			return e
		}
		a, b := frames(in), frames(out)
		bestC, bestLag := -1.0, 0
		for lag := 0; lag <= 4; lag++ {
			var sab, saa, sbb, ma, mb float64
			n := len(a) - lag
			for i := 0; i < n; i++ {
				ma += a[i]
				mb += b[i+lag]
			}
			ma, mb = ma/float64(n), mb/float64(n)
			for i := 0; i < n; i++ {
				sab += (a[i] - ma) * (b[i+lag] - mb)
				saa += (a[i] - ma) * (a[i] - ma)
				sbb += (b[i+lag] - mb) * (b[i+lag] - mb)
			}
			if c := sab / math.Sqrt(saa*sbb); c > bestC {
				bestC, bestLag = c, lag
			}
		}
		// Pitch of a loud stretch, input against output.
		loudAt := 0
		for i, v := range a {
			if v > a[loudAt] {
				loudAt = i
			}
		}
		seg := func(x []int16, from int) []float64 {
			var s []float64
			for _, v := range x[from : from+480] {
				s = append(s, float64(v))
			}
			return s
		}
		pin := periodOf(seg(in, loudAt*160))
		pout := periodOf(seg(out, (loudAt+bestLag)*160))
		t.Logf("f0 %.0f Hz: loudness correlation %.2f at %d frames delay; period in %.0f, out %.0f samples", f0, bestC, bestLag, pin, pout)
		if bestC < 0.85 {
			t.Errorf("f0 %.0f: the loudness contour is lost (%.2f)", f0, bestC)
		}
		if math.Abs(pin-pout) > 0.08*pin {
			t.Errorf("f0 %.0f: the pitch changed: %.0f -> %.0f samples", f0, pin, pout)
		}
	}
}

// The fundamental survives exactly as quantised: decoding b0 gives the ω0 the encoder chose.
func TestEncodeFundamental(t *testing.T) {
	enc := NewEncoder()
	in := voice(8000, 150)
	var frame []byte
	for i := 0; i+160 <= 2400; i += 160 { // 0.3 s, in the voiced part
		frame = enc.Encode(in[i : i+160])
	}
	info := unpackInfoBits(frame)
	h, err := UnpackHeader(info)
	if err != nil || h.Silent {
		t.Fatalf("%v %+v", err, h)
	}
	if f := h.W0 * 8000 / (2 * math.Pi); f < 120 || f > 185 {
		t.Fatalf("fundamental %.0f Hz for a 150 Hz voice (±15%%)", f)
	}
}

// One iteration is 20 ms of speech encoded and decoded.
func BenchmarkEncodeDecode(b *testing.B) {
	in := voice(8000, 150)
	enc, dec := NewEncoder(), New()
	for i := 0; i < b.N; i++ {
		k := i % 50 * 160
		_, _ = dec.Decode(enc.Encode(in[k : k+160]))
	}
}

// A steady tone (an alert tone) keeps its level through the codec instead of collapsing after
// the first frame.
func TestEncodeSteadyTone(t *testing.T) {
	enc, dec := NewEncoder(), New()
	var levels []float64
	for i := 0; i < 50; i++ {
		pcm := make([]int16, 160)
		for k := range pcm {
			pcm[k] = int16(0.45 * 32767 * math.Sin(2*math.Pi*1000*float64(i*160+k)/8000))
		}
		out, err := dec.Decode(enc.Encode(pcm))
		if err != nil {
			t.Fatal(err)
		}
		var e float64
		for _, s := range out {
			e += float64(s) * float64(s)
		}
		levels = append(levels, math.Sqrt(e/160))
	}
	for i, l := range levels[8:] {
		if l < 4000 {
			t.Fatalf("frame %d of a steady 1000 Hz tone decodes at %.0f rms: %v", i+8, l, levels)
		}
	}
}
