package media

import (
	"strconv"
	"strings"

	amrnb "github.com/VoiceBlender/goamr-nb"
	amrwb "github.com/VoiceBlender/goamr-wb"
)

// AMR and AMR-WB (RFC 4867) have dynamic numbers. Each has two internal numbers for its two RTP
// framings, octet-aligned and bandwidth-efficient, which the other side chooses with octet-align=1
// in its fmtp line (bandwidth-efficient when absent).
const (
	PTAMR     = 117 // AMR, octet-aligned
	PTAMRBE   = 118 // AMR, bandwidth-efficient
	PTAMRWB   = 119 // AMR-WB, octet-aligned
	PTAMRWBBE = 120 // AMR-WB, bandwidth-efficient
)

func isAMR(pt int) bool { return pt >= PTAMR && pt <= PTAMRWBBE }

// CodecPTFmtp is CodecPT knowing the codec's fmtp line, which picks AMR's framing.
func CodecPTFmtp(name, fmtp string) int {
	pt := CodecPT(name)
	if pt == PTILBC30 {
		return ilbcPT(fmtp)
	}
	if pt == PTSpeex {
		return pt
	}
	if (pt == PTAMR || pt == PTAMRWB) && !strings.Contains(strings.ReplaceAll(strings.ToLower(fmtp), " ", ""), "octet-align=1") {
		pt++ // the bandwidth-efficient twin
	}
	return pt
}

// modeSet reads RFC 4867's mode-set (the modes the other side accepts) and returns the highest, or
// def when none is given.
func modeSet(fmtp string, def, max int) int {
	for _, p := range strings.Split(fmtp, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(k, "mode-set") {
			continue
		}
		best := -1
		for _, m := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(m)); err == nil && n >= 0 && n <= max && n > best {
				best = n
			}
		}
		if best >= 0 {
			return best
		}
	}
	return def
}

// amrv is AMR-NB: 20 ms frames of 160 samples at 8 kHz, at 12.2 kbit/s unless the other side's
// mode-set allows less.
type amrv struct {
	enc *amrnb.Encoder
	dec *amrnb.Decoder
}

func newAMR(octet bool, fmtp string) *amrv {
	enc, err := amrnb.NewEncoder(amrnb.EncoderConfig{Mode: amrnb.Mode(modeSet(fmtp, int(amrnb.Mode1220), 7)), OctetAligned: octet})
	if err != nil {
		return nil
	}
	return &amrv{enc: enc, dec: amrnb.NewDecoder(amrnb.DecoderConfig{OctetAligned: octet})}
}

func (*amrv) rate() int  { return 8000 }
func (*amrv) clock() int { return 8000 }
func (*amrv) take(n int) int {
	if n >= amrnb.FrameSamples {
		return amrnb.FrameSamples
	}
	return 0
}
func (a *amrv) decode(p []byte) []int16 {
	pcm, err := a.dec.Decode(p)
	if err != nil {
		return nil
	}
	return pcm
}
func (a *amrv) encode(pcm []int16) []byte {
	b, err := a.enc.Encode(pcm)
	if err != nil {
		return nil
	}
	return b
}

// amrwbv is AMR-WB (G.722.2): 20 ms frames of 320 samples at 16 kHz, RTP clock 16000, at 23.85
// kbit/s unless the mode-set allows less.
type amrwbv struct {
	enc *amrwb.Encoder
	dec *amrwb.Decoder
}

func newAMRWB(octet bool, fmtp string) *amrwbv {
	enc, err := amrwb.NewEncoder(amrwb.EncoderConfig{Mode: amrwb.Mode(modeSet(fmtp, int(amrwb.Mode2385), 8)), OctetAligned: octet})
	if err != nil {
		return nil
	}
	return &amrwbv{enc: enc, dec: amrwb.NewDecoder(amrwb.DecoderConfig{OctetAligned: octet})}
}

func (*amrwbv) rate() int  { return 16000 }
func (*amrwbv) clock() int { return 16000 }
func (*amrwbv) take(n int) int {
	if n >= amrwb.FrameSamples {
		return amrwb.FrameSamples
	}
	return 0
}
func (a *amrwbv) decode(p []byte) []int16 {
	pcm, err := a.dec.Decode(p)
	if err != nil {
		return nil
	}
	return pcm
}
func (a *amrwbv) encode(pcm []int16) []byte {
	b, err := a.enc.Encode(pcm)
	if err != nil {
		return nil
	}
	return b
}

// newVoiceFmtp makes a codec's voice with the other side's format parameters (AMR's mode-set).
func newVoiceFmtp(pt int, fmtp string) voice {
	switch pt {
	case PTAMR, PTAMRBE:
		if v := newAMR(pt == PTAMR, fmtp); v != nil {
			return v
		}
		return nil
	case PTAMRWB, PTAMRWBBE:
		if v := newAMRWB(pt == PTAMRWB, fmtp); v != nil {
			return v
		}
		return nil
	case PTSpeex:
		return newSpeex(fmtp)
	}
	return newVoice(pt)
}
