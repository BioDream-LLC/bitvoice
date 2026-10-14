// Package audiosocket speaks Asterisk's AudioSocket protocol: a TCP stream of frames (one type byte, a
// two-byte big-endian length, the payload), opened by the phone system with the call's UUID, then
// 8 kHz 16-bit little-endian signed linear audio both ways, key presses, and a hang-up. Voice bots
// and speech services written for Asterisk's AudioSocket work with BitVoice unchanged.
package audiosocket

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Frame types.
const (
	KindHangup = 0x00
	KindUUID   = 0x01
	KindDTMF   = 0x03
	KindAudio  = 0x10
	KindError  = 0xFF
)

// Frame is one message.
type Frame struct {
	Kind    byte
	Payload []byte
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (Frame, error) {
	var h [3]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	n := int(binary.BigEndian.Uint16(h[1:]))
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return Frame{}, err
	}
	return Frame{Kind: h[0], Payload: p}, nil
}

// AppendFrame writes one frame onto b.
func AppendFrame(b []byte, kind byte, payload []byte) []byte {
	b = append(b, kind)
	b = binary.BigEndian.AppendUint16(b, uint16(len(payload)))
	return append(b, payload...)
}

// UUID turns a call ID into the 16 bytes AudioSocket opens with (zero-padded, or the first 16 bytes of
// a longer ID's hex), and its text form.
func UUID(id string) ([16]byte, string) {
	var u [16]byte
	copy(u[:], []byte(id))
	if b, err := hexDecode(id); err == nil && len(b) > 0 {
		u = [16]byte{}
		copy(u[:], b)
	}
	const hx = "0123456789abcdef"
	s := make([]byte, 0, 36)
	for i, x := range u {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			s = append(s, '-')
		}
		s = append(s, hx[x>>4], hx[x&15])
	}
	return u, string(s)
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("odd")
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		a, b := unhex(s[2*i]), unhex(s[2*i+1])
		if a < 0 || b < 0 {
			return nil, errors.New("not hex")
		}
		out[i] = byte(a<<4 | b)
	}
	return out, nil
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// Ended says how a stream ended.
type Ended int

const (
	// ByBot: the bot sent a hang-up frame: its part of the call is done.
	ByBot Ended = iota
	// Failed: the connection could not be made, broke, or the bot sent an error.
	Failed
	// Stopped: BitVoice closed it (the caller hung up).
	Stopped
)

// Pipe joins an RTP endpoint to an AudioSocket connection: what the endpoint receives goes to the bot
// as linear audio and key presses, and what the bot sends is paced out in 20 ms packets of G.711 µ-law.
// The endpoint is given to Pipe as one end of a pair: Pipe owns the other end's socket.
type Pipe struct {
	conn net.Conn
	udp  *net.UDPConn
	ep   *media.Endpoint // the endpoint the call's audio is connected to

	mu     sync.Mutex
	out    []int16 // bot audio waiting to be played
	wmu    sync.Mutex
	done   chan struct{}
	once   sync.Once
	ended  Ended
	reason string
	// LastKey is when the last key press was passed on (keys come in several packets).
	lastKey  byte
	lastTS   uint32
	dec      media.Decoders
	seq      uint16
	ts       uint32
	ssrc     uint32
	maxQueue int
}

// Dial connects to a bot and starts a pipe for ep: ep is set to send µ-law to the pipe's socket.
func Dial(ctx context.Context, addr, callID string, ep *media.Endpoint) (*Pipe, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return Start(c, callID, ep)
}

// Start runs a pipe on an open connection (Dial, or a test's).
func Start(c net.Conn, callID string, ep *media.Endpoint) (*Pipe, error) {
	u, _ := UUID(callID)
	if _, err := c.Write(AppendFrame(nil, KindUUID, u[:])); err != nil {
		c.Close()
		return nil, err
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		c.Close()
		return nil, err
	}
	p := &Pipe{conn: c, udp: udp, ep: ep, done: make(chan struct{}), ssrc: uint32(time.Now().UnixNano()), maxQueue: 8000 * 120}
	ep.SetPayloadType(0)
	ep.SetRemote("127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port)
	go p.fromBot()
	go p.fromCall()
	go p.pace()
	return p, nil
}

// Done is closed when the stream ends; Ended then says how.
func (p *Pipe) Done() <-chan struct{} { return p.done }

// Ended says how the stream ended, and why when it failed.
func (p *Pipe) Ended() (Ended, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ended, p.reason
}

func (p *Pipe) finish(e Ended, why string) {
	p.once.Do(func() {
		p.mu.Lock()
		p.ended, p.reason = e, why
		p.mu.Unlock()
		close(p.done)
		p.conn.Close()
		p.udp.Close()
	})
}

// Close ends the stream from BitVoice's side, telling the bot the call has hung up.
func (p *Pipe) Close() {
	p.wmu.Lock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = p.conn.Write(AppendFrame(nil, KindHangup, nil))
	p.wmu.Unlock()
	p.finish(Stopped, "")
}

func (p *Pipe) write(kind byte, payload []byte) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := p.conn.Write(AppendFrame(nil, kind, payload)); err != nil {
		go p.finish(Failed, "the bot stopped taking audio: "+err.Error())
	}
}

