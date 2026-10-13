package iax

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Node is one IAX2 endpoint: a UDP port and the calls on it.
type Node struct {
	conn *net.UDPConn
	Log  *slog.Logger
	h    atomic.Pointer[Handlers]

	mu     sync.Mutex
	calls  map[uint16]*Call // by our call number
	remote map[remoteKey]*Call
	next   uint16
	ctx    context.Context
	cancel context.CancelFunc
}

// Handlers are what a node does with what arrives unasked.
type Handlers struct {
	// OnNew receives each new incoming call; it must Accept, ask to Authenticate, or Reject it. It is
	// called on the node's reading goroutine: start the work elsewhere.
	OnNew func(c *Call, ies IEs)
	// OnRegister, when set, lets peers register to this node: it gives the secret for a username
	// (false: unknown), and Registered hears each registration that succeeds.
	OnRegister func(username string) (secret string, ok bool)
	Registered func(username string, from netip.AddrPort, refresh int)
}

// Handle sets the handlers (safe while the node runs).
func (n *Node) Handle(h Handlers) { n.h.Store(&h) }

func (n *Node) handlers() Handlers {
	if h := n.h.Load(); h != nil {
		return *h
	}
	return Handlers{}
}

type remoteKey struct {
	addr netip.AddrPort
	no   uint16
}

// Event is something that happened on a call.
type Event struct {
	Kind   string // accepted, ringing, answered, voice, dtmf, hangup, authreq
	Format uint32 // accepted, voice: the audio format
	Audio  []byte // voice
	Digit  byte   // dtmf
	Cause  string // hangup
	Code   int    // hangup: the cause code (16 normal, 17 busy, 21 rejected, 34 congestion)
}

// Call is one call (or registration) and its reliable delivery of full frames.
type Call struct {
	n      *Node
	local  uint16
	remote uint16
	peer   netip.AddrPort
	start  time.Time
	// Events delivers what happens on the call, in order; it is closed when the call ends.
	Events chan Event

	closeOnce sync.Once
	mu        sync.Mutex
	oseq      uint8
	iseq      uint8
	pending   []*sent
	ended     bool
	inFormat  uint32 // the format of audio coming in (set by the last full voice frame)
	outFormat uint32
	lastTS    uint32
	voiceHigh uint32 // the high bits of the timestamp sent in the last full voice frame
	heard     time.Time
	username  string
	secret    string
	challenge string
	newIEs    IEs // the NEW sent, to send again with a call token
}

type sent struct {
	f     *Frame
	b     []byte
	at    time.Time
	tries int
}

// Listen opens an IAX2 node on addr (":4569").
func Listen(ctx context.Context, addr string, log *slog.Logger) (*Node, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	n := &Node{conn: conn, Log: log, calls: map[uint16]*Call{}, remote: map[remoteKey]*Call{}}
	n.ctx, n.cancel = context.WithCancel(ctx)
	go n.read()
	go n.timers()
	return n, nil
}

// Port is the UDP port the node listens on.
func (n *Node) Port() int { return n.conn.LocalAddr().(*net.UDPAddr).Port }

// Close hangs up every call and closes the port.
func (n *Node) Close() {
	n.mu.Lock()
	calls := make([]*Call, 0, len(n.calls))
	for _, c := range n.calls {
		calls = append(calls, c)
	}
	n.mu.Unlock()
	for _, c := range calls {
		c.Hangup("shutting down", 16)
	}
	time.Sleep(50 * time.Millisecond)
	n.cancel()
	n.conn.Close()
}

func (n *Node) newCall(peer netip.AddrPort, remote uint16) *Call {
	n.mu.Lock()
	defer n.mu.Unlock()
	for {
		n.next++
		if n.next == 0 || n.next > 0x7FFF {
			n.next = 1
		}
		if _, used := n.calls[n.next]; !used {
			break
		}
	}
	c := &Call{n: n, local: n.next, remote: remote, peer: peer, start: time.Now(), Events: make(chan Event, 64), heard: time.Now()}
	n.calls[c.local] = c
	if remote != 0 {
		n.remote[remoteKey{peer, remote}] = c
	}
	return c
}

