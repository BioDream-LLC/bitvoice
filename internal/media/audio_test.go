package media

import (
	"os"
	"testing"
)

// An MP3 upload reads like a WAV one: 8 kHz mono of the same length; the streaming resampler gives
// what the whole-file one does.
func TestDecodeAudioMP3(t *testing.T) {
	b, err := os.ReadFile("mp3/testdata/m2_22k.mp3")
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := DecodeAudio(b)
	if err != nil {
		t.Fatal(err)
	}
	if secs := float64(len(pcm)) / Rate; secs < 3.9 || secs > 4.2 {
		t.Fatalf("%.2f seconds", secs)
	}
	var peak int16
	for _, s := range pcm {
		peak = max(peak, s, -s)
	}
	if peak < 3000 {
		t.Fatalf("too quiet: %d", peak)
	}
	if _, err := DecodeAudio([]byte("hello, not audio")); err == nil {
		t.Fatal("text accepted as audio")
	}
	// Streamed in uneven pieces, the same samples (to within one) as resampled whole.
	whole := resample(downmix(make16(30000), 2), 22050)
	var r Resampler
	var got []int16
	src := make16(30000)
	for i := 0; i < len(src); {
		n := min(2*(97+i%311), len(src)-i)
		got = append(got, r.Push(src[i:i+n], 22050, 2)...)
		i += n
	}
	if d := len(whole) - len(got); d < 0 || d > 1 {
		t.Fatalf("stream %d samples, whole %d", len(got), len(whole))
	}
	for i := range got {
		if diff := int(got[i]) - int(whole[i]); diff > 1 || diff < -1 {
			t.Fatalf("sample %d: %d vs %d", i, got[i], whole[i])
		}
	}
}

func make16(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16((i*7919)%20000 - 10000)
	}
	return out
}
