package h323

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// TokenHeader carries the secret the engine and its loopback gateways share (the IAX2 one).
const TokenHeader = "X-BitVoice-IAX"

// Trunk is an H.323 trunk as the gateway needs it: the far gatekeeper-less endpoint or gateway.
type Trunk struct {
	ID, Name string
	Host     string
	Port     int      // 1720 when 0
	Codecs   []string // PCMU and PCMA, in order; both when empty
}

// Service listens for H.323 calls (TCP 1720) once the first H.323 trunk appears and keeps a loopback
// SIP port per trunk for the engine.
type Service struct {
	addr   string
	engine func() netip.AddrPort
	token  string
	log    *slog.Logger
	ctx    context.Context

	mu     sync.Mutex
	ln     net.Listener
	err    error
	trunks map[string]*trunk
}

// NewService makes the service; addr is where it listens (":1720"), token the engine's secret.
func NewService(ctx context.Context, addr string, engine func() netip.AddrPort, token string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{addr: addr, engine: engine, token: token, log: log, ctx: ctx, trunks: map[string]*trunk{}}
}

type trunk struct {
	s      *Service
	t      Trunk
	stack  *sip.Stack
	cancel context.CancelFunc
	mu     sync.Mutex
	calls  map[string]*call // by SIP Call-ID
}

// Apply puts the H.323 trunks in force: new ones start, changed ones restart, removed ones stop.
func (s *Service) Apply(trunks []Trunk) {
	want := map[string]Trunk{}
	for _, t := range trunks {
		if t.Port == 0 {
			t.Port = 1720
		}
		want[t.ID] = t
	}
	s.mu.Lock()
	var stop []*trunk
	for id, tr := range s.trunks {
		if t, ok := want[id]; !ok || t.Host != tr.t.Host || t.Port != tr.t.Port || strings.Join(t.Codecs, ",") != strings.Join(tr.t.Codecs, ",") {
			stop = append(stop, tr)
			delete(s.trunks, id)
		}
	}
	if len(want) > 0 && s.ln == nil {
		ln, err := net.Listen("tcp", s.addr)
		if err != nil {
			if s.err == nil {
				s.log.Error("H.323 trunks cannot take calls: the H.323 port cannot be opened", "addr", s.addr, "err", err)
			}
			s.err = err
		} else {
			s.ln, s.err = ln, nil
			go s.accept(ln)
		}
	}
	s.mu.Unlock()
	for _, tr := range stop {
		tr.close()
	}
	for id, t := range want {
		s.mu.Lock()
		_, ok := s.trunks[id]
		s.mu.Unlock()
		if ok {
			continue
		}
		tr, err := s.open(t)
		if err != nil {
			s.log.Error("H.323 trunk could not start", "trunk", t.Name, "err", err)
			continue
		}
		s.mu.Lock()
		s.trunks[id] = tr
		s.mu.Unlock()
	}
}

func (s *Service) open(t Trunk) (*trunk, error) {
	ctx, cancel := context.WithCancel(s.ctx)
	tr := &trunk{s: s, t: t, cancel: cancel, calls: map[string]*call{}}
	tr.stack = sip.NewStack(tr.handle)
	tr.stack.Host = "127.0.0.1"
	if err := tr.stack.Listen("127.0.0.1:0"); err != nil {
		cancel()
		return nil, err
	}
	tr.stack.Serve(ctx)
	return tr, nil
}

func (tr *trunk) close() {
	tr.mu.Lock()
	calls := make([]*call, 0, len(tr.calls))
	for _, c := range tr.calls {
		calls = append(calls, c)
	}
	tr.mu.Unlock()
	for _, c := range calls {
		c.end(reasonUndefined, false)
	}
	tr.cancel()
	tr.stack.Close()
}

// SIPPort is the loopback port standing for a trunk (0 when it is not running).
func (s *Service) SIPPort(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr := s.trunks[id]; tr != nil {
		return tr.stack.Port()
	}
	return 0
}

// Status is a trunk's state: "" when it is ready, "not running", or "failed: ...".
func (s *Service) Status(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trunks[id] == nil {
		return "not running"
	}
	if s.err != nil {
		return "failed: the H.323 port (1720) could not be opened, so only outgoing calls work"
	}
	return ""
}

