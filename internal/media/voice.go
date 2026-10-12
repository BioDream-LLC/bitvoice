package media

import (
	"sync"

	"github.com/darui3018823/opus"
	"github.com/hunydev/g729"
)

// PTG729 is G.729's static payload type. PTOpus is the number BitVoice uses for Opus inside the
// engine: Opus has no static type, so each party's own dynamic number is mapped to this one as its
// packets come in and back to it as they go out (Endpoint.MapIncoming, MapPayloadType).
const (
	PTG729 = 18
	PTOpus = 111
)

// voice is one codec's encoder or decoder, holding the state that carries from packet to packet.
type voice interface {
	rate() int  // the PCM sample rate it decodes to and encodes from
	clock() int // its RTP clock rate
	decode(payload []byte) []int16
	// take says how many of n buffered samples to encode as one packet (0: wait for more).
	take(n int) int
	encode(pcm []int16) []byte
}

// concealer is a decoder that can make up audio for lost packets.
type concealer interface {
	conceal(samples int) []int16
}

func newVoice(pt int) voice {
	switch pt {
	case PTPCMU, PTPCMA:
		return g711{pt}
	case PTG722:
		return &g722v{dec: NewG722Decoder(), enc: NewG722Encoder()}
	case PTG729:
		return &g729v{}
	case PTOpus:
		return &opusv{}
	}
	return nil
}

type g711 struct{ pt int }

func (g711) rate() int      { return 8000 }
func (g711) clock() int     { return 8000 }
func (g711) take(n int) int { return n }
func (g g711) decode(p []byte) []int16 {
	out := make([]int16, len(p))
	for i, b := range p {
		if g.pt == PTPCMU {
			out[i] = UlawToLinear(b)
		} else {
			out[i] = AlawToLinear(b)
		}
	}
	return out
}
func (g g711) encode(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i, v := range pcm {
		if g.pt == PTPCMU {
			out[i] = LinearToUlaw(v)
		} else {
			out[i] = LinearToAlaw(v)
		}
	}
	return out
}

// G.722 is sampled at 16 kHz but, for historical reasons, its RTP clock runs at 8000 (RFC 3551).
type g722v struct {
	dec *G722Decoder
	enc *G722Encoder
}

func (*g722v) rate() int                   { return 16000 }
func (*g722v) clock() int                  { return 8000 }
func (*g722v) take(n int) int              { return n &^ 1 }
func (g *g722v) decode(p []byte) []int16   { return g.dec.Decode(p) }
func (g *g722v) encode(pcm []int16) []byte { return g.enc.Encode(pcm) }

// G.729 (Annex A bitstream): 10 ms frames of 10 octets. A 2-octet Annex B silence frame decodes to
// silence; BitVoice offers annexb=no, so it only sees one from a party that ignores that.
//
// The G.729 library keeps one overflow flag for the whole process (its synthesis filter clears it,
// filters, then checks it), so two streams coding at once would corrupt each other's audio: every
// G.729 frame is coded under g729Mu. One core then codes about 170 calls both ways.
var g729Mu sync.Mutex

type g729v struct {
	dec *g729.Decoder
	enc *g729.Encoder
}

func (*g729v) rate() int      { return 8000 }
func (*g729v) clock() int     { return 8000 }
func (*g729v) take(n int) int { return n - n%80 }
func (g *g729v) decode(p []byte) []int16 {
	if g.dec == nil {
		g.dec = g729.NewDecoder()
	}
	var out []int16
	frame := make([]int16, 80)
	g729Mu.Lock()
	defer g729Mu.Unlock()
	for len(p) >= 10 {
		if err := g.dec.DecodeFrame(p[:10], frame); err != nil {
			clear(frame)
		}
		out = append(out, frame...)
		p = p[10:]
	}
	if len(p) == 2 {
		out = append(out, make([]int16, 80)...)
	}
	return out
}
func (g *g729v) encode(pcm []int16) []byte {
	if g.enc == nil {
		g.enc = g729.NewEncoder()
	}
	out := make([]byte, 0, len(pcm)/8)
	frame := make([]byte, 10)
	g729Mu.Lock()
	defer g729Mu.Unlock()
	for len(pcm) >= 80 {
		if err := g.enc.EncodeFrame(pcm[:80], frame); err != nil {
			return nil
		}
		out = append(out, frame...)
		pcm = pcm[80:]
	}
	return out
}

// Opus: BitVoice decodes and encodes wideband (16 kHz) mono speech; the RTP clock is always 48000
// (RFC 7587). In-band FEC is sent so a lost packet can be rebuilt from the next one.
type opusv struct {
	dec  *opus.Decoder
	enc  *opus.Encoder
	pcm  []int16
	last int // the last frame's length, for concealment
}

func (*opusv) rate() int  { return 16000 }
func (*opusv) clock() int { return 48000 }

// take picks the longest Opus frame (10, 20, 40 or 60 ms) the buffer fills.
func (*opusv) take(n int) int {
	for _, f := range []int{960, 640, 320, 160} {
		if n >= f {
			return f
		}
	}
	return 0
}

func (o *opusv) decode(p []byte) []int16 {
	if o.dec == nil {
		d, err := opus.NewDecoder(16000, 1)
		if err != nil {
			return nil
		}
		o.dec, o.pcm = d, make([]int16, 5760)
	}
	n, err := o.dec.Decode(p, o.pcm)
	if err != nil || n <= 0 {
		return nil
	}
	o.last = n
	return append([]int16(nil), o.pcm[:n]...)
}

func (o *opusv) conceal(samples int) []int16 {
	if o.dec == nil || samples <= 0 {
		return nil
	}
	var out []int16
	for samples > 0 {
		f := o.last
		if f <= 0 || f > samples {
			f = min(samples, 320)
		}
		n, err := o.dec.DecodePLC(o.pcm, f)
		if err != nil || n <= 0 {
			out = append(out, make([]int16, samples)...)
			break
		}
		out = append(out, o.pcm[:n]...)
		samples -= n
	}
	return out
}

func (o *opusv) encode(pcm []int16) []byte {
	if o.enc == nil {
		e, err := opus.NewEncoder(16000, 1, opus.ApplicationVOIP)
		if err != nil {
			return nil
		}
		_ = e.SetBitrate(24000)
		_ = e.SetComplexity(5)
		e.SetInbandFEC(true)
		e.SetPacketLossPerc(5)
		o.enc = e
	}
	b, err := o.enc.Encode(pcm, len(pcm))
	if err != nil {
		return nil
	}
	return b
}

// resample converts between the two rates codecs here use, 8 and 16 kHz.
func resampleVoice(pcm []int16, from, to int, down *Halver, up *Doubler) []int16 {
	switch {
	case from == to:
		return pcm
	case from == 16000 && to == 8000:
		return down.Halve(pcm)
	case from == 8000 && to == 16000:
		return up.Double(pcm)
	}
	return pcm
}

// Decoders decodes the audio of one stream, whatever codec each packet carries, to 8 kHz.
type Decoders struct {
	by   map[int]voice
	down Halver
}

// Narrowband decodes one payload to 8 kHz linear PCM (nil when the codec is not one BitVoice speaks).
func (d *Decoders) Narrowband(pt int, payload []byte) []int16 {
	if d.by == nil {
		d.by = map[int]voice{}
	}
	v := d.by[pt]
	if v == nil {
		if v = newVoice(pt); v == nil {
			return nil
		}
		d.by[pt] = v
	}
	pcm := v.decode(payload)
	if v.rate() == 16000 {
		return d.down.Halve(pcm)
	}
	return pcm
}
