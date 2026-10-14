package media

import (
	"encoding/binary"
	"math"
	"strings"
)

// Payload types BitVoice can decode and encode itself (PTG729 and PTOpus are in voice.go). It
// converts between any two of them.
const (
	PTPCMU = 0
	PTPCMA = 8
	PTG722 = 9
	PTGSM  = 3 // GSM 06.10 full rate, 33 bytes per 20 ms
)

// Transcodable reports whether BitVoice can convert audio of this payload type.
func Transcodable(pt int) bool {
	switch pt {
	case PTPCMU, PTPCMA, PTG722, PTG729, PTOpus, PTG726_32, PTG726_16, PTAAL2G726_32, PTGSM, PTG726_24, PTG726_40:
		return true
	}
	if isAMR(pt) || isILBC(pt) || pt == PTSpeex {
		return true
	}
	return false
}

// CodecPT returns the payload type for a codec name (BitVoice's own number for Opus), or -1.
func CodecPT(name string) int {
	switch strings.ToUpper(name) {
	case "PCMU":
		return PTPCMU
	case "PCMA":
		return PTPCMA
	case "G722":
		return PTG722
	case "G729":
		return PTG729
	case "OPUS":
		return PTOpus
	case "G726-32":
		return PTG726_32
	case "G726-16":
		return PTG726_16
	case "G726-24":
		return PTG726_24
	case "G726-40":
		return PTG726_40
	case "AMR":
		return PTAMR
	case "ILBC":
		return PTILBC30 // the mode comes from fmtp: CodecPTFmtp
	case "SPEEX":
		return PTSpeex
	case "AMR-WB":
		return PTAMRWB
	case "AAL2-G726-32":
		return PTAAL2G726_32
	case "GSM":
		return PTGSM
	}
	return -1
}

// Narrowband decodes one payload to 8 kHz linear PCM with a stream's decoders.
func Narrowband(pt int, payload []byte, d *Decoders) []int16 { return d.Narrowband(pt, payload) }

// Halver turns 16 kHz audio into 8 kHz: a low-pass filter below 4 kHz, then every other sample.
type Halver struct{ hist [halfTaps - 1]int32 }

// halfband is a 31-tap half-band low-pass filter (cut-off a quarter of the rate): a Blackman-windowed
// sinc, scaled so the taps sum to 2^15 (unity gain at DC).
const halfTaps = 31

var halfband = func() (h [halfTaps]int32) {
	var f [halfTaps]float64
	sum := 0.0
	for n := range f {
		x := float64(n - halfTaps/2)
		v := 0.5
		if x != 0 {
			v = math.Sin(math.Pi*x/2) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(n)/(halfTaps-1)) + 0.08*math.Cos(4*math.Pi*float64(n)/(halfTaps-1))
		f[n] = v * w
		sum += f[n]
	}
	for n := range f {
		h[n] = int32(math.Round(f[n] / sum * 32768))
	}
	return
}()

func (h *Halver) Halve(in []int16) []int16 {
	buf := make([]int32, 0, len(h.hist)+len(in))
	buf = append(buf, h.hist[:]...)
	for _, v := range in {
		buf = append(buf, int32(v))
	}
	out := make([]int16, 0, len(in)/2)
	for i := 0; i+halfTaps <= len(buf); i += 2 {
		var acc int32
		for k, c := range halfband {
			acc += buf[i+k] * c
		}
		out = append(out, int16(sat16(int(acc>>15))))
	}
	copy(h.hist[:], buf[len(buf)-len(h.hist):])
	return out
}

// Doubler turns 8 kHz audio into 16 kHz: zero-stuffing, then the same half-band filter (gain 2).
type Doubler struct{ hist [halfTaps - 1]int32 }

func (d *Doubler) Double(in []int16) []int16 {
	buf := make([]int32, 0, len(d.hist)+2*len(in))
	buf = append(buf, d.hist[:]...)
	for _, v := range in {
		buf = append(buf, int32(v), 0)
	}
	out := make([]int16, 0, 2*len(in))
	for i := 0; i+halfTaps <= len(buf); i++ {
		var acc int32
		for k, c := range halfband {
			acc += buf[i+k] * c
		}
		out = append(out, int16(sat16(int(acc>>14))))
	}
	copy(d.hist[:], buf[len(buf)-len(d.hist):])
	return out
}

// Transcoder converts one direction of a stream from one codec to another. It keeps its own
// sequence numbers and timestamps, because codecs differ in RTP clock (Opus runs at 48000) and in
// the frame lengths they can send (Opus 10 to 60 ms, G.729 multiples of 10 ms): audio is buffered
// until the next codec can send it, so a packet in may give no packet out.
type Transcoder struct {
	from, to int
	dec, enc voice
	down     Halver
	up       Doubler
	buf      []int16 // decoded audio, at the output codec's rate, waiting to be sent
	started  bool
	inNext   uint32 // the input timestamp the next packet should have
	outTS    uint32 // the output timestamp of buf[0]
	seq      uint16
	lastSeq  uint16
	lastTS   uint32
	// inRef and outRef are one input timestamp and the output timestamp it became, for key presses.
	inRef, outRef uint32
	mapped        bool
}

