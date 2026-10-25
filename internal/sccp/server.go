package sccp

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

// Bridge is the engine side: the loopback gateway phones register through.
type Bridge interface {
	OpenAs(kind, agent, id, ext, secret, client string) (*webphone.Phone, error)
}

// Media supplies RTP endpoints.
type Media interface {
	NewMediaEndpoint() (*media.Endpoint, error)
}

// DigitTimeout is how long a phone may pause between digits before the number is dialled.
var DigitTimeout = 4 * time.Second

// Server accepts Skinny phones on one TCP port.
type Server struct {
	ctx    context.Context
	bridge Bridge
	media  Media
	secret func(ext string) string
	log    *slog.Logger

	mu     sync.Mutex
	ln     net.Listener
	port   int
	phones map[string]config.SCCPPhone // by device name
	allow  []netip.Prefix
	conns  map[string]*phone
}

// New makes a server; Apply opens its port.
func New(ctx context.Context, b Bridge, m Media, secret func(ext string) string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{ctx: ctx, bridge: b, media: m, secret: secret, log: log, phones: map[string]config.SCCPPhone{}, conns: map[string]*phone{}}
}

// Port is the listening port (0 when closed).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Apply puts the phone list in force. Phones no longer listed are disconnected; with Skinny off the
// port closes. listen overrides the address (tests); empty listens on every address.
func (s *Server) Apply(c config.SCCP, listen string) {
	want := c.Port
	if want == 0 {
		want = 2000
	}
	if listen == "" {
		listen = ":" + strconv.Itoa(want)
	}
	s.mu.Lock()
	s.phones = map[string]config.SCCPPhone{}
	for _, p := range c.Phones {
		s.phones[p.Device] = p
	}
	var drop []*phone
	for name, p := range s.conns {
		if cfg, ok := s.phones[name]; !ok || cfg.Extension != p.ext || !c.Enabled {
			drop = append(drop, p)
		}
	}
	ln := s.ln
	reopen := ln != nil && (!c.Enabled || (c.Port != 0 && s.port != want))
	if reopen {
		ln.Close()
		s.ln, s.port, ln = nil, 0, nil
	}
	s.mu.Unlock()
	for _, p := range drop {
		p.conn.Close()
	}
	if !c.Enabled || ln != nil {
		return
	}
	l, err := net.Listen("tcp", listen)
	if err != nil {
		s.log.Error("sccp: cannot open the Skinny port", "addr", listen, "err", err)
		return
	}
	s.mu.Lock()
	s.ln, s.port = l, l.Addr().(*net.TCPAddr).Port
	s.mu.Unlock()
	go s.accept(l)
}

// Close disconnects every phone and closes the port.
func (s *Server) Close() { s.Apply(config.SCCP{}, "") }

// Status is each listed phone's state for the GUI.
func (s *Server) Status() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for name := range s.phones {
		if p := s.conns[name]; p != nil {
			out[name] = "registered from " + p.addr.Addr().String()
		} else {
			out[name] = "not connected"
		}
	}
	return out
}

func (s *Server) accept(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		ap, _ := netip.ParseAddrPort(c.RemoteAddr().String())
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
		// Skinny has no passwords: phones only from the local network.
		if a := ap.Addr(); !(a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()) {
			c.Close()
			continue
		}
		go s.serve(c, ap)
	}
}

// phone is one connected Skinny phone.
type phone struct {
	s    *Server
	conn net.Conn
	addr netip.AddrPort
	name string
	ext  string
	wmu  sync.Mutex
	sip  *webphone.Phone

	mu       sync.Mutex
	offHook  bool
	digits   string
	timer    *time.Timer
	ref      uint32
	call     *webphone.Call
	callRef  uint32
	state    int
	rtp      *media.Endpoint
	redial   string
	incoming *webphone.Call
}

