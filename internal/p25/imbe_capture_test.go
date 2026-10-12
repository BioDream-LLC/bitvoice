package p25

import (
	"math"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
)

// The voice frames in the ISSI capture decode to speech: no frame refused, and audio that is
// neither silence nor noise-at-full-scale.
func TestDecodeCaptureVoice(t *testing.T) {
	d := imbe.New()
	var pcm []int16
	bad := 0
	for _, b := range rtpOf(t, "testdata/issi.pcap") {
		p, err := Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range p.Voice {
			out, err := d.Decode(v.Info())
			if err != nil {
				bad++
				continue
			}
			pcm = append(pcm, out...)
		}
	}
	var sum, peak float64
	loud := 0
	for i := 0; i+160 <= len(pcm); i += 160 {
		var e float64
		for _, s := range pcm[i : i+160] {
			e += float64(s) * float64(s)
			peak = math.Max(peak, math.Abs(float64(s)))
		}
		if math.Sqrt(e/160) > 300 {
			loud++
		}
		sum += e
	}
	rms := math.Sqrt(sum / float64(len(pcm)))
	t.Logf("%d frames (%d refused): rms %.0f, peak %.0f, %d of %d frames with speech", len(pcm)/160, bad, rms, peak, loud, len(pcm)/160)
	if bad > 0 || len(pcm) < 160*100 || loud < 20 || peak >= 32767 {
		t.Fatal("the capture's voice did not decode to speech")
	}
}

// Real speech through BitVoice's encoder: the capture's decoded voice, encoded again and decoded,
// keeps its loudness contour.
func TestTandemCaptureVoice(t *testing.T) {
	d := imbe.New()
	var speech []int16
	for _, b := range rtpOf(t, "testdata/issi.pcap") {
		p, _ := Parse(b)
		for _, v := range p.Voice {
			out, _ := d.Decode(v.Info())
			speech = append(speech, out...)
		}
	}
	enc, dec := imbe.NewEncoder(), imbe.New()
	var again []int16
	for i := 0; i+160 <= len(speech); i += 160 {
		out, err := dec.Decode(enc.Encode(speech[i : i+160]))
		if err != nil {
			t.Fatalf("frame %d: %v", i/160, err)
		}
		again = append(again, out...)
	}
	level := func(x []int16) []float64 {
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
	a, b := level(speech), level(again)
	best := -1.0
	for lag := 0; lag <= 3; lag++ {
		var sab, saa, sbb, ma, mb float64
		n := len(a) - lag
		for i := 0; i < n; i++ {
			ma, mb = ma+a[i], mb+b[i+lag]
		}
		ma, mb = ma/float64(n), mb/float64(n)
		for i := 0; i < n; i++ {
			sab += (a[i] - ma) * (b[i+lag] - mb)
			saa += (a[i] - ma) * (a[i] - ma)
			sbb += (b[i+lag] - mb) * (b[i+lag] - mb)
		}
		best = math.Max(best, sab/math.Sqrt(saa*sbb))
	}
	t.Logf("loudness correlation after a tandem: %.2f", best)
	if best < 0.9 {
		t.Fatalf("the speech's loudness contour is lost: %.2f", best)
	}
}