func NewTranscoder(from, to int) *Transcoder { return NewTranscoderFmtp(from, to, "") }

// NewTranscoderFmtp is NewTranscoder with the receiving party's format parameters (AMR's mode-set).
func NewTranscoderFmtp(from, to int, fmtp string) *Transcoder {
	return &Transcoder{from: from, to: to, dec: newVoiceFmtp(from, ""), enc: newVoiceFmtp(to, fmtp)}
}

// scale converts a count of from-clock ticks into to-clock ticks.
func scale(n int64, from, to int) int64 { return n * int64(to) / int64(from) }

// Convert returns the packet re-encoded, or nil while audio is being buffered. pkt must be a plain
// RTP packet without CSRCs, extension or padding; anything else is returned unchanged.
func (t *Transcoder) Convert(pkt []byte) []byte {
	if len(pkt) < 12 || pkt[0]&0x3f != 0 || t.dec == nil || t.enc == nil {
		return pkt
	}
	if (t.from == PTPCMU || t.from == PTPCMA) && (t.to == PTPCMU || t.to == PTPCMA) {
		return transcodeG711(pkt, t.from, t.to) // sample for sample: timing kept as it is
	}
	seq := binary.BigEndian.Uint16(pkt[2:])
	ts := binary.BigEndian.Uint32(pkt[4:])
	if t.started && int16(seq-t.lastSeq) <= 0 {
		return nil // late or repeated
	}
	inClock, outClock := t.dec.clock(), t.enc.clock()
	if !t.started {
		t.started = true
		t.outTS = uint32(scale(int64(ts), inClock, outClock))
		t.seq = seq
		t.inNext = ts
	}
	if gap := int64(int32(ts - t.inNext)); gap != 0 {
		c, ok := t.dec.(concealer)
		if lost := scale(gap, inClock, t.dec.rate()); ok && gap > 0 && lost <= int64(t.dec.rate())/8 {
			// A packet or two lost: the decoder makes up the missing audio.
			t.buf = append(t.buf, resampleVoice(c.conceal(int(lost)), t.dec.rate(), t.enc.rate(), &t.down, &t.up)...)
		} else {
			// A pause (silence suppression) or a new stream: start again after it.
			t.outTS += uint32(scale(int64(len(t.buf)), t.enc.rate(), outClock) + scale(gap, inClock, outClock))
			t.buf = t.buf[:0]
		}
	}
	t.inRef, t.outRef, t.mapped = ts, t.outTS+uint32(scale(int64(len(t.buf)), t.enc.rate(), outClock)), true
	pcm := t.dec.decode(pkt[12:])
	t.inNext = ts + uint32(scale(int64(len(pcm)), t.dec.rate(), inClock))
	t.lastSeq, t.lastTS = seq, ts
	t.buf = append(t.buf, resampleVoice(pcm, t.dec.rate(), t.enc.rate(), &t.down, &t.up)...)
	n := t.enc.take(len(t.buf))
	if n == 0 {
		return nil
	}
	payload := t.enc.encode(t.buf[:n])
	out := make([]byte, 12, 12+len(payload))
	copy(out, pkt[:12])
	out[1] = out[1]&0x80 | byte(t.to)
	binary.BigEndian.PutUint16(out[2:], t.seq)
	binary.BigEndian.PutUint32(out[4:], t.outTS)
	t.seq++
	t.outTS += uint32(scale(int64(n), t.enc.rate(), outClock))
	t.buf = append(t.buf[:0], t.buf[n:]...)
	if payload == nil {
		return nil
	}
	return append(out, payload...)
}

// Event re-times an RFC 4733 key press for the output side, whose clock may differ (Opus runs at
// 48000, the others at 8000): its timestamp follows the audio's, its duration is rescaled.
func (t *Transcoder) Event(pkt []byte) []byte {
	if !t.mapped || len(pkt) != 16 || t.dec == nil || t.enc == nil {
		return pkt
	}
	inClock, outClock := t.dec.clock(), t.enc.clock()
	out := append([]byte(nil), pkt...)
	ts := binary.BigEndian.Uint32(pkt[4:])
	binary.BigEndian.PutUint32(out[4:], t.outRef+uint32(scale(int64(int32(ts-t.inRef)), inClock, outClock)))
	d := scale(int64(binary.BigEndian.Uint16(pkt[14:])), inClock, outClock)
	binary.BigEndian.PutUint16(out[14:], uint16(min(d, 0xffff)))
	return out
}
