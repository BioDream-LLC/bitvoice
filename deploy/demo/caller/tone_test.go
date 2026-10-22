package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Every line comes back as itself after G.711 μ-law and a WAV file, as BitVoice would send it; speech-
// like noise and silence come back as nothing.
func TestLinesRoundTrip(t *testing.T) {
	for s := range scenarios {
		callerSegs, takerSegs, _ := timeline(s)
		for _, segs := range [][]segment{callerSegs, takerSegs} {
			for _, seg := range segs {
				n0 := int(seg.at.Seconds() * sampleRate)
				n := int(seg.dur.Seconds() * sampleRate)
				pcm := make([]int16, n)
				for i := range pcm {
					pcm[i] = media.UlawToLinear(media.LinearToUlaw(sample(segs, n0+i)))
				}
				got, rate, err := readWAV(wav(pcm))
				if err != nil {
					t.Fatal(err)
				}
				k := whichLine(got, rate)
				want := -1
				for j := range totalLines() {
					if pitch(j) == seg.freq {
						want = j
					}
				}
				if k != want {
					t.Fatalf("scenario %d: tone %.0f Hz read as line %d, want %d", s, seg.freq, k, want)
				}
			}
		}
	}
	noise := make([]int16, 16000)
	x := uint32(1)
	for i := range noise {
		x = x*1664525 + 1013904223
		noise[i] = int16(x>>16) / 4
	}
	if k := whichLine(noise, 8000); k != -1 {
		t.Fatalf("noise read as line %d", k)
	}
	if k := whichLine(make([]int16, 16000), 8000); k != -1 {
		t.Fatalf("silence read as line %d", k)
	}
	if totalLines() > 80 {
		t.Fatal("too many lines for the telephone band")
	}
}

func wav(pcm []int16) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + 2*len(pcm)))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(8000))
	w(uint32(16000))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(2 * len(pcm)))
	w(pcm)
	return b.Bytes()
}
