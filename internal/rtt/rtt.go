// Package rtt carries real-time text: T.140 text over RTP (RFC 4103), each character sent as it is
// typed, with RFC 2198 redundancy so a lost packet's text arrives in the next ones. It is how NG911
// calls and VoLTE phones carry text beside the voice.
package rtt

import (
	"encoding/binary"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Characters with a meaning in T.140.
const (
	BOM       = '\uFEFF' // sent first; carries nothing
	Backspace = '\u0008'
	LineSep   = '\u2028' // a new line
	Lost      = '\uFFFD' // marks text lost in transit (RFC 4103 4.5)
)

// Interval is how long text is gathered before it is sent (RFC 4103 recommends 300 ms).
const Interval = 300 * time.Millisecond

// Level is the redundancy: each packet repeats the text of this many packets before it.
const Level = 2

// Receiver turns RTP packets of a T.140 stream into text. Text comes out once, in order, whether it
// arrived as the primary data or as redundancy; text lost beyond the redundancy becomes Lost.
type Receiver struct {
	T140, Red int // the payload types negotiated (Red 0 when the stream has no redundancy)

	mu      sync.Mutex
	started bool
	next    uint16 // the sequence number whose text is due next
}

// Packet takes one RTP packet and returns the new text in it (possibly empty). It accepts packets
// after the RTP header has been checked by the caller (version 2).
func (r *Receiver) Packet(pkt []byte) string {
	if len(pkt) < 12 || pkt[0]>>6 != 2 {
		return ""
	}
	cc := int(pkt[0] & 0x0f)
	off := 12 + 4*cc
	if pkt[0]&0x10 != 0 { // header extension
		if len(pkt) < off+4 {
			return ""
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(pkt[off+2:]))
	}
	end := len(pkt)
	if pkt[0]&0x20 != 0 && end > off { // padding
		end -= int(pkt[end-1])
	}
	if off > end {
		return ""
	}
	pt := int(pkt[1] & 0x7f)
	seq := binary.BigEndian.Uint16(pkt[2:])
	payload := pkt[off:end]

	// blocks, oldest first: generation i belongs to packet seq-(n-1-i)
	var blocks [][]byte
	switch {
	case pt == r.T140:
		blocks = [][]byte{payload}
	case r.Red != 0 && pt == r.Red:
		var ok bool
		if blocks, ok = splitRed(payload, r.T140); !ok {
			return ""
		}
	default:
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		r.started = true
		r.next = seq - uint16(len(blocks)-1) // the redundancy carries what came before
	}
	ahead := int16(seq - r.next) // how far this packet is past the next one due
	if ahead < 0 {
		return "" // already had it (a duplicate, or late)
	}
	var b strings.Builder
	have := len(blocks) - 1 // generations before the primary
	if int(ahead) > have {
		b.WriteRune(Lost) // more packets lost than the redundancy covers
		r.next = seq - uint16(have)
	}
	for i, blk := range blocks {
		s := seq - uint16(len(blocks)-1-i)
		if int16(s-r.next) < 0 {
			continue
		}
		b.WriteString(clean(blk))
	}
	r.next = seq + 1
	return b.String()
}

// splitRed splits an RFC 2198 payload into its blocks, oldest first; blocks of another payload type
// are kept empty (a sender's other media, not text).
func splitRed(p []byte, t140 int) ([][]byte, bool) {
	type head struct{ pt, n int }
	var hs []head
	i := 0
	for {
		if i >= len(p) {
			return nil, false
		}
		if p[i]&0x80 == 0 { // the last header: the primary
			hs = append(hs, head{int(p[i] & 0x7f), -1})
			i++
			break
		}
		if i+4 > len(p) {
			return nil, false
		}
		n := int(binary.BigEndian.Uint16(p[i+2:]) & 0x3ff)
		hs = append(hs, head{int(p[i] & 0x7f), n})
		i += 4
	}
	out := make([][]byte, len(hs))
	for k, h := range hs {
		n := h.n
		if n < 0 {
			n = len(p) - i
		}
		if i+n > len(p) {
			return nil, false
		}
		if h.pt == t140 {
			out[k] = p[i : i+n]
		}
		i += n
	}
	return out, true
}

// clean drops what carries nothing (the BOM) and broken UTF-8.
func clean(b []byte) string {
	if !utf8.Valid(b) {
		b = []byte(strings.ToValidUTF8(string(b), string(Lost)))
	}
	return strings.ReplaceAll(string(b), string(BOM), "")
}

// Sender turns typed text into RTP packets: text is gathered for an Interval, then sent with the
// previous packets' text repeated, and once typing stops the redundancy is flushed with packets that
// carry no new text.
type Sender struct {
	T140, Red int
	SSRC      uint32
	Write     func(pkt []byte) // sends one packet

	mu      sync.Mutex
	pending []byte
	gens    [][]byte // the last packets' primary text, oldest first
	ts      []uint32 // and their timestamps
	seq     uint16
	start   time.Time
	idle    int // packets sent since text last went
	started bool
	timer   *time.Timer
	closed  bool
}

// Send queues text; it goes within an Interval. The first text of a stream is preceded by a BOM.
func (s *Sender) Send(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || text == "" {
		return
	}
	if !s.started {
		s.started = true
		s.start = time.Now()
		s.seq = uint16(time.Now().UnixNano())
		s.pending = append(s.pending, string(BOM)...)
	}
	s.pending = append(s.pending, text...)
	if s.timer == nil {
		s.timer = time.AfterFunc(Interval, s.tick)
	}
}

// Close stops sending.
func (s *Sender) Close() {
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
}

func (s *Sender) tick() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	primary := s.pending
	s.pending = nil
	if len(primary) == 0 {
		s.idle++
	} else {
		s.idle = 0
	}
	ts := uint32(time.Since(s.start).Milliseconds()) // T.140's clock is 1000 Hz
	pkt := s.packet(primary, ts)
	s.gens = append(s.gens, primary)
	s.ts = append(s.ts, ts)
	if len(s.gens) > Level {
		s.gens, s.ts = s.gens[1:], s.ts[1:]
	}
	// keep sending while there is redundancy still to deliver
	if s.idle < Level || len(s.pending) > 0 {
		s.timer = time.AfterFunc(Interval, s.tick)
	} else {
		s.timer, s.gens, s.ts = nil, nil, nil
	}
	w := s.Write
	s.mu.Unlock()
	if w != nil {
		w(pkt)
	}
}