// Token is the secret the engine sends the gateway.
func (s *Service) Token() string { return s.token }

// Port is where H.323 listens, 0 when it does not.
func (s *Service) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return 0
	}
	return s.ln.Addr().(*net.TCPAddr).Port
}

// Close stops everything.
func (s *Service) Close() {
	s.Apply(nil)
	s.mu.Lock()
	if s.ln != nil {
		s.ln.Close()
		s.ln = nil
	}
	s.mu.Unlock()
}

// call is one H.323 call and its SIP dialog with the engine.
type call struct {
	tr       *trunk
	conn     net.Conn
	wmu      sync.Mutex
	callRef  uint16
	inbound  bool // came from H.323
	confID   []byte
	h323ID   []byte
	codec    int
	peerRTP  netip.AddrPort // the H.323 side's RTP
	engRTP   netip.AddrPort // the engine's RTP, as the H.323 side reaches it
	props    []*olc         // inbound: the caller's proposals taken
	answered bool
	ended    bool
	sentFast bool
	mu       sync.Mutex

	callID              string
	invite              *sip.ServerTx
	client              *sip.ClientTx
	localURI, remoteURI string
	target              string
	cseq                int
}

func (c *call) send(m *q931) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.conn.Write(tpkt(m.bytes()))
}

func (c *call) msg(t byte, u *uuie) *q931 {
	if u != nil {
		u.callID = c.h323ID
		if u.confID == nil {
			u.confID = c.confID
		}
	}
	return newQ931(t, c.callRef, c.inbound, u)
}

func randomID() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

func codecName(a int) string {
	if a == g711Alaw64k {
		return "PCMA"
	}
	return "PCMU"
}

func (tr *trunk) allowed(a int) bool {
	if len(tr.t.Codecs) == 0 {
		return true
	}
	for _, n := range tr.t.Codecs {
		if strings.EqualFold(n, codecName(a)) {
			return true
		}
	}
	return false
}

func (s *Service) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

// trunkFrom is the trunk whose far end has this address.
func (s *Service) trunkFrom(ip netip.Addr) *trunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tr := range s.trunks {
		if a, err := netip.ParseAddr(tr.t.Host); err == nil && a.Unmap() == ip {
			return tr
		}
		if ips, err := net.LookupIP(tr.t.Host); err == nil {
			for _, x := range ips {
				if a, ok := netip.AddrFromSlice(x); ok && a.Unmap() == ip {
					return tr
				}
			}
		}
	}
	return nil
}

func addrOf(a net.Addr) netip.AddrPort {
	ap, _ := netip.ParseAddrPort(a.String())
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

// serve takes a call from the far end: only from a trunk's address, starting with Setup.
func (s *Service) serve(conn net.Conn) {
	defer conn.Close()
	tr := s.trunkFrom(addrOf(conn.RemoteAddr()).Addr())
	if tr == nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, err := readTPKT(conn)
	if err != nil {
		return
	}
	m, err := parseQ931(b)
	if err != nil || m.msgType != q931Setup {
		return
	}
	u, err := m.uu()
	if err != nil {
		s.log.Info("h323: unreadable Setup", "from", conn.RemoteAddr(), "err", err)
		return
	}
	c := &call{tr: tr, conn: conn, callRef: m.callRef, inbound: true, confID: u.confID, h323ID: u.callID}
	if c.h323ID == nil {
		c.h323ID = randomID()
	}
	if !c.takeProposals(u.fastStart) {
		c.send(c.msg(q931ReleaseComplete, &uuie{body: bodyReleaseComplete, reason: reasonUndefined}))
		s.log.Info("h323: call refused: no Fast Connect with G.711", "trunk", tr.t.Name)
		return
	}
	c.send(c.msg(q931CallProceeding, &uuie{body: bodyCallProceeding}))
	called := number(m.ies[ieCalled])
	if called == "" && len(u.calledAlias) > 0 {
		called = u.calledAlias[0]
	}
	from := number(m.ies[ieCalling])
	if from == "" && len(u.callerAlias) > 0 && onlyDigits(u.callerAlias[0]) {
		from = u.callerAlias[0]
	}
	name := string(m.ies[ieDisplay])
	if name == "" {
		for _, a := range u.callerAlias {
			if !onlyDigits(a) {
				name = a
				break
			}
		}
	}
	if from == "" {
		from = name
	}
	go c.toEngine(called, from, name)
	c.readLoop()
}

// takeProposals chooses, among the caller's Fast Connect proposals, a G.711 pair this trunk allows:
// what the caller sends and what it receives, with the address it receives on.
func (c *call) takeProposals(fs [][]byte) bool {
	var olcs []*olc
	for _, b := range fs {
		if o, err := decodeOLC(b); err == nil {
			olcs = append(olcs, o)
		}
	}
	for _, rx := range olcs {
		if !rx.reverse || !rx.media.IsValid() || !c.tr.allowed(rx.codec) {
			continue
		}
		for _, tx := range olcs {
			if !tx.reverse && tx.codec == rx.codec {
				c.codec, c.peerRTP, c.props = rx.codec, rx.media, []*olc{tx, rx}
				return true
			}
		}
	}
	return false
}

func sipUser(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("+*#-_.", r) {
			return r
		}
		return -1
	}, s)
}

