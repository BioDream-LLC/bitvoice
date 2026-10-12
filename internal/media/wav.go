package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Rate is the sample rate of all audio BitVoice stores and plays: telephone audio is 8 kHz.
const Rate = 8000

// EncodeWAV writes 8 kHz mono 16-bit PCM as a WAV file, which every browser and phone tool can play.
func EncodeWAV(pcm []int16) []byte {
	var b bytes.Buffer
	data := len(pcm) * 2
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, struct {
		Format, Channels uint16
		Rate, ByteRate   uint32
		Align, Bits      uint16
	}{1, 1, Rate, Rate * 2, 2, 16})
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data))
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

// EncodeStereoWAV writes two 8 kHz channels (a call recording: caller left, called party right). The
// shorter channel is padded with silence.
func EncodeStereoWAV(left, right []int16) []byte {
	n := max(len(left), len(right))
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+n*4))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, struct {
		Format, Channels uint16
		Rate, ByteRate   uint32
		Align, Bits      uint16
	}{1, 2, Rate, Rate * 4, 4, 16})
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(n*4))
	frame := make([]int16, 2*n)
	for i := range n {
		if i < len(left) {
			frame[2*i] = left[i]
		}
		if i < len(right) {
			frame[2*i+1] = right[i]
		}
	}
	binary.Write(&b, binary.LittleEndian, frame)
	return b.Bytes()
}

// Track records one party of a call against the wall clock, so two tracks started together stay in step
// whatever each side's packet timing: audio is placed where it arrived, and gaps become silence.
type Track struct {
	mu    sync.Mutex
	start time.Time
	pcm   []int16
	max   int
	now   func() time.Time
	dec   Decoders
	// paused drops audio (a card number being read out); the gap becomes silence when it resumes.
	paused bool
}

// Pause stops or restarts keeping audio.
func (t *Track) Pause(on bool) {
	t.mu.Lock()
	t.paused = on
	t.mu.Unlock()
}

// NewTrack starts a track at start, holding at most maxSeconds.
func NewTrack(start time.Time, maxSeconds int) *Track {
	return &Track{start: start, max: maxSeconds * Rate, now: time.Now}
}

// Packet is the Endpoint.Record callback.
func (t *Track) Packet(pkt []byte) {
	if len(pkt) < 12 || pkt[0]&0x30 != 0 || pkt[0]&0x0f != 0 { // skip padded, extended or CSRC packets
		return
	}
	pt := int(pkt[1] & 0x7f)
	if !Transcodable(pt) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paused {
		return
	}
	samples := t.dec.Narrowband(pt, pkt[12:])
	at := int(t.now().Sub(t.start).Seconds()*Rate) - len(samples) // where this packet's audio began
	// Packets arrive in bursts (network jitter): keep appending while within 100 ms of the clock, so audio
	// is not chopped; jump forward only across a real gap.
	if at > len(t.pcm)+Rate/10 {
		t.pcm = append(t.pcm, make([]int16, min(at, t.max)-len(t.pcm))...)
	}
	for _, v := range samples {
		if len(t.pcm) >= t.max {
			return
		}
		t.pcm = append(t.pcm, v)
	}
}

// Samples returns what was recorded.
func (t *Track) Samples() []int16 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int16(nil), t.pcm...)
}

// ErrWAV is returned for audio that is not a WAV file BitVoice can read.
var ErrWAV = errors.New("not a WAV file BitVoice can read: use 16-bit PCM, µ-law or A-law")

// DecodeWAV reads a WAV file (16-bit PCM, µ-law or A-law; mono or stereo; any sample rate) and returns
// 8 kHz mono 16-bit samples. The web interface converts other formats to WAV before uploading.
func DecodeWAV(b []byte) ([]int16, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, ErrWAV
	}
	var format, channels, bits uint16
	var rate uint32
	var data []byte
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		size := int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8:]
		if size > len(body) {
			size = len(body) // a truncated final chunk: use what is there
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, ErrWAV
			}
			format = binary.LittleEndian.Uint16(body)
			channels = binary.LittleEndian.Uint16(body[2:])
			rate = binary.LittleEndian.Uint32(body[4:])
			bits = binary.LittleEndian.Uint16(body[14:])
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE: the real format is in the sub-format
				format = binary.LittleEndian.Uint16(body[24:])
			}
		case "data":
			data = body[:size]
		}
		p += 8 + size + size%2
	}
	if channels == 0 || channels > 8 || rate < 4000 || rate > 192000 || data == nil {
		return nil, ErrWAV
	}
	var mono []float64
	ch := int(channels)
	switch {
	case format == 1 && bits == 16:
		n := len(data) / 2 / ch
		mono = make([]float64, n)
		for i := range n {
			var s float64
			for c := range ch {
				s += float64(int16(binary.LittleEndian.Uint16(data[(i*ch+c)*2:])))
			}
			mono[i] = s / float64(ch)
		}
	case (format == 6 || format == 7) && bits == 8:
		n := len(data) / ch
		mono = make([]float64, n)
		for i := range n {
			var s float64
			for c := range ch {
				if format == 7 {
					s += float64(UlawToLinear(data[i*ch+c]))
				} else {
					s += float64(AlawToLinear(data[i*ch+c]))
				}
			}
			mono[i] = s / float64(ch)
		}
	default:
		return nil, fmt.Errorf("%w (this one is format %d, %d bits)", ErrWAV, format, bits)
	}
	return resample(mono, int(rate)), nil
}

