package ilbc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

func speech(n int) []int16 {
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

// The encoded bytes and the decoded sound, with frames 5, 6 and 40 lost and concealed, hash to what the
// RFC 3951 reference program (its Appendix A, compiled without floating-point contraction) writes for
// the same input: this translation is bit-exact with it in both modes, the enhancer and the concealment
// included.
func TestBitExactWithRFC3951(t *testing.T) {
	want := map[int][2]string{
		20: {"1532507b410e2a615fb28ac9ba1e24f8d858e3577aa23279051a8d68af662ad5", "65bac5cb7cdf6f32340ac29f821b3a242e2c616d585868835cb6efda73b2185a"},
		30: {"47f5c31d31328704e3216eff211c70d0eb9fea70d542c129be0547b4f0485680", "5e7e259b25957171fe32f743c5f3778b2396f0dfc10fdb9b75ea7f1063bc1a11"},
	}
	in := speech(8000 * 3)
	lost := map[int]bool{5: true, 6: true, 40: true}
	for mode, w := range want {
		enc, _ := NewEncoder(mode)
		dec, _ := NewDecoder(mode, true)
		fs, fb := enc.FrameSamples(), enc.FrameBytes()
		var bits, pcm []byte
		frame := make([]byte, fb)
		out := make([]int16, fs)
		for i, n := 0, 0; i+fs <= len(in); i, n = i+fs, n+1 {
			enc.Encode(frame, in[i:i+fs])
			bits = append(bits, frame...)
			if lost[n] {
				dec.Decode(out, nil)
			} else {
				dec.Decode(out, frame)
			}
			for _, v := range out {
				pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
			}
		}
		if h := sha256.Sum256(bits); hex.EncodeToString(h[:]) != w[0] {
			t.Errorf("%d ms: encoded bytes differ from the reference", mode)
		}
		if h := sha256.Sum256(pcm); hex.EncodeToString(h[:]) != w[1] {
			t.Errorf("%d ms: decoded sound differs from the reference", mode)
		}
	}
}

func TestModes(t *testing.T) {
	if _, err := NewEncoder(25); err == nil {
		t.Fatal("mode 25 accepted")
	}
	e, _ := NewEncoder(30)
	if e.FrameSamples() != 240 || e.FrameBytes() != 50 {
		t.Fatal("30 ms sizes")
	}
}
