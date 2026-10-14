// Package media moves RTP: it allocates ports, relays packets between call legs, loops them back for the
// echo test, generates tones, and measures quality per stream.
//
// Every call is anchored: both legs send their media to BitVoice and BitVoice forwards it. That costs a
// little latency (well under a millisecond on a LAN) and buys recording, quality measurement, NAT traversal
// without STUN on phones, and the ability to move a leg (transfer, hold) without renegotiating the other.
package media

import (
	"github.com/biodream-llc/bitvoice/internal/srtp"

	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Pool hands out even RTP ports with the following odd port for RTCP.
type Pool struct {
	Host     string // address to bind, "" for all
	Min, Max int

	mu   sync.Mutex
	next int
}

// ErrExhausted means every port in the range is in use.
var ErrExhausted = errors.New("media: RTP port range exhausted")

// Allocate opens an RTP and RTCP socket pair.
func (p *Pool) Allocate() (*Endpoint, error) { return p.AllocateOn("") }

// AllocateOn is Allocate bound to one address (a shared address), or the pool's own when host is "".
func (p *Pool) AllocateOn(host string) (*Endpoint, error) {
	if host == "" {
		host = p.Host
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	lo, hi := p.Min&^1, p.Max
	if lo <= 0 || hi <= lo {
		lo, hi = 10000, 20000
	}
	if p.next < lo || p.next >= hi {
		p.next = lo
	}
	start := p.next
	for {
		port := p.next
		p.next += 2
		if p.next >= hi {
			p.next = lo
		}
		rtp, err := listenMedia(host, port)
		if err == nil {
			rtcp, err2 := listenMedia(host, port+1)
			if err2 == nil {
				_ = rtp.SetReadBuffer(1 << 20)
				return newEndpoint(rtp, rtcp), nil
			}
			rtp.Close()
		}
		if p.next == start {
			return nil, ErrExhausted
		}
	}
}

// Stats are the counters for one direction of one leg, as received from the far end.
type Stats struct {
	Packets   uint64  `json:"packets"`
	Bytes     uint64  `json:"bytes"`
	Lost      uint64  `json:"lost"`
	JitterMS  float64 `json:"jitterMs"`
	LastSeen  int64   `json:"lastSeen"` // unix ms, 0 if nothing yet
	PT        int     `json:"payloadType"`
	Source    string  `json:"source"`
	Latched   bool    `json:"latched"`
	MOS       float64 `json:"mos"`
	ClockRate int     `json:"-"`
}

// Endpoint is one leg's local RTP and RTCP sockets and what is known about the far end.
type Endpoint struct {
	rtp, rtcp *net.UDPConn

	mu       sync.Mutex
	remote   netip.AddrPort // where to send RTP
	remoteRC netip.AddrPort
	latched  bool
	peer     *Endpoint // where received packets go; nil drops them
	echo     bool
	recorder func(pkt []byte)
	sent     func(pkt []byte) // sees each RTP packet sent (radio channel recording)
	stats    Stats
	lastSeq  uint16
	haveSeq  bool
	// Loss as RFC 3550 A.3 counts it: packets expected (highest sequence number seen, with wraps,
	// less the first) less packets received, so a packet arriving late is not lost.
	cycles   uint32
	baseSeq  uint32
	received uint64
	lostBase uint64 // loss counted on earlier streams of this endpoint
	expected uint64
	lastTS   uint32
	lastArr  time.Time
	jitter   float64
	closed   atomic.Bool
	wg       sync.WaitGroup
	started  bool
	onPacket func()
	// raw relays every datagram as it is, with no RTP rules (T.38 fax over UDPTL).
	raw   atomic.Bool
	onRaw func(pkt []byte) // in raw mode, takes datagrams instead of a peer (BitVoice's own fax)
	// wantPT is the payload type this party negotiated (0 PCMU, 8 PCMA, 9 G.722), or -1. When the other
	// side sends a different one of those (a transfer joining two parties who chose differently, or a
	// prompt played to a wideband phone), packets are converted on the way through.
	wantPT int
	xmu    sync.Mutex
	xcode  map[int]*Transcoder // by incoming payload type
	// srtpIn decrypts what this party sends; srtpOut encrypts what is sent to it. Nil: plain RTP.
	srtpIn, srtpOut *srtp.Context
	// ptMap renumbers payload types on the way out to this party (a browser's telephone-event type).
	ptMap map[byte]byte
	// inMap renumbers payload types as they come in from this party (its dynamic Opus number to PTOpus).
	inMap map[byte]byte
	// evPT is this party's telephone-event number (-1 unknown); lastX the transcoder last used, which
	// re-times key presses when the audio is converted.
	evPT  int
	lastX *Transcoder
	// other receives non-RTP datagrams (STUN, DTLS) on WebRTC legs; nil drops them.
	other func(pkt []byte, from netip.AddrPort)
	// tap, when set, sees every audio packet this party sends (a mixing bridge).
	tap func(pkt []byte)
	// cleaner, when set, cleans the audio this party sends (G.711 only) before it goes on.
	cleaner *Cleaner
	// dtmf hears each key this party presses (RFC 4733 events), once per press; lastEv dedupes.
	dtmf   func(key byte)
	lastEv uint32
	haveEv bool
}

// SetTap sets a function that sees every packet this party sends, after clean-up; nil removes it. It
// must not keep the slice.
func (e *Endpoint) SetTap(f func(pkt []byte)) {
	e.mu.Lock()
	e.tap = f
	e.mu.Unlock()
}

// SetCleanup switches noise suppression and level control on or off for the audio this party sends.
func (e *Endpoint) SetCleanup(on bool) {
	e.mu.Lock()
	switch {
	case on && e.cleaner == nil:
		e.cleaner = NewCleaner(Rate)
	case !on:
		e.cleaner = nil
	}
	e.mu.Unlock()
}

// clean rewrites a plain G.711 packet's audio through the cleaner; other packets pass unchanged.
func clean(c *Cleaner, pkt []byte) []byte {
	if len(pkt) <= 12 || pkt[0]&0x3f != 0 {
		return pkt
	}
	pt := pkt[1] & 0x7f
	if pt != PTPCMU && pt != PTPCMA {
		return pkt
	}
	pcm := make([]int16, len(pkt)-12)
	for i, b := range pkt[12:] {
		if pt == PTPCMU {
			pcm[i] = UlawToLinear(b)
		} else {
			pcm[i] = AlawToLinear(b)
		}
	}
	out := append([]byte(nil), pkt...)
	for i, v := range c.Process(pcm) {
		if pt == PTPCMU {
			out[12+i] = LinearToUlaw(v)
		} else {
			out[12+i] = LinearToAlaw(v)
		}
	}
	return out
}

// OnDTMF sets a function told each key this party presses while media flows through; nil stops it.
func (e *Endpoint) OnDTMF(f func(key byte)) {
	e.mu.Lock()
	e.dtmf = f
	e.mu.Unlock()
}

// event reads an RFC 4733 telephone-event packet's key when it ends, or 0.
func (e *Endpoint) event(pkt []byte) byte {
	pt := pkt[1] & 0x7f
	if pt < 96 || pt > 127 || pt == PTOpus {
		return 0 // static types are audio; so is Opus, which has a dynamic number too
	}
	hdr := 12 + 4*int(pkt[0]&0x0f)
	if pkt[0]&0x10 != 0 && len(pkt) >= hdr+4 {
		hdr += 4 + 4*int(binary.BigEndian.Uint16(pkt[hdr+2:]))
	}
	if len(pkt) != hdr+4 || pkt[hdr+1]&0x80 == 0 {
		return 0 // an event is exactly 4 octets, and counts when it ends
	}
	ts := binary.BigEndian.Uint32(pkt[4:8])
	if e.haveEv && e.lastEv == ts {
		return 0 // the end packet is sent three times
	}
	e.haveEv, e.lastEv = true, ts
	return EventKey(pkt[hdr])
}

// SetEventPT records this party's telephone-event payload number, for key presses that arrive
// alongside audio being converted for it.
func (e *Endpoint) SetEventPT(pt int) {
	e.xmu.Lock()
	e.evPT = pt
	e.xmu.Unlock()
}

// MapIncoming makes packets from this party with payload type from read as to: a dynamic number
// becomes the one BitVoice uses inside (PTOpus).
func (e *Endpoint) MapIncoming(from, to int) {
	e.mu.Lock()
	if from == to {
		delete(e.inMap, byte(from))
	} else {
		if e.inMap == nil {
			e.inMap = map[byte]byte{}
		}
		e.inMap[byte(from)] = byte(to)
	}
	e.mu.Unlock()
}

// MapPayloadType makes packets sent to this party with payload type from carry to instead.
func (e *Endpoint) MapPayloadType(from, to int) {
	e.mu.Lock()
	if e.ptMap == nil {
		e.ptMap = map[byte]byte{}
	}
	e.ptMap[byte(from)] = byte(to)
	e.mu.Unlock()
}

// OnOther sets the handler for STUN and DTLS datagrams arriving on the media port (WebRTC legs).
func (e *Endpoint) OnOther(f func(pkt []byte, from netip.AddrPort)) {
	e.mu.Lock()
	e.other = f
	e.mu.Unlock()
}

// WriteTo sends a raw datagram from the media port (STUN and DTLS replies).
func (e *Endpoint) WriteTo(b []byte, to netip.AddrPort) {
	_, _ = e.rtp.WriteToUDPAddrPort(b, to)
}

// Latch fixes where media is sent and accepted from (ICE chose the address).
func (e *Endpoint) Latch(addr netip.AddrPort) {
	e.mu.Lock()
	e.remote, e.latched = addr, true
	e.stats.LastSeen = time.Now().UnixMilli()
	e.mu.Unlock()
}

// SetSRTP switches the party's media to SRTP (nil, nil switches back to plain RTP). Packets that fail
// authentication are dropped before they can move the media latch.
func (e *Endpoint) SetSRTP(in, out *srtp.Context) {
	e.mu.Lock()
	e.srtpIn, e.srtpOut = in, out
	e.mu.Unlock()
}

// Secure reports whether the party's media is encrypted.
func (e *Endpoint) Secure() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.srtpIn != nil
}

func newEndpoint(rtp, rtcp *net.UDPConn) *Endpoint {
	return &Endpoint{rtp: rtp, rtcp: rtcp, wantPT: -1, evPT: -1}
}

// Port is the local RTP port.
func (e *Endpoint) Port() int { return e.rtp.LocalAddr().(*net.UDPAddr).Port }

// SetRemote sets where to send media, from SDP. A later packet from the far end overrides it (latching).
func (e *Endpoint) SetRemote(addr string, port int) {
	ip, err := netip.ParseAddr(addr)
	if err != nil || port == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.latched {
		e.remote = netip.AddrPortFrom(ip.Unmap(), uint16(port))
		e.remoteRC = netip.AddrPortFrom(ip.Unmap(), uint16(port+1))
	}
}

// SetPayloadType records the payload type this party expects (0 PCMU, 8 PCMA, 9 G.722, -1 unknown).
func (e *Endpoint) SetPayloadType(pt int) {
	e.mu.Lock()
	changed := e.wantPT != pt
	e.wantPT = pt
	e.mu.Unlock()
	if changed {
		e.xmu.Lock()
		e.xcode, e.lastX = nil, nil // encoders were for the old codec
		e.xmu.Unlock()
	}
}

// Remote returns where media is being sent.
func (e *Endpoint) Remote() netip.AddrPort {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.remote
}

// Connect makes packets received on e go out of peer's socket to peer's remote, and the reverse. Pass nil
// to stop forwarding (hold, or a leg being replaced).
func Connect(a, b *Endpoint) {
	a.mu.Lock()
	a.peer, a.echo = b, false
	a.mu.Unlock()
	if b != nil {
		b.mu.Lock()
		b.peer, b.echo = a, false
		b.mu.Unlock()
	}
}

// Echo sends every packet back where it came from (the echo test).
func (e *Endpoint) Echo() {
	e.mu.Lock()
	e.echo, e.peer = true, nil
	e.mu.Unlock()
}

// Record sets a function that sees every RTP packet received on this leg. It must not keep the slice.
func (e *Endpoint) Record(f func(pkt []byte)) {
	e.mu.Lock()
	e.recorder = f
	e.mu.Unlock()
}

// RecordSent sets a function that sees every RTP packet sent on this leg, as sent (before SRTP).
// It must not keep the slice.
func (e *Endpoint) RecordSent(f func(pkt []byte)) {
	e.mu.Lock()
	e.sent = f
	e.mu.Unlock()
}

// OnPacket sets a function called (at most once per packet, cheaply) when media arrives; used to detect
// dead calls.
func (e *Endpoint) OnPacket(f func()) {
	e.mu.Lock()
	e.onPacket = f
	e.mu.Unlock()
}

// Start begins reading.
func (e *Endpoint) Start() {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.mu.Unlock()
	e.wg.Add(2)
	go e.readRTP()
	go e.readRTCP()
}

// Close releases the sockets.
func (e *Endpoint) Close() {
	if e.closed.Swap(true) {
		return
	}
	e.rtp.Close()
	e.rtcp.Close()
	e.wg.Wait()
}

// Stats returns a copy of the receive statistics.
func (e *Endpoint) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.stats
	s.JitterMS = e.jitterMS()
	s.Latched = e.latched
	if e.remote.IsValid() {
		s.Source = e.remote.String()
	}
	s.MOS = mos(s)
	return s
}

