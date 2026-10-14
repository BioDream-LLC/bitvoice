package media

import (
	"strconv"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/media/speex"
)

// PTSpeex is narrowband Speex (RFC 5574, speex/8000), a dynamic number in SDP.
const PTSpeex = 123

// speexMode reads RFC 5574's mode= (the Speex mode the other side prefers, 1 to 8); BitVoice
// sends mode 5 (15 kbit/s, Speex's quality 8, what most equipment uses) when none is given.
func speexMode(fmtp string) int {
	for _, part := range strings.Split(strings.ReplaceAll(strings.ToLower(fmtp), " ", ""), ";") {
		if v, ok := strings.CutPrefix(part, "mode="); ok {
			v, _, _ = strings.Cut(strings.Trim(v, `"`), ",")
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 8 {
				return n
			}
		}
	}
	return 5
}

type speexv struct {
	enc *speex.Encoder
	dec *speex.Decoder
}

func newSpeex(fmtp string) *speexv {
	return &speexv{enc: speex.NewEncoderMode(speexMode(fmtp), 2), dec: speex.NewDecoder()}
}

func (*speexv) rate() int  { return 8000 }
func (*speexv) clock() int { return 8000 }
func (*speexv) take(n int) int {
	if n >= speex.FrameSamples {
		return speex.FrameSamples
	}
	return 0
}

func (v *speexv) decode(p []byte) []int16 {
	out, _ := v.dec.Decode(p)
	return out
}

func (v *speexv) encode(pcm []int16) []byte {
	if len(pcm) != speex.FrameSamples {
		return nil
	}
	return v.enc.Encode(pcm)
}

// conceal makes up lost audio with Speex's own concealment, a frame at a time.
func (v *speexv) conceal(samples int) []int16 {
	var out []int16
	for len(out) < samples {
		out = append(out, v.dec.Conceal()...)
	}
	return out[:samples]
}