func (c *call) description(media netip.AddrPort) *sdp.Session {
	pt := 0
	if c.codec == g711Alaw64k {
		pt = 8
	}
	return sdp.New(media.Addr().String(), int(media.Port()), uint64(time.Now().Unix()), []sdp.Codec{{PT: pt, Name: codecName(c.codec), ClockRate: 8000}}, -1)
}

// engineMedia is the engine's RTP address as the far end must reach it: the address this computer
// has towards the far end when the engine described a loopback one.
func (c *call) engineMedia(s *sdp.Session) netip.AddrPort {
	if s == nil || s.Audio() == nil {
		return netip.AddrPort{}
	}
	ip, err := netip.ParseAddr(s.Connection(s.Audio()))
	if err != nil || ip.IsLoopback() || ip.IsUnspecified() {
		ip = addrOf(c.conn.LocalAddr()).Addr()
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(s.Audio().Port))
}

func (c *call) fastAnswer() [][]byte {
	tx, rx := c.props[0], c.props[1]
	ctl := netip.AddrPortFrom(c.engRTP.Addr(), c.engRTP.Port()+1)
	return [][]byte{
		encodeOLC(&olc{number: tx.number, codec: c.codec, frames: tx.frames, session: 1, media: c.engRTP, control: ctl}),
		encodeOLC(&olc{number: rx.number, reverse: true, codec: c.codec, frames: rx.frames, session: 1, media: rx.media, control: ctl}),
	}
}

// toEngine places the H.323 caller's call on the engine.
func (c *call) toEngine(called, from, name string) {
	tr := c.tr
	engine := tr.s.engine()
	c.callID = sip.NewCallID()
	c.localURI = "<sip:" + sipUser(from) + "@127.0.0.1>;tag=" + sip.NewTag()
	if name != "" {
		c.localURI = strconv.Quote(name) + " " + c.localURI
	}
	c.remoteURI = "<sip:" + sipUser(called) + "@127.0.0.1>"
	c.cseq = 1
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + sipUser(called) + "@" + engine.String()}
	inv.Set("From", c.localURI)
	inv.Set("To", c.remoteURI)
	inv.Set("Call-ID", c.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", tr.contact())
	inv.Set(TokenHeader, tr.s.token)
	inv.Set("Content-Type", "application/sdp")
	inv.Body = c.description(c.peerRTP).Bytes()
	tr.add(c)
	tx := tr.stack.Request(inv, sip.Peer{Transport: "UDP", Addr: engine})
	c.mu.Lock()
	c.client = tx
	c.mu.Unlock()
	var fin *sip.Message
	alerted := false
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			fin = r
			continue
		}
		if r.StatusCode == 180 || r.StatusCode == 183 {
			u := &uuie{body: bodyAlerting}
			if s, err := sdp.Parse(r.Body); err == nil && s.Audio() != nil {
				c.mu.Lock()
				c.engRTP = c.engineMedia(s)
				if !c.sentFast {
					c.sentFast = true
					u.fastStart = c.fastAnswer()
				}
				c.mu.Unlock()
			}
			if !alerted || u.fastStart != nil {
				c.send(c.msg(q931Alerting, u))
				alerted = true
			}
		}
	}
	if fin == nil || fin.StatusCode >= 300 {
		reason := reasonUndefined
		if fin != nil {
			reason = reasonFor(fin.StatusCode)
		}
		c.end(reason, true)
		return
	}
	c.mu.Lock()
	c.remoteURI = fin.Get("To")
	if ct, err := sip.ParseAddress(fin.Get("Contact")); err == nil && ct.URI != nil {
		c.target = ct.URI.String()
	}
	c.answered = true
	u := &uuie{body: bodyConnect}
	if s, err := sdp.Parse(fin.Body); err == nil && s.Audio() != nil {
		c.engRTP = c.engineMedia(s)
	}
	if !c.sentFast {
		c.sentFast = true
		u.fastStart = c.fastAnswer()
	}
	c.mu.Unlock()
	n, _ := fin.CSeq()
	ack := c.request(sip.ACK)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = tr.stack.Stateless(ack, sip.Peer{Transport: "UDP", Addr: engine})
	tx.SetAck(ack)
	c.send(c.msg(q931Connect, u))
}

