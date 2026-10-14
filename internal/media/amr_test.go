package media

import (
	"encoding/binary"
	"math"
	"testing"
)

// pcmuPackets turns 8 kHz audio into 20 ms PCMU RTP packets.
func pcmuPackets(pcm []int16) [][]byte {
	var out [][]byte
	for i, seq := 0, 0; i+160 <= len(pcm); i, seq = i+160, seq+1 {
		p := make([]byte, 12, 172)
		p[0], p[1] = 0x80, PTPCMU
		binary.BigEndian.PutUint16(p[2:], uint16(seq))
		binary.BigEndian.PutUint32(p[4:], uint32(i))
		for _, v := range pcm[i : i+160] {
			p = append(p, LinearToUlaw(v))
		}
		out = append(out, p)
	}
	return out
}

// Through AMR and back: speech survives (a speech codec scores lower on SNR than a waveform one, so
// the check is that the level and the tone's frequency come through).
func TestAMRTranscoding(t *testing.T) {
	tone := make([]int16, 8000)
	for i := range tone {
		tone[i] = int16(6000 * math.Sin(2*math.Pi*440*float64(i)/8000))
	}
	for _, c := range []struct {
		pt    int
		bytes int // payload of one 20 ms frame
	}{{PTAMR, 33}, {PTAMRBE, 32}, {PTAMRWB, 62}, {PTAMRWBBE, 61}} {
		there, back := NewTranscoder(PTPCMU, c.pt), NewTranscoder(c.pt, PTPCMU)
		var out []int16
		for _, p := range pcmuPackets(tone) {
			a := there.Convert(p)
			if a == nil {
				continue
			}
			if int(a[1]&0x7f) != c.pt || len(a)-12 != c.bytes {
				t.Fatalf("pt %d: packet type %d, %d bytes", c.pt, a[1]&0x7f, len(a)-12)
			}
			if b := back.Convert(a); b != nil {
				for _, u := range b[12:] {
					out = append(out, UlawToLinear(u))
				}
			}
		}
		if len(out) < 6000 {
			t.Fatalf("pt %d: only %d samples back", c.pt, len(out))
		}
		mid := out[2000:6000]
		if p440, p1000 := goertzel(mid, 440, 8000), goertzel(mid, 1000, 8000); p440 < 1e5 || p440 < 20*p1000 {
			t.Fatalf("pt %d: the 440 Hz tone did not come through (%.0f, %.0f)", c.pt, p440, p1000)
		}
	}
}

func TestAMRFmtp(t *testing.T) {
	if CodecPTFmtp("AMR", "octet-align=1; mode-set=0,2,4,7") != PTAMR || CodecPTFmtp("amr", "") != PTAMRBE ||
		CodecPTFmtp("AMR-WB", "octet-align=1") != PTAMRWB || CodecPTFmtp("AMR-WB", "mode-set=2") != PTAMRWBBE {
		t.Fatal("framing from fmtp")
	}
	if modeSet("octet-align=1;mode-set=0,2,4", 7, 7) != 4 || modeSet("", 7, 7) != 7 || modeSet("mode-set=9", 8, 8) != 8 {
		t.Fatal("mode-set")
	}
	// A mode-set limits the encoder: mode 2 (5.90 kbit/s) frames are smaller than 12.2's.
	x := NewTranscoderFmtp(PTPCMU, PTAMR, "octet-align=1;mode-set=2")
	for _, p := range pcmuPackets(make([]int16, 1600)) {
		if a := x.Convert(p); a != nil && len(a)-12 != 17 {
			t.Fatalf("mode 2 payload %d bytes", len(a)-12)
		}
	}
}

func TestILBCTranscoding(t *testing.T) {
	if CodecPTFmtp("iLBC", "mode=20") != PTILBC20 || CodecPTFmtp("ILBC", "") != PTILBC30 || CodecPTFmtp("ilbc", "mode=30") != PTILBC30 {
		t.Fatal("iLBC mode from fmtp")
	}
	tone := make([]int16, 8000)
	for i := range tone {
		tone[i] = int16(6000 * math.Sin(2*math.Pi*440*float64(i)/8000))
	}
	for _, c := range []struct{ pt, bytes int }{{PTILBC20, 38}, {PTILBC30, 50}} {
		there, back := NewTranscoder(PTPCMU, c.pt), NewTranscoder(c.pt, PTPCMU)
		var out []int16
		for _, p := range pcmuPackets(tone) {
			a := there.Convert(p)
			if a == nil {
				continue
			}
			if int(a[1]&0x7f) != c.pt || len(a)-12 != c.bytes {
				t.Fatalf("pt %d: type %d, %d bytes", c.pt, a[1]&0x7f, len(a)-12)
			}
			if b := back.Convert(a); b != nil {
				for _, u := range b[12:] {
					out = append(out, UlawToLinear(u))
				}
			}
		}
		mid := out[2000:6000]
		if p440, p1000 := goertzel(mid, 440, 8000), goertzel(mid, 1000, 8000); p440 < 1e5 || p440 < 20*p1000 {
			t.Fatalf("pt %d: tone lost (%.0f %.0f)", c.pt, p440, p1000)
		}
	}
	// A peer that sends 30 ms frames to a 20 ms decoder is followed.
	v := newILBC(20)
	if pcm := v.decode(make([]byte, 50)); len(pcm) != 240 || v.mode != 30 {
		t.Fatalf("mode switch: %d samples, mode %d", len(pcm), v.mode)
	}
}