func (e *Endpoint) jitterMS() float64 {
	rate := e.stats.ClockRate
	if rate == 0 {
		rate = 8000
	}
	return e.jitter / float64(rate) * 1000
}

// mos estimates a listening MOS from loss and jitter with a simplified E-model (ITU-T G.107) for G.711.
func mos(s Stats) float64 {
	if s.Packets == 0 {
		return 0
	}
	lossPct := 0.0
	if total := s.Packets + s.Lost; total > 0 {
		lossPct = float64(s.Lost) / float64(total) * 100
	}
	delay := 20 + 2*s.JitterMS + 10 // packetisation, jitter buffer, network
	id := 0.024*delay + 0.11*(delay-177.3)*boolf(delay > 177.3)
	ie := 0 + 30*lossPct/(lossPct+10) // G.711 with packet loss concealment, Bpl about 10
	r := 93.2 - id - ie
	if r < 0 {
		return 1
	}
	m := 1 + 0.035*r + r*(r-60)*(100-r)*7e-6
	if m > 4.5 {
		m = 4.5
	}
	return float64(int(m*100)) / 100
}

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (e *Endpoint) readRTP() {
	defer e.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := e.rtp.ReadFromUDPAddrPort(buf)
		if err != nil {
			if e.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if e.raw.Load() {
			now := time.Now()
			e.mu.Lock()
			e.latch(from, now)
			if from != e.remote {
				e.mu.Unlock()
				continue
			}
			e.stats.Packets++
			e.stats.Bytes += uint64(n)
			e.stats.LastSeen = now.UnixMilli()
			peer, h := e.peer, e.onRaw
			e.mu.Unlock()
			switch {
			case h != nil:
				h(append([]byte(nil), buf[:n]...))
			case peer != nil:
				peer.sendRaw(append([]byte(nil), buf[:n]...))
			}
			continue
		}
		e.mu.Lock()
		other := e.other
		e.mu.Unlock()
		if other != nil && n > 0 && (buf[0] < 4 || buf[0] >= 20 && buf[0] < 64) {
			// STUN (0-3) and DTLS (20-63) share the port with RTP on WebRTC legs (RFC 7983).
			other(append([]byte(nil), buf[:n]...), from)
			continue
		}
		if n < 12 || buf[0]>>6 != 2 {
			continue // not RTP version 2
		}
		if other != nil && buf[1] >= 192 && buf[1] <= 223 {
			continue // RTCP multiplexed on the RTP port (RFC 5761): not relayed on WebRTC legs
		}
		pkt := buf[:n]
		e.mu.Lock()
		in := e.srtpIn
		e.mu.Unlock()
		if in != nil {
			plain, err := in.Unprotect(pkt)
			if err != nil {
				continue // forged, replayed or keyed for someone else
			}
			pkt = plain
		}
		now := time.Now()
		e.mu.Lock()
		if np, ok := e.inMap[pkt[1]&0x7f]; ok {
			if in == nil {
				pkt = append([]byte(nil), pkt...) // buf is reused for the next read
			}
			pkt[1] = pkt[1]&0x80 | np
		}
		e.latch(from, now)
		if from != e.remote {
			// Media from somewhere other than the latched source is dropped (RTP injection).
			e.mu.Unlock()
			continue
		}
		e.account(pkt, now)
		peer, echo, rec, on := e.peer, e.echo, e.recorder, e.onPacket
		var key byte
		if e.dtmf != nil {
			key = e.event(pkt)
		}
		dtmf, cl, tap := e.dtmf, e.cleaner, e.tap
		e.mu.Unlock()
		if cl != nil && !echo {
			pkt = clean(cl, pkt) // recorded and forwarded clean
		}
		if tap != nil {
			tap(pkt)
		}
		if key != 0 {
			dtmf(key)
		}
		if rec != nil {
			rec(pkt)
		}
		if on != nil {
			on()
		}
		switch {
		case echo:
			e.send(append([]byte(nil), pkt...))
		case peer != nil:
			peer.send(pkt)
		}
	}
}