func (n *Node) forget(c *Call) {
	n.mu.Lock()
	delete(n.calls, c.local)
	delete(n.remote, remoteKey{c.peer, c.remote})
	n.mu.Unlock()
}

func (n *Node) write(b []byte, to netip.AddrPort) {
	_, _ = n.conn.WriteToUDPAddrPort(b, to)
}

func (n *Node) read() {
	buf := make([]byte, 4096)
	for {
		sz, from, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if n.ctx.Err() != nil {
				return
			}
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		p, err := Parse(buf[:sz])
		if err != nil {
			continue
		}
		if p.Full == nil {
			n.mu.Lock()
			c := n.remote[remoteKey{from, p.MiniSrc}]
			n.mu.Unlock()
			if c != nil {
				c.mu.Lock()
				f := c.inFormat
				c.heard = time.Now()
				c.mu.Unlock()
				if f != 0 {
					c.emit(Event{Kind: "voice", Format: f, Audio: append([]byte(nil), p.Audio...)})
				}
			}
			continue
		}
		n.receive(p.Full, from)
	}
}

func (n *Node) receive(f *Frame, from netip.AddrPort) {
	var c *Call
	n.mu.Lock()
	if f.Dst != 0 {
		c = n.calls[f.Dst]
	} else {
		c = n.remote[remoteKey{from, f.Src}]
	}
	n.mu.Unlock()
	if c == nil {
		if f.Dst != 0 || f.Type != TypeIAX {
			return
		}
		switch f.Subclass {
		case IaxNew, IaxRegReq:
			c = n.newCall(from, f.Src)
			c.handle(f)
		case IaxPoke:
			// A qualify: answer PONG from a call number of our own, keep it a moment for the far end's
			// acknowledgement, then forget it.
			c = n.newCall(from, f.Src)
			c.mu.Lock()
			c.iseq = f.OSeq + 1
			c.mu.Unlock()
			c.send(&Frame{Type: TypeIAX, Subclass: IaxPong, TS: f.TS})
			c.end()
		}
		return
	}
	if c.remote == 0 && f.Src != 0 {
		c.mu.Lock()
		c.remote = f.Src
		c.mu.Unlock()
		n.mu.Lock()
		n.remote[remoteKey{from, f.Src}] = c
		n.mu.Unlock()
	}
	c.handle(f)
}

// timers resends unacknowledged frames, and ends calls the far end has gone quiet on.
func (n *Node) timers() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
		case <-ping.C:
			n.mu.Lock()
			for _, c := range n.calls {
				go c.send(&Frame{Type: TypeIAX, Subclass: IaxPing})
			}
			n.mu.Unlock()
			continue
		}
		n.mu.Lock()
		calls := make([]*Call, 0, len(n.calls))
		for _, c := range n.calls {
			calls = append(calls, c)
		}
		n.mu.Unlock()
		for _, c := range calls {
			c.resend()
		}
	}
}

func (c *Call) ts() uint32 {
	t := uint32(time.Since(c.start).Milliseconds())
	if t <= c.lastTS {
		t = c.lastTS + 1
	}
	c.lastTS = t
	return t
}

// send sends a full frame reliably (it is resent until acknowledged).
func (c *Call) send(f *Frame) {
	c.mu.Lock()
	if c.ended && !(f.Type == TypeIAX && (f.Subclass == IaxHangup || f.Subclass == IaxReject)) {
		c.mu.Unlock()
		return
	}
	f.Src, f.Dst = c.local, c.remote
	if f.TS == 0 {
		f.TS = c.ts()
	}
	f.OSeq, f.ISeq = c.oseq, c.iseq
	if f.counted() {
		c.oseq++
	}
	b := f.Marshal()
	if f.reliable() {
		c.pending = append(c.pending, &sent{f: f, b: b, at: time.Now()})
	}
	peer := c.peer
	c.mu.Unlock()
	c.n.write(b, peer)
}

