package media

import "testing"

func TestGSMRoundTrip(t *testing.T) {
	in := g726Speech(8000 * 2)
	enc, dec := NewGSMEncoder(), NewGSMDecoder()
	var out []int16
	for i := 0; i+160 <= len(in); i += 160 {
		p := enc.Encode(in[i : i+160])
		if len(p) != 33 || p[0]>>4 != 0xD {
			t.Fatalf("frame of %d octets starting %02x", len(p), p[0])
		}
		out = append(out, dec.Decode(p)...)
	}
	// GSM is a vocoder-like codec: its waveform match is modest, but well above noise.
	if got := snr(in, out, 800); got < 6 {
		t.Fatalf("signal to noise %.1f dB", got)
	} else {
		t.Logf("GSM: %.1f dB", got)
	}
}

func TestGSMRejectsOtherPayloads(t *testing.T) {
	if out := NewGSMDecoder().Decode(make([]byte, 33)); len(out) != 0 {
		t.Fatal("a frame without the GSM signature was decoded")
	}
}