// latch adopts the source of incoming media as the far end. The first source wins; a different source is
// adopted only after the current one has been silent for two seconds (a phone that changed network).
func (e *Endpoint) latch(from netip.AddrPort, now time.Time) {
	if from == e.remote && e.latched {
		return
	}
	silent := e.stats.LastSeen == 0 || now.Sub(time.UnixMilli(e.stats.LastSeen)) > 2*time.Second
	if !e.latched || silent {
		e.remote = from
		e.remoteRC = netip.AddrPortFrom(from.Addr(), from.Port()+1)
		e.latched = true
		e.haveSeq = false
	}
}

func (e *Endpoint) account(pkt []byte, now time.Time) {
	st := &e.stats
	st.Packets++
	st.Bytes += uint64(len(pkt))
	st.LastSeen = now.UnixMilli()
	pt := int(pkt[1] & 0x7f)
	if pt < 72 || pt > 76 { // ignore payload types that collide with RTCP
		if pt != 101 && pt != 13 {
			st.PT = pt
		}
	}
	if st.ClockRate = 8000; st.PT == PTOpus {
		st.ClockRate = 48000
	}
	seq := binary.BigEndian.Uint16(pkt[2:4])
	ts := binary.BigEndian.Uint32(pkt[4:8])
	restart := func() { // a new stream: a new source, or a jump in numbering
		e.lastSeq, e.cycles, e.baseSeq, e.received, e.lostBase = seq, 0, uint32(seq), 0, st.Lost
	}
	if e.haveSeq {
		switch gap := seq - e.lastSeq; {
		case gap > 0 && gap < 3000: // in order, perhaps after a gap
			if seq < e.lastSeq {
				e.cycles += 1 << 16
			}
			e.lastSeq = seq
		case gap >= 3000 && gap <= 65535-3000:
			restart()
		}
		// Otherwise a late or repeated packet: received, nothing more.
		// RFC 3550 A.8 interarrival jitter, in timestamp units.
		arrival := now.Sub(e.lastArr).Seconds() * float64(st.ClockRate)
		d := arrival - float64(int32(ts-e.lastTS))
		if d < 0 {
			d = -d
		}
		e.jitter += (d - e.jitter) / 16
	} else {
		restart()
		e.haveSeq = true
	}
	e.received++
	if expected := uint64(e.cycles+uint32(e.lastSeq)-e.baseSeq) + 1; expected > e.received {
		st.Lost = e.lostBase + expected - e.received
	} else {
		st.Lost = e.lostBase
	}
	e.lastTS = ts
	e.lastArr = now
}

