package media

import (
	"strings"

	"github.com/biodream-llc/bitvoice/internal/media/ilbc"
)

// iLBC (RFC 3951, RTP RFC 3952) has a dynamic number and two frame modes, chosen with mode=20 or
// mode=30 in the fmtp line (30 when absent, as RFC 3952 says). Each mode has an internal number.
const (
	PTILBC20 = 121
	PTILBC30 = 122
)

func isILBC(pt int) bool { return pt == PTILBC20 || pt == PTILBC30 }

// ilbcPT picks the mode from the fmtp line.
func ilbcPT(fmtp string) int {
	if strings.Contains(strings.ReplaceAll(strings.ToLower(fmtp), " ", ""), "mode=20") {
		return PTILBC20
	}
	return PTILBC30
}

type ilbcv struct {
	mode int
	enc  *ilbc.Encoder
	dec  *ilbc.Decoder
	frm  []byte
	pcm  []int16
}

func newILBC(mode int) *ilbcv {
	enc, _ := ilbc.NewEncoder(mode)
	dec, _ := ilbc.NewDecoder(mode, true)
	return &ilbcv{mode: mode, enc: enc, dec: dec, frm: make([]byte, enc.FrameBytes()), pcm: make([]int16, enc.FrameSamples())}
}

func (*ilbcv) rate() int  { return 8000 }
func (*ilbcv) clock() int { return 8000 }
func (v *ilbcv) take(n int) int {
	if f := v.enc.FrameSamples(); n >= f {
		return f
	}
	return 0
}

func (v *ilbcv) decode(p []byte) []int16 {
	fb := v.dec.FrameBytes()
	// A peer that sends the other mode's frames is followed (38-byte frames are 20 ms, 50 are 30 ms).
	if len(p) > 0 && len(p)%fb != 0 {
		other := 30
		if v.mode == 30 {
			other = 20
		}
		if o := newILBC(other); len(p)%o.dec.FrameBytes() == 0 {
			*v = *o
			fb = v.dec.FrameBytes()
		}
	}
	var out []int16
	for len(p) >= fb {
		v.dec.Decode(v.pcm, p[:fb])
		out = append(out, v.pcm...)
		p = p[fb:]
	}
	return out
}

func (v *ilbcv) encode(pcm []int16) []byte {
	if len(pcm) != v.enc.FrameSamples() {
		return nil
	}
	v.enc.Encode(v.frm, pcm)
	return append([]byte(nil), v.frm...)
}

// conceal makes up lost audio with iLBC's own concealment, a frame at a time.
func (v *ilbcv) conceal(samples int) []int16 {
	var out []int16
	for len(out) < samples {
		v.dec.Decode(v.pcm, nil)
		out = append(out, v.pcm...)
	}
	return out[:samples]
}
