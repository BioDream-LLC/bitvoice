package media

import (
	"encoding/binary"
	"math"
	"testing"
)

// snrAt finds the codec delay and returns the signal-to-noise ratio in dB at that delay.
func snrAt(in, out []int16) (float64, int) {
	best, lag := -100.0, 0
	for d := 0; d < 64; d++ {
		var sig, noise float64
		for i := 400; i+d < len(out) && i < len(in); i++ {
			s, n := float64(in[i]), float64(out[i+d])-float64(in[i])
			sig += s * s
			noise += n * n
		}
		if v := 10 * math.Log10(sig/(noise+1)); v > best {
			best, lag = v, d
		}
	}
	return best, lag
}

func TestG722RoundTrip(t *testing.T) {
	for _, hz := range []float64{300, 1000, 3000, 6000} {
		in := make([]int16, 16000)
		for i := range in {
			in[i] = int16(8000 * math.Sin(2*math.Pi*hz*float64(i)/16000))
		}
		code := NewG722Encoder().Encode(in)
		if len(code) != len(in)/2 {
			t.Fatalf("%d octets for %d samples", len(code), len(in))
		}
		out := NewG722Decoder().Decode(code)
		snr, lag := snrAt(in, out)
		t.Logf("%v Hz: %.1f dB at delay %d", hz, snr, lag)
		if snr < 20 {
			t.Fatalf("%v Hz: SNR %.1f dB", hz, snr)
		}
	}
}

func TestG722Silence(t *testing.T) {
	out := NewG722Decoder().Decode(NewG722Encoder().Encode(make([]int16, 3200)))
	for i, v := range out {
		if v > 64 || v < -64 {
			t.Fatalf("sample %d is %d in silence", i, v)
		}
	}
}

func TestTranscodeG711ToG722AndBack(t *testing.T) {
	// A 1 kHz tone in PCMU, through G.722 and back to PCMU, stays a 1 kHz tone.
	in := make([]int16, 8000)
	for i := range in {
		in[i] = int16(6000 * math.Sin(2*math.Pi*1000*float64(i)/8000))
	}
	there, back := NewTranscoder(PTPCMU, PTG722), NewTranscoder(PTG722, PTPCMU)
	var out []int16
	for i := 0; i+160 <= len(in); i += 160 {
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, PTPCMU
		binary.BigEndian.PutUint16(pkt[2:], uint16(i/160))
		binary.BigEndian.PutUint32(pkt[4:], uint32(i))
		for _, v := range in[i : i+160] {
			pkt = append(pkt, LinearToUlaw(v))
		}
		w := there.Convert(pkt)
		if w[1]&0x7f != PTG722 || len(w) != 172 {
			t.Fatalf("G.722 packet: pt %d, %d bytes", w[1]&0x7f, len(w))
		}
		n := back.Convert(w)
		if n[1]&0x7f != PTPCMU || len(n) != 172 {
			t.Fatalf("PCMU packet: pt %d, %d bytes", n[1]&0x7f, len(n))
		}
		for _, b := range n[12:] {
			out = append(out, UlawToLinear(b))
		}
	}
	snr, lag := snrAt(in, out)
	t.Logf("%.1f dB at delay %d", snr, lag)
	if snr < 15 {
		t.Fatalf("SNR %.1f dB", snr)
	}
}
