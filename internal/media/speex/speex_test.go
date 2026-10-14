package speex

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

// The packets and the decoded sound, with frames 5, 6 and 40 lost and concealed, hash to what
// libspeex 1.2.1 (built for floating point without contraction, the enhancer on) gives for the same
// input at these qualities and complexities. Every quality 0 to 10 and complexities 1 to 10 were
// also compared frame by frame with it, on this input and on one of noise, silence and a sweep.
func TestBitExactWithLibspeex(t *testing.T) {
	in := speech(8000 * 3)
	var raw []byte
	for _, v := range in {
		raw = binary.LittleEndian.AppendUint16(raw, uint16(v))
	}
	if h := sha256.Sum256(raw); hex.EncodeToString(h[:]) != "eb006910de0cb116f2b9ba97fd2dd9155af3e0e84b611d0824c153259906d991" {
		t.Fatal("test input changed")
	}
	for _, c := range []struct {
		q, cplx   int
		bits, pcm string
	}{
		{8, 2, "3d1e286d5900e5efbcc8ea77b7d01e3dcd689d45d99e2d3c698aebcc2eab7b3a", "46e977703da547512fb6a557ac497bc23d4473106ea15e896332cece1d0cba9f"},
		{3, 2, "bd8ef7ddd66e825b3e268bd2c8b6b70ad9fb7ed2905a296c795481051d080a18", "f0796e2d6ec9693857b592e237f84a89598804c0698ec4b4af018d4f0cc8e93e"},
		{1, 2, "30e21daa79dca89e40c9554e7a690b0462dfc5328e0723c71b6d43353241343e", "b23c707da779501d772c8ca4dd87552f27bffdcaca4f2809b873088861103b21"},
		{10, 2, "fcf9fc287996514cf4a5f7790388b078ff14576254e5e747e64aca789a0cfe55", "5a3a97d0c35398ce618ffef52a2369c95cd04fa6ed02d17f5527ca1846f684ff"},
		{8, 5, "9854cd4934f71e4e0902faaeee25c1310f338ae8ba90e5889ebb2484ce25701e", "d02493454d67094eb4f247dfe4e7c178225b3c50971a8725bd3ed50d710be0bb"},
	} {
		enc, dec := NewEncoder(c.q, c.cplx), NewDecoder()
		var bits, pcm []byte
		for n := 0; (n+1)*160 <= len(in); n++ {
			frame := enc.Encode(in[n*160 : (n+1)*160])
			bits = append(bits, frame...)
			var out []int16
			if n == 5 || n == 6 || n == 40 {
				out = dec.Conceal()
			} else {
				var err error
				if out, err = dec.Decode(frame); err != nil || len(out) != 160 {
					t.Fatalf("q%d frame %d: %d samples, %v", c.q, n, len(out), err)
				}
			}
			for _, v := range out {
				pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
			}
		}
		if h := sha256.Sum256(bits); hex.EncodeToString(h[:]) != c.bits {
			t.Errorf("quality %d complexity %d: packets differ from libspeex", c.q, c.cplx)
		}
		if h := sha256.Sum256(pcm); hex.EncodeToString(h[:]) != c.pcm {
			t.Errorf("quality %d complexity %d: decoded sound differs from libspeex", c.q, c.cplx)
		}
	}
}

func TestBitrates(t *testing.T) {
	if Bitrate(8) != 15000 || Bitrate(0) != 2150 || Bitrate(10) != 24600 {
		t.Fatal(Bitrate(8), Bitrate(0), Bitrate(10))
	}
	enc := NewEncoder(8, 2)
	if n := len(enc.Encode(make([]int16, 160))); n != 38 {
		t.Fatalf("a quality-8 frame is %d bytes, want 38", n)
	}
}

// Two frames in one packet decode to 320 samples; rubbish is refused, not a panic.
func TestPacketsAndRubbish(t *testing.T) {
	enc, dec := NewEncoder(8, 2), NewDecoder()
	in := speech(320)
	w := &bitWriter{}
	for _, f := range [][]int16{in[:160], in[160:]} {
		b := enc.Encode(f)
		for _, x := range b[:len(b)-1] {
			w.pack(uint32(x), 8)
		}
		// a quality-8 frame is 300 bits: 37 whole bytes and 4 bits of the last
		w.pack(uint32(b[len(b)-1]>>4), 4)
	}
	out, err := dec.Decode(w.bytes())
	if err != nil || len(out) != 320 {
		t.Fatalf("%d samples, %v", len(out), err)
	}
	for _, junk := range [][]byte{nil, {0xff}, {0x7f, 0xff, 0x00}, make([]byte, 3)} {
		NewDecoder().Decode(junk)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(NewEncoder(8, 2).Encode(speech(160)))
	f.Fuzz(func(t *testing.T, b []byte) {
		NewDecoder().Decode(b)
	})
}