func (c *Call) sendUnreliable(f *Frame) {
	f.Src, f.Dst = c.local, c.remote
	c.n.write(f.Marshal(), c.peer)
}

func (c *Call) ack(f *Frame) {
	c.mu.Lock()
	a := &Frame{Src: c.local, Dst: c.remote, TS: f.TS, OSeq: c.oseq, ISeq: c.iseq, Type: TypeIAX, Subclass: IaxAck}
	peer := c.peer
	c.mu.Unlock()
	c.n.write(a.Marshal(), peer)
}

// resend sends again what has waited too long for an acknowledgement; after five tries the call is lost.
func (c *Call) resend() {
	c.mu.Lock()
	now := time.Now()
	lost := false
	var out [][]byte
	for _, s := range c.pending {
		wait := time.Duration(500<<min(s.tries, 3)) * time.Millisecond
		if now.Sub(s.at) < wait {
			continue
		}
		if s.tries >= 5 {
			lost = true
			break
		}
		s.tries++
		s.at = now
		s.f.Retrans = true
		s.f.ISeq = c.iseq
		s.b = s.f.Marshal()
		out = append(out, s.b)
	}
	quiet := now.Sub(c.heard) > 60*time.Second
	ended := c.ended
	peer := c.peer
	if ended && len(c.pending) == 0 {
		lost = true // nothing more to deliver
	}
	c.mu.Unlock()
	for _, b := range out {
		c.n.write(b, peer)
	}
	if lost || (quiet && !ended) {
		c.finish("the other end stopped answering", 41)
	}
}

// handle processes a full frame from the far end.
func (c *Call) handle(f *Frame) {
	c.mu.Lock()
	c.heard = time.Now()
	// The far end has every frame of ours below its ISeq.
	kept := c.pending[:0]
	for _, s := range c.pending {
		if int8(s.f.OSeq-f.ISeq) >= 0 {
			kept = append(kept, s)
		}
	}
	c.pending = kept
	if f.Type == TypeIAX && f.Subclass == IaxAck {
		ended := c.ended && len(c.pending) == 0
		c.mu.Unlock()
		if ended {
			c.n.forget(c)
		}
		return
	}
	if f.counted() {
		switch d := int8(f.OSeq - c.iseq); {
		case d < 0: // a frame we already have, sent again: acknowledge again, do nothing
			c.mu.Unlock()
			c.ack(f)
			return
		case d > 0: // a gap: wait for the missing one to be resent
			c.mu.Unlock()
			return
		}
		c.iseq++
	}
	c.mu.Unlock()
	if f.reliable() {
		c.ack(f)
	}
	switch f.Type {
	case TypeVoice:
		c.mu.Lock()
		c.inFormat = f.Subclass
		c.mu.Unlock()
		c.emit(Event{Kind: "voice", Format: f.Subclass, Audio: f.Data})
	case TypeDTMFEnd:
		c.emit(Event{Kind: "dtmf", Digit: byte(f.Subclass)})
	case TypeControl:
		switch f.Subclass {
		case CtlRinging, CtlProceeding:
			c.emit(Event{Kind: "ringing"})
		case CtlAnswer:
			c.emit(Event{Kind: "answered"})
		case CtlBusy:
			c.finish("busy", 17)
		case CtlCongestion:
			c.finish("congestion", 34)
		case CtlHangup:
			c.finish("hung up", 16)
		}
	case TypeIAX:
		c.handleIAX(f)
	}
}