func (e *Endpoint) send(pkt []byte) {
	e.mu.Lock()
	to, want, out, pmap, sent := e.remote, e.wantPT, e.srtpOut, e.ptMap, e.sent
	e.mu.Unlock()

	if Transcodable(want) && len(pkt) >= 12 {
		if pt := int(pkt[1] & 0x7f); Transcodable(pt) && pt != want {
			e.xmu.Lock()
			x := e.xcode[pt]
			if x == nil {
				if e.xcode == nil {
					e.xcode = map[int]*Transcoder{}
				}
				x = NewTranscoder(pt, want)
				e.xcode[pt] = x
			}
			e.lastX = x
			pkt = x.Convert(pkt)
			e.xmu.Unlock()
			if pkt == nil {
				return // buffered until the codec has a whole frame
			}
		} else if len(pkt) == 16 && pt >= 96 && pt != PTOpus {
			// A key press (telephone-event) alongside converted audio: re-timed for this party's clock
			// and sent under its own number.
			e.xmu.Lock()
			if x := e.lastX; x != nil {
				pkt = x.Event(pkt)
				if ev := e.evPT; ev >= 0 {
					pkt[1] = pkt[1]&0x80 | byte(ev)
				}
			}
			e.xmu.Unlock()
		}
	}
	if len(pkt) >= 2 && pmap != nil {
		if np, ok := pmap[pkt[1]&0x7f]; ok {
			pkt = append([]byte(nil), pkt...)
			pkt[1] = pkt[1]&0x80 | np
		}
	}
	if sent != nil {
		sent(pkt) // as sent: converted and renumbered, before SRTP
	}
	if !to.IsValid() {
		return
	}
	if out != nil {
		sp, err := out.Protect(pkt)
		if err != nil {
			return
		}
		pkt = sp
	}
	_, _ = e.rtp.WriteToUDPAddrPort(pkt, to)
}