func (p *phone) send(id uint32, b *builder) {
	var data []byte
	if b != nil {
		data = b.b.Bytes()
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = p.conn.Write(frame(id, data))
}

func (s *Server) serve(c net.Conn, addr netip.AddrPort) {
	defer c.Close()
	p := &phone{s: s, conn: c, addr: addr}
	for {
		_ = c.SetReadDeadline(time.Now().Add(90 * time.Second))
		id, data, err := readMessage(c)
		if err != nil {
			break
		}
		if p.name == "" && id != msgRegister {
			continue
		}
		p.handle(id, data)
	}
	p.gone()
}

func (p *phone) gone() {
	p.mu.Lock()
	call, inc, ep, sp := p.call, p.incoming, p.rtp, p.sip
	p.call, p.incoming, p.rtp, p.sip = nil, nil, nil, nil
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	for _, c := range []*webphone.Call{call, inc} {
		if c != nil {
			c.Hangup()
		}
	}
	if ep != nil {
		ep.Close()
	}
	if sp != nil {
		sp.Close()
	}
	if p.name != "" {
		p.s.mu.Lock()
		if p.s.conns[p.name] == p {
			delete(p.s.conns, p.name)
		}
		p.s.mu.Unlock()
	}
}

func (p *phone) handle(id uint32, d []byte) {
	switch id {
	case msgRegister:
		p.register(d)
	case msgKeepAlive:
		p.send(msgKeepAliveAck, nil)
	case msgButtonTemplateReq:
		b := (&builder{}).u32(0, 1, 1).u8(1, 0x09) // one line button
		b.zero(2 * 41)
		p.send(msgButtonTemplate, b)
	case msgSoftKeyTemplateReq:
		p.send(msgSoftKeyTemplateRes, softKeyTemplate())
	case msgSoftKeySetReq:
		p.send(msgSoftKeySetRes, softKeySets())
	case msgLineStatReq:
		n := le32(d, 0)
		// Number 24, display name 40, label 40, display options: 112 bytes, as phones and Wireshark read it.
		p.send(msgLineStat, (&builder{}).u32(n).str(p.ext, 24).str(p.ext, 40).str(p.ext, 40).u32(0))
	case msgSpeedDialStatReq:
		p.send(msgSpeedDialStat, (&builder{}).u32(le32(d, 0)).str("", 24).str("", 40))
	case msgConfigStatReq:
		p.send(msgConfigStat, (&builder{}).str(p.name, 16).u32(0, 1).str(p.ext, 40).str("BitVoice", 40).u32(1, 0))
	case msgTimeDateReq:
		t := time.Now()
		p.send(msgDefineTimeDate, (&builder{}).u32(uint32(t.Year()), uint32(t.Month()), uint32(t.Weekday()), uint32(t.Day()),
			uint32(t.Hour()), uint32(t.Minute()), uint32(t.Second()), uint32(t.Nanosecond()/1e6), uint32(t.Unix())))
	case msgVersionReq:
		p.send(msgVersion, (&builder{}).str("BitVoice", 16))
	case msgServerReq:
		b := (&builder{}).str("BitVoice", 48).zero(48 * 4)
		b.u32(uint32(p.s.Port()), 0, 0, 0, 0).zero(4 * 5)
		p.send(msgServerRes, b)
	case msgUnregister:
		p.send(msgUnregisterAck, (&builder{}).u32(0))
		p.conn.Close()
	case msgOffHook:
		p.hookOff()
	case msgOnHook:
		p.hookOn()
	case msgKeypadButton:
		p.key(le32(d, 0))
	case msgEnblocCall:
		p.dial(strings.TrimSpace(cstr(d)))
	case msgStimulus:
		switch le32(d, 0) {
		case stimulusLine:
			p.hookOff()
		case stimulusRedial:
			p.redialLast()
		}
	case msgSoftKeyEvent:
		switch le32(d, 0) {
		case keyNewCall:
			p.hookOff()
		case keyRedial:
			p.redialLast()
		case keyAnswer:
			p.hookOff()
		case keyEndCall:
			p.hookOn()
		}
	case msgOpenReceiveChannelAck:
		p.receiveAck(d)
	}
}

func (p *phone) register(d []byte) {
	if len(d) < 28 {
		return
	}
	name := strings.ToUpper(cstr(d[:16]))
	ver := uint8(0)
	if len(d) > 40 {
		ver = d[40]
	}
	p.s.mu.Lock()
	cfg, ok := p.s.phones[name]
	old := p.s.conns[name]
	p.s.mu.Unlock()
	if !ok {
		p.send(msgRegisterReject, (&builder{}).str("Not listed: "+name, 33))
		p.s.log.Info("sccp: unknown phone refused", "device", name, "from", p.addr)
		return
	}
	if old != nil && old != p {
		old.conn.Close()
	}
	sp, err := p.s.bridge.OpenAs("sccp", "BitVoice Skinny phone", name, cfg.Extension, p.s.secret(cfg.Extension), p.addr.Addr().String())
	if err != nil {
		p.send(msgRegisterReject, (&builder{}).str("Line not available", 33))
		return
	}
	p.name, p.ext, p.sip = name, cfg.Extension, sp
	p.s.mu.Lock()
	p.s.conns[name] = p
	p.s.mu.Unlock()
	if ver > 11 {
		ver = 11
	}
	p.send(msgRegisterAck, (&builder{}).u32(30).str("M/D/Y", 6).zero(2).u32(30).u8(ver, 0, 0, 0))
	p.send(msgCapabilitiesReq, nil)
	go p.events(sp)
}

func softKeyTemplate() *builder {
	keys := []struct {
		label string
		event uint32
	}{{"Redial", keyRedial}, {"NewCall", keyNewCall}, {"EndCall", keyEndCall}, {"Answer", keyAnswer}}
	b := (&builder{}).u32(0, uint32(len(keys)), uint32(len(keys)))
	for _, k := range keys {
		b.str(k.label, 16).u32(k.event)
	}
	return b.zero((32 - len(keys)) * 20)
}

// softKeySets lists, for each set, the template entries (1-based) shown.
func softKeySets() *builder {
	sets := map[int][]uint8{setOnHook: {1, 2}, setConnected: {3}, setRingIn: {4, 3}, setOffHook: {1, 3}, setRingOut: {3}}
	b := (&builder{}).u32(0, 16, 16)
	for i := 0; i < 16; i++ {
		var idx [16]uint8
		copy(idx[:], sets[i])
		b.u8(idx[:]...)
		for j := 0; j < 16; j++ {
			info := uint16(0)
			if idx[j] != 0 {
				info = 300 + uint16(idx[j])
			}
			b.u8(uint8(info), uint8(info>>8))
		}
	}
	return b
}

func (p *phone) callState(state int, ref uint32) {
	p.send(msgCallState, (&builder{}).u32(uint32(state), 1, ref, 0, 0, 0))
}

func (p *phone) keys(set int, ref uint32) {
	p.send(msgSelectSoftKeys, (&builder{}).u32(1, ref, uint32(set), 0xffffffff))
}

func (p *phone) tone(t uint32, ref uint32) {
	p.send(msgStartTone, (&builder{}).u32(t, 0, 1, ref))
}

func (p *phone) stopTone(ref uint32) {
	p.send(msgStopTone, (&builder{}).u32(1, ref, 0))
}

func (p *phone) prompt(text string, ref uint32) {
	p.send(msgDisplayPromptStatus, (&builder{}).u32(0).str(text, 32).u32(1, ref, 0, 0, 0))
}

func (p *phone) lamp(mode uint32) {
	p.send(msgSetLamp, (&builder{}).u32(stimulusLine, 1, mode))
}

func (p *phone) callInfo(ref uint32, inbound bool, number, name string) {
	b := &builder{}
	typ := uint32(2)
	if inbound {
		b.str(name, 40).str(number, 24).str("", 40).str(p.ext, 24)
		typ = 1
	} else {
		b.str("", 40).str(p.ext, 24).str(name, 40).str(number, 24)
	}
	b.u32(1, ref, typ)
	b.zero(40 + 24 + 40 + 24 + 4 + 4 + 24*4 + 12)
	p.send(msgCallInfo, b)
}

func (p *phone) hookOff() {
	p.mu.Lock()
	if p.offHook {
		p.mu.Unlock()
		return
	}
	p.offHook = true
	inc := p.incoming
	p.mu.Unlock()
	if inc != nil {
		p.answer(inc)
		return
	}
	p.mu.Lock()
	p.ref++
	ref := p.ref
	p.callRef, p.digits, p.state = ref, "", stateOffHook
	p.mu.Unlock()
	p.send(msgActivateCallPlane, (&builder{}).u32(1))
	p.lamp(lampOn)
	p.callState(stateOffHook, ref)
	p.keys(setOffHook, ref)
	p.prompt("Enter number", ref)
	p.tone(toneDial, ref)
}

func (p *phone) hookOn() {
	p.mu.Lock()
	call, inc, ref := p.call, p.incoming, p.callRef
	p.offHook, p.call, p.digits, p.state = false, nil, "", stateOnHook
	if p.timer != nil {
		p.timer.Stop()
	}
	if inc != nil && call == nil {
		// Declining a ringing call from the phone: it stops ringing here; the others keep ringing.
		p.incoming = nil
	}
	p.mu.Unlock()
	if call != nil {
		call.Hangup()
	} else if inc != nil {
		inc.Hangup()
	}
	p.closeMedia(ref)
	p.send(msgSetRinger, (&builder{}).u32(ringOff, 1, 1, ref))
	p.callState(stateOnHook, ref)
	p.lamp(lampOff)
	p.keys(setOnHook, 0)
	p.send(msgClearPromptStatus, (&builder{}).u32(1, ref))
}

func (p *phone) key(button uint32) {
	var c string
	switch {
	case button <= 9:
		c = strconv.Itoa(int(button))
	case button == 14:
		c = "*"
	case button == 15:
		c = "#"
	default:
		return
	}
	p.mu.Lock()
	if !p.offHook || p.call != nil || p.state != stateOffHook {
		p.mu.Unlock()
		return // keys in a call go as audio tones from the phone itself
	}
	first := p.digits == ""
	p.digits += c
	digits, ref := p.digits, p.callRef
	if p.timer != nil {
		p.timer.Stop()
	}
	if c != "#" {
		p.timer = time.AfterFunc(DigitTimeout, func() { p.dialCollected(ref) })
	}
	p.mu.Unlock()
	if first {
		p.stopTone(ref)
	}
	p.send(msgDialedNumber, (&builder{}).str(digits, 24).u32(1, ref))
	if c == "#" {
		p.dialCollected(ref)
	}
}

func (p *phone) dialCollected(ref uint32) {
	p.mu.Lock()
	if p.callRef != ref || p.call != nil {
		p.mu.Unlock()
		return
	}
	n := strings.TrimSuffix(p.digits, "#")
	p.mu.Unlock()
	p.dial(n)
}

func (p *phone) redialLast() {
	p.mu.Lock()
	n := p.redial
	p.mu.Unlock()
	if n == "" {
		return
	}
	p.hookOff()
	p.dial(n)
}

func (p *phone) dial(number string) {
	if number == "" {
		return
	}
	p.mu.Lock()
	if p.call != nil || p.sip == nil {
		p.mu.Unlock()
		return
	}
	if !p.offHook {
		p.mu.Unlock()
		p.hookOff()
		p.mu.Lock()
	}
	ref, sp := p.callRef, p.sip
	p.redial, p.state = number, stateRingOut
	p.mu.Unlock()
	p.stopTone(ref)
	ep, err := p.openMedia(ref)
	var c *webphone.Call
	if err == nil {
		c, err = sp.Dial(number, ep, 0, 101)
	}
	if err != nil {
		p.s.log.Warn("sccp: call not placed", "device", p.name, "err", err)
		p.closeMedia(ref)
		p.tone(toneReorder, ref)
		return
	}
	p.mu.Lock()
	p.call = c
	p.mu.Unlock()
	p.callState(stateRingOut, ref)
	p.callInfo(ref, false, number, "")
	p.keys(setRingOut, ref)
	p.prompt("Calling "+number, ref)
}

// openMedia makes our endpoint and asks the phone to send to it and to open its receiver.
func (p *phone) openMedia(ref uint32) (*media.Endpoint, error) {
	ep, err := p.s.media.NewMediaEndpoint()
	if err != nil {
		return nil, err
	}
	ep.Start()
	p.mu.Lock()
	if p.rtp != nil {
		p.rtp.Close()
	}
	p.rtp = ep
	p.mu.Unlock()
	local := localIP(p.conn)
	// The full layouts (168 and 116 bytes, as Asterisk's chan_skinny sends them to Cisco phones): the
	// encryption key block and the stream and DTMF fields after it are zero.
	p.send(msgOpenReceiveChannel, (&builder{}).u32(ref, ref, 20, codecULaw, 0, 0, ref).zero(4*35))
	p.send(msgStartMediaTransmit, (&builder{}).u32(ref, ref).u8(local[:]...).u32(uint32(ep.Port()), 20, codecULaw, 0, 0, 1, 0).zero(4*19))
	return ep, nil
}

func localIP(c net.Conn) [4]byte {
	var out [4]byte
	if ap, err := netip.ParseAddrPort(c.LocalAddr().String()); err == nil && ap.Addr().Unmap().Is4() {
		out = ap.Addr().Unmap().As4()
	}
	return out
}

// receiveAck learns where the phone receives audio.
func (p *phone) receiveAck(d []byte) {
	var ip netip.Addr
	var port uint32
	switch {
	case len(d) >= 32: // version 17 and later: an address family, 16 bytes of address
		if le32(d, 4) == 0 {
			ip = netip.AddrFrom4([4]byte(d[8:12]))
		} else {
			ip = netip.AddrFrom16([16]byte(d[8:24]))
		}
		port = le32(d, 24)
	case len(d) >= 12:
		ip, port = netip.AddrFrom4([4]byte(d[4:8])), le32(d, 8)
	default:
		return
	}
	if le32(d, 0) != 0 {
		return
	}
	if !ip.IsValid() || ip.IsUnspecified() {
		ip = p.addr.Addr()
	}
	p.mu.Lock()
	ep := p.rtp
	p.mu.Unlock()
	if ep != nil {
		ep.SetRemote(ip.Unmap().String(), int(port))
	}
}

func (p *phone) closeMedia(ref uint32) {
	p.mu.Lock()
	ep := p.rtp
	p.rtp = nil
	p.mu.Unlock()
	if ep == nil {
		return
	}
	p.send(msgCloseReceiveChannel, (&builder{}).u32(ref, ref, ref, 0)) // conference, party, call, port handling
	p.send(msgStopMediaTransmit, (&builder{}).u32(ref, ref, ref, 0))
	ep.Close()
}

func (p *phone) answer(c *webphone.Call) {
	p.mu.Lock()
	ref := p.callRef
	p.incoming, p.call, p.state = nil, c, stateConnected
	p.mu.Unlock()
	p.send(msgSetRinger, (&builder{}).u32(ringOff, 1, 1, ref))
	p.send(msgActivateCallPlane, (&builder{}).u32(1))
	ep, err := p.openMedia(ref)
	if err == nil {
		err = c.Answer(ep, 0, 101)
	}
	if err != nil {
		p.closeMedia(ref)
		p.callState(stateOnHook, ref)
		p.tone(toneReorder, ref)
		return
	}
	p.lamp(lampOn)
	p.callState(stateConnected, ref)
	p.keys(setConnected, ref)
}

func (p *phone) events(sp *webphone.Phone) {
	for {
		select {
		case <-p.s.ctx.Done():
			return
		case ev, ok := <-sp.Events:
			if !ok {
				return
			}
			p.event(sp, ev)
			p.mu.Lock()
			gone := p.sip != sp
			p.mu.Unlock()
			if gone {
				return
			}
		}
	}
}

func (p *phone) event(sp *webphone.Phone, ev webphone.Event) {
	p.mu.Lock()
	mine := p.call != nil && p.call.ID == ev.CallID
	ringingMine := p.incoming != nil && p.incoming.ID == ev.CallID
	ref := p.callRef
	p.mu.Unlock()
	switch ev.Type {
	case "incoming":
		c := sp.Find(ev.CallID)
		p.mu.Lock()
		busy := p.offHook || p.incoming != nil || p.call != nil
		if !busy && c != nil {
			p.ref++
			p.callRef, p.incoming, p.state = p.ref, c, stateRingIn
			ref = p.ref
		}
		p.mu.Unlock()
		if busy || c == nil {
			if c != nil {
				c.Hangup() // busy here; the extension's other phones still ring
			}
			return
		}
		p.callInfo(ref, true, ev.From, ev.FromName)
		p.callState(stateRingIn, ref)
		p.send(msgSetRinger, (&builder{}).u32(ringInside, 1, 1, ref))
		p.lamp(lampBlink)
		p.keys(setRingIn, ref)
		p.prompt("From "+ev.From, ref)
	case "ringing":
		if mine {
			p.tone(toneAlert, ref)
		}
	case "answered":
		if mine {
			p.stopTone(ref)
			p.callState(stateConnected, ref)
			p.keys(setConnected, ref)
			p.prompt("Connected", ref)
		}
	case "ended", "failed":
		switch {
		case ringingMine:
			p.mu.Lock()
			p.incoming, p.state = nil, stateOnHook
			p.mu.Unlock()
			p.send(msgSetRinger, (&builder{}).u32(ringOff, 1, 1, ref))
			p.callState(stateOnHook, ref)
			p.lamp(lampOff)
			p.keys(setOnHook, 0)
			p.send(msgClearPromptStatus, (&builder{}).u32(1, ref))
		case mine:
			p.mu.Lock()
			p.call = nil
			off := p.offHook
			p.mu.Unlock()
			p.closeMedia(ref)
			if !off {
				p.callState(stateOnHook, ref)
				return
			}
			t := uint32(toneReorder)
			if ev.Type == "failed" && strings.HasPrefix(ev.Reason, "486") {
				t = toneBusy
				p.callState(stateBusy, ref)
			}
			p.tone(t, ref)
			p.prompt("Call ended", ref)
		}
	}
}