func (c *Call) handleIAX(f *Frame) {
	switch f.Subclass {
	case IaxNew:
		if h := c.n.handlers(); h.OnNew != nil {
			c.mu.Lock()
			c.username = f.IEs.Str(IEUsername)
			c.mu.Unlock()
			h.OnNew(c, f.IEs)
		} else {
			c.Reject("no calls taken here", 21)
		}
	case IaxRegReq:
		c.registerRequest(f)
	case IaxPing:
		c.send(&Frame{Type: TypeIAX, Subclass: IaxPong, TS: f.TS})
	case IaxLagRq:
		c.send(&Frame{Type: TypeIAX, Subclass: IaxLagRp, TS: f.TS})
	case IaxAccept:
		format, _ := f.IEs.U32(IEFormat)
		c.mu.Lock()
		c.outFormat = format
		c.mu.Unlock()
		c.emit(Event{Kind: "accepted", Format: format})
	case IaxAuthReq, IaxRegAuth:
		c.mu.Lock()
		secret := c.secret
		c.mu.Unlock()
		methods, _ := f.IEs.U16(IEAuthMethods)
		if methods&AuthMD5 == 0 || secret == "" {
			c.finish("the other end asks for a sign-in BitVoice cannot give", 21)
			return
		}
		sum := md5.Sum([]byte(f.IEs.Str(IEChallenge) + secret))
		res := hex.EncodeToString(sum[:])
		if f.Subclass == IaxAuthReq {
			c.send(&Frame{Type: TypeIAX, Subclass: IaxAuthRep, IEs: IEs{}.SetStr(IEMD5Result, res)})
		} else {
			c.mu.Lock()
			ies := IEs{}
			for k, v := range c.newIEs {
				ies[k] = v
			}
			c.mu.Unlock()
			ies.SetStr(IEMD5Result, res)
			c.send(&Frame{Type: TypeIAX, Subclass: IaxRegReq, IEs: ies})
		}
	case IaxAuthRep:
		c.mu.Lock()
		want := md5.Sum([]byte(c.challenge + c.secret))
		c.mu.Unlock()
		if f.IEs.Str(IEMD5Result) != hex.EncodeToString(want[:]) {
			c.Reject("wrong password", 21)
			c.emit(Event{Kind: "hangup", Cause: "wrong password", Code: 21})
			return
		}
		c.emit(Event{Kind: "authenticated"})
	case IaxCallToken:
		// The far end wants its token in our request: send the request again with it, as a new call.
		c.mu.Lock()
		ies := IEs{}
		for k, v := range c.newIEs {
			ies[k] = v
		}
		ies[IECallToken] = f.IEs[IECallToken]
		c.newIEs = ies
		c.pending = nil
		c.oseq, c.iseq = 0, 0
		reg := ies[IERefresh] != nil
		// The token's frame came from a call number the far end does not keep: the request goes again
		// as a new one, to no call number.
		old := c.remote
		c.remote = 0
		c.mu.Unlock()
		c.n.mu.Lock()
		delete(c.n.remote, remoteKey{c.peer, old})
		c.n.mu.Unlock()
		sub := uint32(IaxNew)
		if reg {
			sub = IaxRegReq
		}
		c.send(&Frame{Type: TypeIAX, Subclass: sub, IEs: ies})
	case IaxRegAck:
		refresh, _ := f.IEs.U16(IERefresh)
		c.emit(Event{Kind: "registered", Code: int(refresh)})
		c.end()
	case IaxRegRej:
		c.emit(Event{Kind: "hangup", Cause: "registration refused: " + f.IEs.Str(IECause), Code: 21})
		c.end()
	case IaxHangup, IaxReject:
		cause := f.IEs.Str(IECause)
		code := 16
		if v := f.IEs[IECauseCode]; len(v) == 1 {
			code = int(v[0])
		}
		if cause == "" {
			cause = "hung up"
		}
		c.finish(cause, code)
	case IaxInval:
		c.finish("the other end does not know this call", 41)
	}
}

func (c *Call) emit(e Event) {
	c.mu.Lock()
	ended := c.ended
	c.mu.Unlock()
	if ended && e.Kind != "hangup" {
		return
	}
	defer func() { _ = recover() }() // Events may be closed by a concurrent finish
	select {
	case c.Events <- e:
	default:
		if e.Kind != "voice" {
			c.Events <- e
		}
	}
}