// readLoop follows the H.323 side until it releases the call or the connection closes.
func (c *call) readLoop() {
	for {
		_ = c.conn.SetReadDeadline(time.Time{})
		b, err := readTPKT(c.conn)
		if err != nil {
			c.end(reasonUndefined, false)
			return
		}
		m, err := parseQ931(b)
		if err != nil {
			continue
		}
		if m.msgType == q931ReleaseComplete {
			if !c.inbound {
				c.fromFar(m) // before the answer: the engine is told why
			}
			c.end(reasonUndefined, false)
			return
		}
		if !c.inbound {
			c.fromFar(m)
		}
	}
}

// reasonFor turns a SIP failure into a release reason.
func reasonFor(code int) int {
	switch code {
	case 404, 410, 484:
		return reasonUnreachable
	case 403:
		return reasonNoPermission
	case 486, 600:
		return reasonAdaptiveBusy
	case 503:
		return reasonGatewayResources
	case 603:
		return reasonDestinationRejection
	}
	return reasonUndefined
}

// sipFor turns an H.323 release (its reason, or the Q.931 cause) into a SIP failure.
func sipFor(reason int, cause []byte) int {
	if len(cause) >= 2 {
		switch cause[1] & 0x7f {
		case 1, 3:
			return 404
		case 17:
			return 486
		case 18, 19, 16:
			return 480
		case 21:
			return 603
		case 28:
			return 484
		case 34, 38, 41, 42, 44, 47:
			return 503
		}
	}
	switch reason {
	case reasonUnreachable:
		return 404
	case reasonDestinationRejection:
		return 603
	case reasonNoPermission:
		return 403
	case reasonBadFormatAddress:
		return 484
	case reasonAdaptiveBusy, reasonInConf:
		return 486
	case reasonNoBandwidth, reasonGatewayResources:
		return 503
	}
	return 480
}

func (tr *trunk) contact() string { return fmt.Sprintf("<sip:h323@127.0.0.1:%d>", tr.stack.Port()) }

func (tr *trunk) add(c *call) {
	tr.mu.Lock()
	tr.calls[c.callID] = c
	tr.mu.Unlock()
}

func (c *call) request(method string) *sip.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.target
	if target == "" {
		target = "sip:" + c.tr.s.engine().String()
	}
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: target}
	m.Set("From", c.localURI)
	m.Set("To", c.remoteURI)
	m.Set("Call-ID", c.callID)
	if method != sip.ACK {
		c.cseq++
	}
	m.Set("CSeq", strconv.Itoa(c.cseq)+" "+method)
	m.Set("Contact", c.tr.contact())
	return m
}