// SetRaw makes the endpoint relay datagrams as they are, without RTP rules: T.38 fax (UDPTL).
func (e *Endpoint) SetRaw() { e.raw.Store(true) }

// sendRaw sends a datagram as it is.
func (e *Endpoint) sendRaw(pkt []byte) {
	e.mu.Lock()
	to := e.remote
	e.mu.Unlock()
	if to.IsValid() {
		_, _ = e.rtp.WriteToUDPAddrPort(pkt, to)
	}
}

// OnRaw takes the datagrams a raw endpoint receives (BitVoice's own fax machine).
func (e *Endpoint) OnRaw(f func(pkt []byte)) {
	e.mu.Lock()
	e.onRaw = f
	e.mu.Unlock()
}

// SendRaw sends a datagram as it is to the far end (BitVoice's own fax).
func (e *Endpoint) SendRaw(pkt []byte) { e.sendRaw(pkt) }

// Wideband says whether this party's codec carries 16 kHz audio (G.722, Opus).
func (e *Endpoint) Wideband() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return IsWideband(e.wantPT)
}

// SendRTP sends a locally generated packet to the far end (tones, prompts).
func (e *Endpoint) SendRTP(pkt []byte) { e.send(pkt) }

func (e *Endpoint) readRTCP() {
	defer e.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := e.rtcp.ReadFromUDPAddrPort(buf)
		if err != nil {
			if e.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		pkt := buf[:n]
		e.mu.Lock()
		in := e.srtpIn
		e.mu.Unlock()
		if in != nil {
			plain, err := in.UnprotectRTCP(pkt)
			if err != nil {
				continue
			}
			pkt = plain
		}
		e.mu.Lock()
		e.remoteRC = from
		peer := e.peer
		e.mu.Unlock()
		if peer != nil {
			peer.mu.Lock()
			to, out := peer.remoteRC, peer.srtpOut
			peer.mu.Unlock()
			if out != nil {
				sp, err := out.ProtectRTCP(pkt)
				if err != nil {
					continue
				}
				pkt = sp
			}
			if to.IsValid() {
				_, _ = peer.rtcp.WriteToUDPAddrPort(pkt, to)
			}
		}
	}
}