// fromBot reads the bot's frames: audio is queued to play, a hang-up or error ends the stream.
func (p *Pipe) fromBot() {
	r := bufio.NewReader(p.conn)
	for {
		_ = p.conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		f, err := ReadFrame(r)
		if err != nil {
			select {
			case <-p.done:
			default:
				p.finish(Failed, "the connection to the bot ended without a hang-up")
			}
			return
		}
		switch f.Kind {
		case KindHangup:
			p.finish(ByBot, "")
			return
		case KindError:
			p.finish(Failed, "the bot sent an error")
			return
		case KindAudio:
			n := len(f.Payload) / 2
			p.mu.Lock()
			if len(p.out)+n <= p.maxQueue {
				for i := range n {
					p.out = append(p.out, int16(binary.LittleEndian.Uint16(f.Payload[2*i:])))
				}
			}
			p.mu.Unlock()
		}
	}
}

// pace plays the bot's queued audio to the call, 160 samples every 20 ms.
func (p *Pipe) pace() {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	pkt := make([]byte, 12+160)
	to := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(p.ep.Port()))
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
		}
		p.mu.Lock()
		if len(p.out) < 160 {
			p.mu.Unlock()
			p.ts += 160
			continue
		}
		frame := p.out[:160]
		p.out = p.out[160:]
		p.mu.Unlock()
		pkt[0], pkt[1] = 0x80, 0
		p.seq++
		p.ts += 160
		binary.BigEndian.PutUint16(pkt[2:], p.seq)
		binary.BigEndian.PutUint32(pkt[4:], p.ts)
		binary.BigEndian.PutUint32(pkt[8:], p.ssrc)
		for i, s := range frame {
			pkt[12+i] = media.LinearToUlaw(s)
		}
		_, _ = p.udp.WriteToUDPAddrPort(pkt, to)
	}
}

// fromCall reads what the call sends: audio to the bot as linear, key presses as DTMF frames.
func (p *Pipe) fromCall() {
	buf := make([]byte, 2048)
	var pending []int16
	for {
		n, err := p.udp.Read(buf)
		if err != nil {
			return
		}
		if n < 12 || buf[0]>>6 != 2 {
			continue
		}
		hl := 12 + 4*int(buf[0]&0x0F)
		if buf[0]&0x10 != 0 && n >= hl+4 {
			hl += 4 + 4*int(binary.BigEndian.Uint16(buf[hl+2:]))
		}
		if hl > n {
			continue
		}
		pt := int(buf[1] & 0x7F)
		payload := buf[hl:n]
		if pt >= 96 && len(payload) == 4 {
			// telephone-event (RFC 4733): passed on once per key, when it starts.
			ts := binary.BigEndian.Uint32(buf[4:])
			if k := keyOf(payload[0]); k != 0 && (k != p.lastKey || ts != p.lastTS) {
				p.lastKey, p.lastTS = k, ts
				p.write(KindDTMF, []byte{k})
			}
			continue
		}
		pcm := p.dec.Narrowband(pt, payload)
		if pcm == nil {
			continue
		}
		pending = append(pending, pcm...)
		for len(pending) >= 160 {
			out := make([]byte, 320)
			for i := range 160 {
				binary.LittleEndian.PutUint16(out[2*i:], uint16(pending[i]))
			}
			pending = pending[160:]
			p.write(KindAudio, out)
		}
	}
}

func keyOf(ev byte) byte {
	switch {
	case ev <= 9:
		return '0' + ev
	case ev == 10:
		return '*'
	case ev == 11:
		return '#'
	case ev >= 12 && ev <= 15:
		return 'A' + ev - 12
	}
	return 0
}
