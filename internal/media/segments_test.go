package media

import (
	"math"
	"testing"
)

func tone(n int, amp float64) []int16 {
	p := make([]int16, n)
	for i := range p {
		p[i] = int16(amp * math.Sin(float64(i)/4))
	}
	return p
}

func TestSpeechSegments(t *testing.T) {
	const r = 8000
	var pcm []int16
	pcm = append(pcm, tone(r, 30)...)      // 1 s of hiss
	pcm = append(pcm, tone(2*r, 5000)...)  // 2 s of talk
	pcm = append(pcm, tone(r/2, 30)...)    // a short pause: same segment
	pcm = append(pcm, tone(r, 5000)...)    // 1 s more
	pcm = append(pcm, tone(3*r, 30)...)    // a long pause
	pcm = append(pcm, tone(r/10, 5000)...) // a click: too short on its own... padded to 0.5 s
	pcm = append(pcm, tone(2*r, 30)...)
	pcm = append(pcm, tone(40*r, 5000)...) // a long speech: split at 30 s
	segs := SpeechSegments(pcm, r)
	if len(segs) != 4 {
		t.Fatalf("%d segments: %v", len(segs), segs)
	}
	if s := segs[0]; s.Start > r || s.End < 4*r || s.End > int(4.8*r) {
		t.Errorf("first %v", s)
	}
	if d := segs[2].End - segs[2].Start; d != 30*r {
		t.Errorf("long speech not split at 30 s: %d", d)
	}
	if SpeechSegments(tone(5*r, 30), r) != nil {
		t.Error("hiss heard as speech")
	}
	left, right, rate, err := SplitStereoWAV(EncodeStereoWAV([]int16{1, 2, 3}, []int16{4, 5}))
	if err != nil || rate != 8000 || len(left) != 3 || right[1] != 5 || right[2] != 0 {
		t.Fatalf("split %v %v %d %v", left, right, rate, err)
	}
	if _, _, _, err := SplitStereoWAV(EncodeWAV([]int16{1})); err == nil {
		t.Error("mono accepted as stereo")
	}
}