// finish ends the call from the far end's side (or a failure): tell the owner once, and forget it.
func (c *Call) finish(cause string, code int) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.pending = nil
	c.mu.Unlock()
	c.emit2(Event{Kind: "hangup", Cause: cause, Code: code})
	c.closeEvents()
	// Kept a little longer, to acknowledge what the far end still sends (its own HANGUP after a BUSY).
	go func() {
		time.Sleep(8 * time.Second)
		c.n.forget(c)
	}()
}

func (c *Call) closeEvents() { c.closeOnce.Do(func() { close(c.Events) }) }

// registerRequest answers a peer registering here: a challenge first, then the check of its answer.
func (c *Call) registerRequest(f *Frame) {
	user := f.IEs.Str(IEUsername)
	h := c.n.handlers()
	if h.OnRegister == nil {
		c.send(&Frame{Type: TypeIAX, Subclass: IaxRegRej, IEs: IEs{}.SetStr(IECause, "registration is not taken here")})
		c.end()
		return
	}
	secret, ok := h.OnRegister(user)
	res := f.IEs.Str(IEMD5Result)
	c.mu.Lock()
	ch := c.challenge
	c.mu.Unlock()
	if res == "" || ch == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		c.mu.Lock()
		c.challenge = hex.EncodeToString(b)
		ch = c.challenge
		c.mu.Unlock()
		c.send(&Frame{Type: TypeIAX, Subclass: IaxRegAuth, IEs: IEs{}.SetU16(IEAuthMethods, AuthMD5).SetStr(IEChallenge, ch).SetStr(IEUsername, user)})
		return
	}
	want := md5.Sum([]byte(ch + secret))
	if !ok || res != hex.EncodeToString(want[:]) {
		c.send(&Frame{Type: TypeIAX, Subclass: IaxRegRej, IEs: IEs{}.SetStr(IECause, "registration refused")})
		c.end()
		return
	}
	refresh, _ := f.IEs.U16(IERefresh)
	if refresh == 0 || refresh > 3600 {
		refresh = 60
	}
	c.send(&Frame{Type: TypeIAX, Subclass: IaxRegAck, IEs: IEs{}.SetStr(IEUsername, user).SetU16(IERefresh, refresh)})
	if h.Registered != nil {
		h.Registered(user, c.peer, int(refresh))
	}
	c.end()
}

func (c *Call) emit2(e Event) {
	select {
	case c.Events <- e:
	case <-time.After(time.Second):
	}
}

// end closes a finished exchange (a registration) after its acknowledgement.
func (c *Call) end() {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.mu.Unlock()
	go func() {
		time.Sleep(2 * time.Second)
		c.n.forget(c)
		c.closeEvents()
	}()
}

// --- what the owner of a call does ---

// DialParams describe a call out.
type DialParams struct {
	Called, Calling, CallingName string
	Username, Secret             string
	Context                      string
	Formats                      uint32 // every format offered
	Preferred                    uint32 // the one wanted
}

// Dial starts a call to peer. Events report it being accepted, ringing, answered or refused.
func (n *Node) Dial(peer netip.AddrPort, p DialParams) *Call {
	c := n.newCall(peer, 0)
	c.secret = p.Secret
	ies := IEs{}.SetU16(IEVersion, 2).SetStr(IECalledNumber, p.Called).SetU32(IECapability, p.Formats).SetU32(IEFormat, p.Preferred)
	if p.Calling != "" {
		ies.SetStr(IECallingNumber, p.Calling)
	}
	if p.CallingName != "" {
		ies.SetStr(IECallingName, p.CallingName)
	}
	if p.Username != "" {
		ies.SetStr(IEUsername, p.Username)
	}
	if p.Context != "" {
		ies.SetStr(IECalledContext, p.Context)
	}
	ies[IECallToken] = []byte{}
	c.newIEs = ies
	c.send(&Frame{Type: TypeIAX, Subclass: IaxNew, IEs: ies})
	return c
}