// packet builds one RTP packet: the redundant generations, then the primary. Called with s.mu held.
func (s *Sender) packet(primary []byte, ts uint32) []byte {
	pkt := make([]byte, 12, 12+64+len(primary))
	pkt[0] = 0x80
	marker := len(s.gens) == 0 // the first packet after an idle period
	pt := s.T140
	if s.Red != 0 {
		pt = s.Red
	}
	pkt[1] = byte(pt)
	if marker {
		pkt[1] |= 0x80
	}
	binary.BigEndian.PutUint16(pkt[2:], s.seq)
	s.seq++
	binary.BigEndian.PutUint32(pkt[4:], ts)
	binary.BigEndian.PutUint32(pkt[8:], s.SSRC)
	if s.Red == 0 {
		return append(pkt, primary...)
	}
	// Redundancy is sent from the first packet: empty generations stand for packets not yet sent, so
	// the receiver can count them (RFC 4103 4.2).
	gens := make([][]byte, Level)
	tss := make([]uint32, Level)
	copy(gens[Level-len(s.gens):], s.gens)
	copy(tss[Level-len(s.ts):], s.ts)
	for i := range gens {
		off := uint32(0)
		if tss[i] != 0 || gens[i] != nil {
			off = ts - tss[i]
		}
		h := uint32(0x80|s.T140)<<24 | (off&0x3fff)<<10 | uint32(len(gens[i])&0x3ff)
		pkt = binary.BigEndian.AppendUint32(pkt, h)
	}
	pkt = append(pkt, byte(s.T140))
	for _, g := range gens {
		pkt = append(pkt, g...)
	}
	return append(pkt, primary...)
}