// String describes the endpoint for logs.
func (e *Endpoint) String() string { return fmt.Sprintf("rtp:%d->%s", e.Port(), e.Remote()) }

var ulawToAlaw, alawToUlaw [256]byte

func init() {
	for i := 0; i < 256; i++ {
		ulawToAlaw[i] = LinearToAlaw(UlawToLinear(byte(i)))
		alawToUlaw[i] = LinearToUlaw(AlawToLinear(byte(i)))
	}
}

// transcodeG711 converts a PCMU packet to PCMA or the reverse, returning a new packet.
func transcodeG711(pkt []byte, from, to int) []byte {
	out := make([]byte, len(pkt))
	copy(out, pkt)
	hdr := 12 + 4*int(pkt[0]&0x0f)
	if pkt[0]&0x10 != 0 && len(pkt) >= hdr+4 { // header extension
		hdr += 4 + 4*int(binary.BigEndian.Uint16(pkt[hdr+2:]))
	}
	if hdr > len(pkt) {
		return pkt
	}
	table := &ulawToAlaw
	if from == 8 {
		table = &alawToUlaw
	}
	for i := hdr; i < len(out); i++ {
		out[i] = table[out[i]]
	}
	out[1] = out[1]&0x80 | byte(to)
	return out
}

// ListenAt opens an endpoint on a fixed port (a radio gateway sends to a port set on it), with
// RTCP on the next one.
func ListenAt(host string, port int) (*Endpoint, error) {
	rtp, err := listenMedia(host, port)
	if err != nil {
		return nil, err
	}
	rtcp, err := listenMedia(host, port+1)
	if err != nil {
		rtp.Close()
		return nil, err
	}
	_ = rtp.SetReadBuffer(1 << 20)
	return newEndpoint(rtp, rtcp), nil
}