// end finishes both sides. fromSIP: the engine ended it (or refused it), so the H.323 side is told.
func (c *call) end(reason int, fromSIP bool) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	answered, invite, client, inbound := c.answered, c.invite, c.client, c.inbound
	c.mu.Unlock()
	c.tr.mu.Lock()
	delete(c.tr.calls, c.callID)
	c.tr.mu.Unlock()
	if fromSIP {
		c.send(c.msg(q931ReleaseComplete, &uuie{body: bodyReleaseComplete, reason: reason}))
	} else {
		peer := sip.Peer{Transport: "UDP", Addr: c.tr.s.engine()}
		switch {
		case answered:
			go func() {
				for range c.tr.stack.Request(c.request(sip.BYE), peer).Responses {
				}
			}()
		case !inbound && invite != nil:
			r := sip.NewResponse(invite.Req, 480, "")
			r.Set("To", c.localURI)
			invite.Respond(r)
		case inbound && client != nil:
			client.Cancel()
		}
	}
	c.conn.Close()
}

// handle receives the engine's requests on the trunk's loopback port.
func (tr *trunk) handle(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	if tx == nil {
		return
	}
	if !in.From.Addr.Addr().IsLoopback() {
		tx.Respond(sip.NewResponse(m, 403, ""))
		return
	}
	to, _ := sip.ParseAddress(m.Get("To"))
	inDialog := to != nil && to.Tag() != ""
	tr.mu.Lock()
	c := tr.calls[m.CallID()]
	tr.mu.Unlock()
	switch m.Method {
	case sip.INVITE:
		if inDialog {
			if c == nil {
				tx.Respond(sip.NewResponse(m, 481, ""))
				return
			}
			// A re-INVITE (hold, a transfer): the far end keeps sending where it does; answer with it.
			r := sip.NewResponse(m, 200, "")
			r.Set("Contact", tr.contact())
			r.Set("Content-Type", "application/sdp")
			c.mu.Lock()
			r.Body = c.description(c.peerRTP).Bytes()
			c.mu.Unlock()
			tx.Respond(r)
			return
		}
		if m.Get(TokenHeader) != tr.s.token {
			tx.Respond(sip.NewResponse(m, 403, "Not The Engine"))
			return
		}
		go tr.outgoing(tx, m)
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if c != nil && !c.answered {
			c.end(reasonUndefined, true)
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if c != nil {
			c.mu.Lock()
			c.answered = false
			c.mu.Unlock()
			c.end(reasonUndefined, true)
		}
	case sip.ACK:
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// outgoing places the engine's call on the H.323 trunk, proposing G.711 by Fast Connect.
func (tr *trunk) outgoing(tx *sip.ServerTx, m *sip.Message) {
	offer, err := sdp.Parse(m.Body)
	if err != nil || offer.Audio() == nil {
		tx.Respond(sip.NewResponse(m, 488, ""))
		return
	}
	var codecs []int
	for _, cd := range offer.Audio().Codecs() {
		switch {
		case strings.EqualFold(cd.Name, "PCMU") && tr.allowed(g711Ulaw64k):
			codecs = append(codecs, g711Ulaw64k)
		case strings.EqualFold(cd.Name, "PCMA") && tr.allowed(g711Alaw64k):
			codecs = append(codecs, g711Alaw64k)
		}
	}
	if len(codecs) == 0 {
		tx.Respond(sip.NewResponse(m, 488, "H.323 Takes G.711 Only"))
		return
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(tr.t.Host, strconv.Itoa(tr.t.Port)), 5*time.Second)
	if err != nil {
		tx.Respond(sip.NewResponse(m, 503, "H.323 Peer Not Reachable"))
		return
	}
	var ref [2]byte
	_, _ = rand.Read(ref[:])
	c := &call{tr: tr, conn: conn, callRef: (uint16(ref[0])<<8 | uint16(ref[1])) & 0x7fff, confID: randomID(), h323ID: randomID()}
	if c.callRef == 0 {
		c.callRef = 1
	}
	c.callID, c.invite = m.CallID(), tx
	c.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	c.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		c.target = ct.URI.String()
	}
	c.cseq = 1000
	c.engRTP = c.engineMedia(offer)
	tr.add(c)
	tx.NoAck = func() { c.end(reasonUndefined, true) }
	called := ""
	if u, err := sip.ParseURI(m.RequestURI); err == nil {
		called = u.User
	}
	from, _ := sip.ParseAddress(m.Get("From"))
	ctl := netip.AddrPortFrom(c.engRTP.Addr(), c.engRTP.Port()+1)
	var fast [][]byte
	for i, a := range codecs {
		fast = append(fast,
			encodeOLC(&olc{number: 2*i + 1, codec: a, frames: 20, session: 1, control: ctl}),
			encodeOLC(&olc{number: 2*i + 2, reverse: true, codec: a, frames: 20, session: 1, media: c.engRTP, control: ctl}))
	}
	u := &uuie{body: bodySetup, calledAlias: []string{called}, fastStart: fast}
	setup := c.msg(q931Setup, u)
	setup.ies[ieBearer] = []byte{0x88, 0x90, 0xa5}
	setup.ies[ieCalled] = numberIE(called)
	if from != nil && from.URI != nil {
		if from.URI.User != "" {
			u.callerAlias = []string{from.URI.User}
			setup.ies[ieCalling] = numberIE(from.URI.User)
		}
		if from.Display != "" {
			setup.ies[ieDisplay] = []byte(from.Display)
		}
	}
	setup.ies[ieUserUser] = append([]byte{userUserH225}, encodeUU(u)...)
	c.send(setup)
	c.readLoop()
	c.mu.Lock()
	answered := c.answered
	c.mu.Unlock()
	if !answered {
		c.refused(nil, -1)
	}
}

// fromFar handles the far end's answers to BitVoice's Setup.
func (c *call) fromFar(m *q931) {
	u, err := m.uu()
	if err != nil {
		return
	}
	c.learn(u.fastStart)
	switch m.msgType {
	case q931Alerting:
		c.mu.Lock()
		has, tx := c.peerRTP.IsValid(), c.invite
		c.mu.Unlock()
		code := 180
		if has {
			code = 183
		}
		r := sip.NewResponse(tx.Req, code, "")
		r.Set("To", c.localURI)
		r.Set("Contact", c.tr.contact())
		if has {
			r.Set("Content-Type", "application/sdp")
			r.Body = c.description(c.peerRTP).Bytes()
		}
		tx.Respond(r)
	case q931Connect:
		c.mu.Lock()
		has, tx := c.peerRTP.IsValid(), c.invite
		if has {
			c.answered = true
		}
		c.mu.Unlock()
		if !has {
			c.send(c.msg(q931ReleaseComplete, &uuie{body: bodyReleaseComplete, reason: reasonUndefined}))
			c.refused(nil, reasonUndefined)
			c.conn.Close()
			return
		}
		r := sip.NewResponse(tx.Req, 200, "")
		r.Set("To", c.localURI)
		r.Set("Contact", c.tr.contact())
		r.Set("Content-Type", "application/sdp")
		r.Body = c.description(c.peerRTP).Bytes()
		tx.Respond(r)
	case q931ReleaseComplete:
		c.refused(m.ies[ieCause], u.reason)
	}
}

// learn takes the far end's Fast Connect answer: our sending channel, accepted, says where to send.
func (c *call) learn(fs [][]byte) {
	for _, b := range fs {
		o, err := decodeOLC(b)
		if err != nil || o.reverse || !o.media.IsValid() {
			continue
		}
		c.mu.Lock()
		if !c.peerRTP.IsValid() {
			c.codec, c.peerRTP = o.codec, o.media
		}
		c.mu.Unlock()
	}
}

// refused answers the engine's INVITE with the far end's refusal.
func (c *call) refused(cause []byte, reason int) {
	c.mu.Lock()
	if c.ended || c.answered {
		c.mu.Unlock()
		return
	}
	c.ended = true
	tx := c.invite
	c.mu.Unlock()
	c.tr.mu.Lock()
	delete(c.tr.calls, c.callID)
	c.tr.mu.Unlock()
	r := sip.NewResponse(tx.Req, sipFor(reason, cause), "")
	r.Set("To", c.localURI)
	tx.Respond(r)
	c.conn.Close()
}

// NewToken makes a secret for the engine and its loopback gateways.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