// Register sends one registration; Events reports "registered" (Code is the refresh in seconds) or
// "hangup" when it is refused.
func (n *Node) Register(peer netip.AddrPort, username, secret string, refresh int) *Call {
	c := n.newCall(peer, 0)
	c.secret = secret
	ies := IEs{}.SetStr(IEUsername, username).SetU16(IERefresh, uint16(refresh))
	ies[IECallToken] = []byte{}
	c.newIEs = IEs{}.SetStr(IEUsername, username).SetU16(IERefresh, uint16(refresh))
	c.newIEs[IECallToken] = []byte{}
	c.send(&Frame{Type: TypeIAX, Subclass: IaxRegReq, IEs: ies})
	return c
}

// Username is who the far end said it is (IAX2's USERNAME in its NEW).
func (c *Call) Username() string { c.mu.Lock(); defer c.mu.Unlock(); return c.username }

// Peer is the far end's address.
func (c *Call) Peer() netip.AddrPort { return c.peer }

// Authenticate challenges an incoming call to prove it knows secret; Events reports "authenticated"
// (then Accept it) or a hangup.
func (c *Call) Authenticate(secret string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	c.mu.Lock()
	c.secret, c.challenge = secret, hex.EncodeToString(b)
	ch := c.challenge
	user := c.username
	c.mu.Unlock()
	ies := IEs{}.SetU16(IEAuthMethods, AuthMD5).SetStr(IEChallenge, ch)
	if user != "" {
		ies.SetStr(IEUsername, user)
	}
	c.send(&Frame{Type: TypeIAX, Subclass: IaxAuthReq, IEs: ies})
}

// Accept takes an incoming call with an audio format.
func (c *Call) Accept(format uint32) {
	c.mu.Lock()
	c.outFormat = format
	c.mu.Unlock()
	c.send(&Frame{Type: TypeIAX, Subclass: IaxAccept, IEs: IEs{}.SetU32(IEFormat, format)})
}

// Reject refuses an incoming call.
func (c *Call) Reject(cause string, code int) {
	c.mu.Lock()
	c.ended = true
	c.mu.Unlock()
	c.send(&Frame{Type: TypeIAX, Subclass: IaxReject, IEs: IEs{IECauseCode: {byte(code)}}.SetStr(IECause, cause)})
	go func() {
		time.Sleep(3 * time.Second)
		c.n.forget(c)
		c.closeEvents()
	}()
}

// Ringing tells the caller the call rings.
func (c *Call) Ringing() { c.send(&Frame{Type: TypeControl, Subclass: CtlRinging}) }

// Answer answers the call.
func (c *Call) Answer() { c.send(&Frame{Type: TypeControl, Subclass: CtlAnswer}) }

// Hangup ends the call.
func (c *Call) Hangup(cause string, code int) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.mu.Unlock()
	c.send(&Frame{Type: TypeIAX, Subclass: IaxHangup, IEs: IEs{IECauseCode: {byte(code)}}.SetStr(IECause, cause)})
	go func() {
		time.Sleep(3 * time.Second)
		c.n.forget(c)
		c.closeEvents()
	}()
}

// SendVoice sends audio in the agreed format: a full frame when one is needed (the first, and each time
// the timestamp's high 16 bits change), else a mini frame.
func (c *Call) SendVoice(audio []byte) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	ts := c.ts()
	full := c.voiceHigh == 0 || ts>>16 != c.voiceHigh>>16
	format := c.outFormat
	if full {
		c.voiceHigh = ts | 1
	}
	local, peer := c.local, c.peer
	c.mu.Unlock()
	if full {
		c.send(&Frame{Type: TypeVoice, Subclass: format, TS: ts, Data: audio})
		return
	}
	c.n.write(MarshalMini(local, ts, audio), peer)
}

// SendDTMF sends a key press.
func (c *Call) SendDTMF(digit byte) {
	c.send(&Frame{Type: TypeDTMFEnd, Subclass: uint32(digit)})
}

// ErrClosed is a call that has ended.
var ErrClosed = errors.New("iax: the call has ended")
