package main

import (
	"math"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
)

// A unit's made-up speech survives IMBE: much of each second, decoded, is loud enough for BitVoice
// to count as someone talking (so a transmission is one recording, not many short ones).
func TestTalkThroughIMBE(t *testing.T) {
	enc, dec := imbe.NewEncoder(), imbe.New()
	v := newTalk(1207)
	loud := 0
	const frames = 150
	for range frames {
		pcm, err := dec.Decode(enc.Encode(v.next()))
		if err != nil {
			t.Fatal(err)
		}
		var e float64
		for _, s := range pcm {
			e += float64(s) * float64(s)
		}
		if math.Sqrt(e/float64(len(pcm))) >= 300 {
			loud++
		}
	}
	t.Logf("%d of %d frames loud", loud, frames)
	if loud < frames*4/10 {
		t.Fatalf("only %d of %d frames loud", loud, frames)
	}
}