// resample converts to 8 kHz. Going down it averages each output sample's span of input (a box filter),
// which keeps most aliasing out of voice audio; going up it interpolates linearly.
func resample(in []float64, rate int) []int16 {
	if rate == Rate {
		out := make([]int16, len(in))
		for i, v := range in {
			out[i] = clamp(v)
		}
		return out
	}
	step := float64(rate) / Rate
	n := int(float64(len(in)) / step)
	out := make([]int16, n)
	for i := range n {
		start := float64(i) * step
		if step > 1 {
			a, z := int(start), int(start+step)
			if z > len(in) {
				z = len(in)
			}
			var s float64
			for _, v := range in[a:z] {
				s += v
			}
			out[i] = clamp(s / float64(max(z-a, 1)))
			continue
		}
		a := int(start)
		f := start - float64(a)
		v := in[a]
		if a+1 < len(in) {
			v += (in[a+1] - v) * f
		}
		out[i] = clamp(v)
	}
	return out
}

func clamp(v float64) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}

// Capture collects what a party says, as 8 kHz PCM, and the keys they press (RFC 4733 events). Feed it
// from Endpoint.Record. Packets are taken in arrival order; a lost packet becomes silence.
type Capture struct {
	mu      sync.Mutex
	pcm     []int16
	max     int
	lastSeq uint16
	have    bool
	full    bool
	// Keys receives each key press once (digits, * and #). It is buffered; presses are dropped when full.
	Keys    chan byte
	lastEv  uint32 // timestamp of the last event reported, so the repeated end packets count once
	evSeen  bool
	stopped bool
	dec     Decoders
	lastLen int // samples in the last packet, to fill a lost one with silence
}

// NewCapture records up to maxSeconds of audio.
func NewCapture(maxSeconds int) *Capture {
	return &Capture{max: maxSeconds * Rate, Keys: make(chan byte, 16)}
}

// Packet is the Endpoint.Record callback.
func (c *Capture) Packet(pkt []byte) {
	if len(pkt) < 12 {
		return
	}
	hdr := 12 + 4*int(pkt[0]&0x0f)
	if pkt[0]&0x10 != 0 && len(pkt) >= hdr+4 { // header extension
		hdr += 4 + 4*int(binary.BigEndian.Uint16(pkt[hdr+2:]))
	}
	if hdr > len(pkt) {
		return
	}
	payload := pkt[hdr:]
	if pkt[0]&0x20 != 0 && len(payload) > 0 { // padding
		payload = payload[:max(len(payload)-int(payload[len(payload)-1]), 0)]
	}
	pt := pkt[1] & 0x7f
	seq := binary.BigEndian.Uint16(pkt[2:])
	ts := binary.BigEndian.Uint32(pkt[4:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if pt >= 96 && pt != PTOpus && len(payload) == 4 { // telephone-event: report a key once, at its end
		ev, end := payload[0], payload[1]&0x80 != 0
		if end && !(c.evSeen && c.lastEv == ts) {
			c.evSeen, c.lastEv = true, ts
			if k := eventKey(ev); k != 0 {
				select {
				case c.Keys <- k:
				default:
				}
			}
		}
		return
	}
	if !Transcodable(int(pt)) {
		return
	}
	if c.have {
		gap := int(seq - c.lastSeq)
		if gap <= 0 || gap > 3000 {
			return // duplicate or reordered: drop it rather than smear the audio
		}
		for i := 1; i < gap && i < 50; i++ {
			c.add(make([]int16, c.lastLen))
		}
	}
	c.have, c.lastSeq = true, seq
	pcm := c.dec.Narrowband(int(pt), payload)
	c.lastLen = len(pcm)
	c.add(pcm)
}

func (c *Capture) add(s []int16) {
	room := c.max - len(c.pcm)
	if room <= 0 {
		c.full = true
		return
	}
	if len(s) > room {
		s = s[:room]
	}
	c.pcm = append(c.pcm, s...)
}

// Samples returns a copy of what has been captured so far.
func (c *Capture) Samples() []int16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int16(nil), c.pcm...)
}

// Full reports whether the maximum length has been reached.
func (c *Capture) Full() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.full
}

// Stop ends recording and returns the audio.
func (c *Capture) Stop() []int16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	return c.pcm
}

// EventKey is the key an RFC 4733 event number stands for, or 0.
func EventKey(ev byte) byte { return eventKey(ev) }

func eventKey(ev byte) byte {
	switch {
	case ev <= 9:
		return '0' + ev
	case ev == 10:
		return '*'
	case ev == 11:
		return '#'
	}
	return 0
}

// TrimSilence removes quiet audio at the end (the pause before a caller hangs up) and a key press tone
// that may have been captured as audio.
func TrimSilence(pcm []int16) []int16 {
	const quiet = 500
	end := len(pcm)
	for end > 0 {
		a := max(end-Rate/50, 0) // 20 ms windows
		loud := false
		for _, v := range pcm[a:end] {
			if v > quiet || v < -quiet {
				loud = true
				break
			}
		}
		if loud {
			break
		}
		end = a
	}
	return pcm[:min(end+Rate/4, len(pcm))]
}
