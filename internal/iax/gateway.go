package iax

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The gateway joins IAX2 trunks to the engine. To the engine, each IAX2 trunk is an ordinary SIP
// trunk at a loopback port of the gateway's own; the gateway turns each call into an IAX2 call and
// back, and passes the audio through unchanged (IAX2 and RTP carry the same codec frames), so the
// engine converts codecs as it does for any trunk.

// TokenHeader carries the secret the engine and the gateway share, so nothing else on this computer
// can place calls on an IAX2 trunk.
const TokenHeader = "X-BitVoice-IAX"

// Trunk is an IAX2 trunk as the gateway needs it.
type Trunk struct {
	ID       string
	Name     string
	Host     string // the far server; empty when it registers to us (then Username names it)
	Port     int    // 4569 when 0
	Username string // our name at the far end, and the far end's name when it calls or registers here
	Secret   string
	Register bool // register to the far end, so it can send calls here
	Formats  []string
}

// codec pairs.
var formatsByName = map[string]uint32{
	"PCMU": FormatULAW, "PCMA": FormatALAW, "GSM": FormatGSM, "G722": FormatG722,
	"G726-32": FormatG726, "AAL2-G726-32": FormatG726AAL2, "G729": FormatG729,
}

func nameOf(format uint32) string {
	for n, f := range formatsByName {
		if f == format {
			return n
		}
	}
	return ""
}

// staticPT is a format's RTP payload number on the loopback side (dynamic ones get 96 to 98).
func staticPT(name string) int {
	switch name {
	case "PCMU":
		return 0
	case "GSM":
		return 3
	case "PCMA":
		return 8
	case "G722":
		return 9
	case "G729":
		return 18
	case "G726-32":
		return 96
	case "AAL2-G726-32":
		return 97
	}
	return -1
}

// samplesOf is the RTP timestamp step for a payload (8 kHz clock; G.722's is 8000 too, RFC 3551).
func samplesOf(name string, n int) uint32 {
	switch name {
	case "GSM":
		return uint32(n / 33 * 160)
	case "G726-32", "AAL2-G726-32":
		return uint32(n * 2)
	case "G729":
		return uint32(n / 10 * 80)
	case "G722":
		return uint32(n)
	}
	return uint32(n)
}

// Gateway is the IAX2 side of the engine.
type Gateway struct {
	node   *Node
	engine netip.AddrPort // the engine's SIP port on this computer
	token  string
	log    *slog.Logger
	ctx    context.Context

	mu     sync.Mutex
	trunks map[string]*gwTrunk
	// where peers that register here are now, by username
	dynamic map[string]netip.AddrPort
}

type gwTrunk struct {
	g      *Gateway
	t      Trunk
	stack  *sip.Stack
	cancel context.CancelFunc
	state  string // registered, failed: <why>, or "" when it does not register
	calls  map[string]*gwCall
	mu     sync.Mutex
}

type gwCall struct {
	tr       *gwTrunk
	iax      *Call
	callID   string
	inbound  bool // came from IAX2
	rtp      *net.UDPConn
	peer     netip.AddrPort // the engine's RTP address
	pt       int            // the loopback payload number
	eventPT  int
	name     string // the codec
	seq      uint16
	ts       uint32
	ssrc     uint32
	answered bool
	ended    bool
	mu       sync.Mutex
	// the SIP dialog with the engine
	invite              *sip.ServerTx // engine called us
	client              *sip.ClientTx // we called the engine
	localURI, remoteURI string
	target              string
	cseq                int
	lastEvent           uint32
}

// NewGateway starts a gateway on node, placing calls to the engine at engine.
func NewGateway(ctx context.Context, node *Node, engine netip.AddrPort, token string, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	g := &Gateway{node: node, engine: engine, token: token, log: log, ctx: ctx, trunks: map[string]*gwTrunk{}, dynamic: map[string]netip.AddrPort{}}
	node.Handle(Handlers{OnNew: g.onNew, OnRegister: g.onRegister, Registered: g.registered})
	return g
}

// Apply puts the IAX2 trunks in force: new ones start, changed ones restart, removed ones stop.
func (g *Gateway) Apply(trunks []Trunk) {
	want := map[string]Trunk{}
	for _, t := range trunks {
		if t.Port == 0 {
			t.Port = 4569
		}
		want[t.ID] = t
	}
	g.mu.Lock()
	var stop []*gwTrunk
	for id, tr := range g.trunks {
		if t, ok := want[id]; !ok || !sameTrunk(t, tr.t) {
			stop = append(stop, tr)
			delete(g.trunks, id)
		}
	}
	var start []Trunk
	for id, t := range want {
		if _, ok := g.trunks[id]; !ok {
			start = append(start, t)
		}
	}
	g.mu.Unlock()
	for _, tr := range stop {
		tr.close()
	}
	for _, t := range start {
		tr, err := g.open(t)
		if err != nil {
			g.log.Error("IAX2 trunk could not start", "trunk", t.Name, "err", err)
			continue
		}
		g.mu.Lock()
		g.trunks[t.ID] = tr
		g.mu.Unlock()
	}
}

func sameTrunk(a, b Trunk) bool {
	return a.Host == b.Host && a.Port == b.Port && a.Username == b.Username && a.Secret == b.Secret &&
		a.Register == b.Register && strings.Join(a.Formats, ",") == strings.Join(b.Formats, ",")
}

// SIPPort is the loopback port that stands for an IAX2 trunk (0 when it is not running).
func (g *Gateway) SIPPort(id string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if tr := g.trunks[id]; tr != nil {
		return tr.stack.Port()
	}
	return 0
}

// Status says how a trunk's registration stands: "registered", "failed: ..." or "".
func (g *Gateway) Status(id string) string {
	g.mu.Lock()
	tr := g.trunks[id]
	g.mu.Unlock()
	if tr == nil {
		return "not running"
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.state
}

// Token is the shared secret the engine must send.
func (g *Gateway) Token() string { return g.token }

func (g *Gateway) open(t Trunk) (*gwTrunk, error) {
	ctx, cancel := context.WithCancel(g.ctx)
	tr := &gwTrunk{g: g, t: t, cancel: cancel, calls: map[string]*gwCall{}}
	tr.stack = sip.NewStack(tr.handle)
	tr.stack.Host = "127.0.0.1"
	if err := tr.stack.Listen("127.0.0.1:0"); err != nil {
		cancel()
		return nil, err
	}
	tr.stack.Serve(ctx)
	if t.Register && t.Host != "" {
		go tr.registerLoop(ctx)
	}
	return tr, nil
}

func (tr *gwTrunk) close() {
	tr.mu.Lock()
	calls := make([]*gwCall, 0, len(tr.calls))
	for _, c := range tr.calls {
		calls = append(calls, c)
	}
	tr.mu.Unlock()
	for _, c := range calls {
		c.end("the trunk was changed", 16, true)
	}
	tr.cancel()
	tr.stack.Close()
}

// farAddr is where the far IAX2 server is: its host, or where it last registered from.
func (tr *gwTrunk) farAddr() (netip.AddrPort, error) {
	if tr.t.Host == "" {
		tr.g.mu.Lock()
		a, ok := tr.g.dynamic[tr.t.Username]
		tr.g.mu.Unlock()
		if !ok {
			return netip.AddrPort{}, errors.New("the far server has not registered")
		}
		return a, nil
	}
	if a, err := netip.ParseAddr(tr.t.Host); err == nil {
		return netip.AddrPortFrom(a.Unmap(), uint16(tr.t.Port)), nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip4", tr.t.Host)
	if err != nil || len(ips) == 0 {
		return netip.AddrPort{}, errors.New("cannot find " + tr.t.Host + " in DNS")
	}
	return netip.AddrPortFrom(ips[0].Unmap(), uint16(tr.t.Port)), nil
}

func (tr *gwTrunk) registerLoop(ctx context.Context) {
	for {
		wait := 60 * time.Second
		addr, err := tr.farAddr()
		if err == nil {
			c := tr.g.node.Register(addr, tr.t.Username, tr.t.Secret, 120)
			state := "failed: no answer"
			timeout := time.After(10 * time.Second)
		loop:
			for {
				select {
				case e, ok := <-c.Events:
					if !ok {
						break loop
					}
					if e.Kind == "registered" {
						state = "registered"
						if e.Code > 20 {
							wait = time.Duration(e.Code-10) * time.Second
						}
						break loop
					}
					if e.Kind == "hangup" {
						state = "failed: " + e.Cause
						wait = 30 * time.Second
						break loop
					}
				case <-timeout:
					wait = 30 * time.Second
					break loop
				case <-ctx.Done():
					return
				}
			}
			tr.mu.Lock()
			tr.state = state
			tr.mu.Unlock()
		} else {
			tr.mu.Lock()
			tr.state = "failed: " + err.Error()
			tr.mu.Unlock()
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

func (g *Gateway) onRegister(user string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, tr := range g.trunks {
		if tr.t.Host == "" && tr.t.Username == user && tr.t.Secret != "" {
			return tr.t.Secret, true
		}
	}
	return "", false
}

func (g *Gateway) registered(user string, from netip.AddrPort, refresh int) {
	g.mu.Lock()
	g.dynamic[user] = from
	g.mu.Unlock()
}

// trunkFor is the trunk an incoming IAX2 call belongs to: the one whose server it comes from, or the
// registered peer with its username.
func (g *Gateway) trunkFor(c *Call) *gwTrunk {
	g.mu.Lock()
	defer g.mu.Unlock()
	var byAddr *gwTrunk
	for _, tr := range g.trunks {
		if tr.t.Host == "" {
			if a, ok := g.dynamic[tr.t.Username]; ok && a.Addr() == c.Peer().Addr() && (c.Username() == "" || c.Username() == tr.t.Username) {
				return tr
			}
			continue
		}
		if a, err := netip.ParseAddr(tr.t.Host); err == nil && a.Unmap() == c.Peer().Addr() {
			if c.Username() == tr.t.Username {
				return tr
			}
			byAddr = tr
		}
	}
	return byAddr
}

// onNew takes a call from an IAX2 server: check it, accept it with a codec, then call the engine.
func (g *Gateway) onNew(c *Call, ies IEs) {
	tr := g.trunkFor(c)
	if tr == nil {
		c.Reject("no trunk here for this server", 21)
		return
	}
	go tr.incoming(c, ies)
}

func (tr *gwTrunk) allowed() []string {
	if len(tr.t.Formats) > 0 {
		return tr.t.Formats
	}
	return []string{"PCMU", "PCMA", "G722", "GSM", "G726-32", "G729"}
}

// choose picks the call's codec: the caller's preferred one when this trunk allows it, else the first
// allowed one the caller can do.
func (tr *gwTrunk) choose(preferred, capability uint32) uint32 {
	for _, n := range tr.allowed() {
		if f := formatsByName[n]; f == preferred {
			return f
		}
	}
	for _, n := range tr.allowed() {
		if f := formatsByName[n]; f != 0 && capability&f != 0 {
			return f
		}
	}
	return 0
}

func (tr *gwTrunk) incoming(c *Call, ies IEs) {
	if tr.t.Secret != "" {
		c.Authenticate(tr.t.Secret)
		select {
		case e, ok := <-c.Events:
			if !ok || e.Kind != "authenticated" {
				return
			}
		case <-time.After(10 * time.Second):
			c.Reject("no answer to the password challenge", 21)
			return
		}
	}
	pref, _ := ies.U32(IEFormat)
	capab, _ := ies.U32(IECapability)
	format := tr.choose(pref, capab|pref)
	if format == 0 {
		c.Reject("no codec in common", 58)
		return
	}
	c.Accept(format)
	gc, err := tr.newCall(c, true, nameOf(format))
	if err != nil {
		c.Hangup("no media port", 34)
		return
	}
	called := ies.Str(IECalledNumber)
	if called == "" {
		called = ies.Str(IEDNID)
	}
	from := ies.Str(IECallingNumber)
	name := ies.Str(IECallingName)
	gc.callID = sip.NewCallID()
	gc.localURI = "<sip:" + sipUser(from) + "@127.0.0.1>;tag=" + sip.NewTag()
	if name != "" {
		gc.localURI = strconv.Quote(name) + " " + gc.localURI
	}
	gc.remoteURI = "<sip:" + sipUser(called) + "@127.0.0.1>"
	gc.cseq = 1
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + sipUser(called) + "@" + tr.g.engine.String()}
	inv.Set("From", gc.localURI)
	inv.Set("To", gc.remoteURI)
	inv.Set("Call-ID", gc.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", tr.contact())
	inv.Set(TokenHeader, tr.g.token)
	inv.Set("Content-Type", "application/sdp")
	inv.Body = gc.description().Bytes()
	tr.add(gc)
	go gc.iaxEvents()
	tx := tr.stack.Request(inv, sip.Peer{Transport: "UDP", Addr: tr.g.engine})
	gc.mu.Lock()
	gc.client = tx
	gc.mu.Unlock()
	var fin *sip.Message
	for r := range tx.Responses {
		if r.StatusCode == 180 || r.StatusCode == 183 {
			c.Ringing()
		}
		if r.StatusCode >= 200 {
			fin = r
		}
	}
	if fin == nil || fin.StatusCode >= 300 {
		code, why := 34, "BitVoice did not answer"
		if fin != nil {
			code, why = causeFor(fin.StatusCode), strconv.Itoa(fin.StatusCode)+" "+fin.Reason
		}
		gc.end(why, code, false)
		return
	}
	gc.mu.Lock()
	gc.remoteURI = fin.Get("To")
	if ct, err := sip.ParseAddress(fin.Get("Contact")); err == nil && ct.URI != nil {
		gc.target = ct.URI.String()
	}
	gc.answered = true
	gc.mu.Unlock()
	if s, err := sdp.Parse(fin.Body); err == nil && s.Audio() != nil {
		gc.setRemote(s)
	}
	n, _ := fin.CSeq()
	ack := gc.request(sip.ACK)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = tr.stack.Stateless(ack, sip.Peer{Transport: "UDP", Addr: tr.g.engine})
	tx.SetAck(ack)
	c.Answer()
}

// sipUser keeps a number SIP can carry.
func sipUser(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '*' || r == '#' || r == '+' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, s)
	if s == "" {
		return "anonymous"
	}
	return s
}

// causeFor turns a SIP failure into the Q.850 cause IAX2 carries.
func causeFor(code int) int {
	switch code {
	case 404, 484:
		return 1
	case 486, 600:
		return 17
	case 480, 408:
		return 18
	case 403, 603:
		return 21
	case 488:
		return 58
	}
	return 34
}

// sipFor turns an IAX2 cause into a SIP failure.
func sipFor(code int) int {
	switch code {
	case 1:
		return 404
	case 17:
		return 486
	case 18, 19:
		return 480
	case 21:
		return 403
	case 58:
		return 488
	}
	return 503
}

func (tr *gwTrunk) contact() string {
	return "<sip:iax@127.0.0.1:" + strconv.Itoa(tr.stack.Port()) + ">"
}

func (tr *gwTrunk) add(c *gwCall) {
	tr.mu.Lock()
	tr.calls[c.callID] = c
	tr.mu.Unlock()
}

func (tr *gwTrunk) newCall(c *Call, inbound bool, name string) (*gwCall, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	gc := &gwCall{tr: tr, iax: c, inbound: inbound, rtp: conn, name: name, pt: staticPT(name), eventPT: 101,
		seq: uint16(rand.Uint32()), ts: rand.Uint32(), ssrc: rand.Uint32()}
	go gc.readRTP()
	return gc, nil
}

// description is the loopback side's SDP: the call's codec and key presses.
func (gc *gwCall) description() *sdp.Session {
	port := gc.rtp.LocalAddr().(*net.UDPAddr).Port
	return sdp.New("127.0.0.1", port, uint64(time.Now().Unix()), []sdp.Codec{{PT: gc.pt, Name: gc.name, ClockRate: 8000}}, gc.eventPT)
}

func (gc *gwCall) setRemote(s *sdp.Session) {
	a, err := netip.ParseAddr(s.Connection(s.Audio()))
	if err != nil {
		return
	}
	gc.mu.Lock()
	gc.peer = netip.AddrPortFrom(a, uint16(s.Audio().Port))
	for _, c := range s.Audio().Codecs() {
		if strings.EqualFold(c.Name, "telephone-event") {
			gc.eventPT = c.PT
		} else if strings.EqualFold(c.Name, gc.name) {
			gc.pt = c.PT // the engine's number for our codec
		}
	}
	gc.mu.Unlock()
}

// iaxEvents passes what the IAX2 side does to the SIP side.
func (gc *gwCall) iaxEvents() {
	for e := range gc.iax.Events {
		switch e.Kind {
		case "voice":
			gc.sendRTP(e.Audio)
		case "dtmf":
			gc.sendKey(e.Digit)
		case "ringing":
			gc.mu.Lock()
			tx := gc.invite
			gc.mu.Unlock()
			if tx != nil && !gc.inbound {
				r := sip.NewResponse(tx.Req, 180, "")
				r.Set("To", gc.localURI)
				r.Set("Contact", gc.tr.contact())
				tx.Respond(r)
			}
		case "answered":
			if !gc.inbound {
				gc.answerEngine()
			}
		case "hangup":
			gc.end(e.Cause, e.Code, false)
			return
		}
	}
	gc.end("the IAX2 call ended", 16, false)
}

func (gc *gwCall) sendRTP(payload []byte) {
	gc.mu.Lock()
	peer, pt := gc.peer, gc.pt
	if !peer.IsValid() || gc.ended {
		gc.mu.Unlock()
		return
	}
	pkt := make([]byte, 12, 12+len(payload))
	pkt[0], pkt[1] = 0x80, byte(pt)
	binary.BigEndian.PutUint16(pkt[2:], gc.seq)
	binary.BigEndian.PutUint32(pkt[4:], gc.ts)
	binary.BigEndian.PutUint32(pkt[8:], gc.ssrc)
	gc.seq++
	gc.ts += samplesOf(gc.name, len(payload))
	gc.mu.Unlock()
	_, _ = gc.rtp.WriteToUDPAddrPort(append(pkt, payload...), peer)
}

// sendKey sends a key press as RFC 4733 events: three end packets, as phones do.
func (gc *gwCall) sendKey(d byte) {
	ev := strings.IndexByte("0123456789*#ABCD", d)
	if ev < 0 {
		return
	}
	gc.mu.Lock()
	peer, ept, ts := gc.peer, gc.eventPT, gc.ts
	gc.ts += 800
	gc.mu.Unlock()
	if !peer.IsValid() {
		return
	}
	for i := range 3 {
		gc.mu.Lock()
		seq := gc.seq
		gc.seq++
		gc.mu.Unlock()
		p := make([]byte, 16)
		p[0], p[1] = 0x80, byte(ept)
		if i == 0 {
			p[1] |= 0x80
		}
		binary.BigEndian.PutUint16(p[2:], seq)
		binary.BigEndian.PutUint32(p[4:], ts)
		binary.BigEndian.PutUint32(p[8:], gc.ssrc)
		p[12], p[13] = byte(ev), 0x80|10
		binary.BigEndian.PutUint16(p[14:], 800)
		_, _ = gc.rtp.WriteToUDPAddrPort(p, peer)
	}
}

// readRTP passes the engine's audio to the IAX2 side, and its key presses as IAX2 key frames.
func (gc *gwCall) readRTP() {
	buf := make([]byte, 2048)
	for {
		n, from, err := gc.rtp.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		if n < 12 || buf[0]>>6 != 2 {
			continue
		}
		gc.mu.Lock()
		if !gc.peer.IsValid() {
			gc.peer = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		}
		ept := gc.eventPT
		gc.mu.Unlock()
		head := 12 + int(buf[0]&0x0F)*4
		if head > n {
			continue
		}
		pt := int(buf[1] & 0x7F)
		if pt == ept {
			if n-head >= 4 && buf[head+1]&0x80 != 0 {
				ts := binary.BigEndian.Uint32(buf[4:])
				gc.mu.Lock()
				dup := ts == gc.lastEvent
				gc.lastEvent = ts
				gc.mu.Unlock()
				if !dup && int(buf[head]) < 16 {
					gc.iax.SendDTMF("0123456789*#ABCD"[buf[head]])
				}
			}
			continue
		}
		gc.iax.SendVoice(append([]byte(nil), buf[head:n]...))
	}
}

// handle receives the engine's requests on a trunk's loopback port.
func (tr *gwTrunk) handle(tx *sip.ServerTx, in *sip.Incoming) {
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
	gc := tr.calls[m.CallID()]
	tr.mu.Unlock()
	switch m.Method {
	case sip.INVITE:
		if inDialog {
			if gc == nil {
				tx.Respond(sip.NewResponse(m, 481, ""))
				return
			}
			// A re-INVITE (hold, a transfer): the audio stays on the same ports.
			if s, err := sdp.Parse(m.Body); err == nil && s.Audio() != nil {
				gc.setRemote(s)
			}
			r := sip.NewResponse(m, 200, "")
			r.Set("Contact", tr.contact())
			r.Set("Content-Type", "application/sdp")
			r.Body = gc.description().Bytes()
			tx.Respond(r)
			return
		}
		if m.Get(TokenHeader) != tr.g.token {
			tx.Respond(sip.NewResponse(m, 403, "Not The Engine"))
			return
		}
		go tr.outgoing(tx, m)
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if gc != nil && !gc.answered {
			gc.end("the caller hung up", 16, true)
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if gc != nil {
			gc.mu.Lock()
			gc.answered = false // the engine has ended its side: no BYE back
			gc.mu.Unlock()
			gc.end("the other party hung up", 16, true)
		}
	case sip.ACK:
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// outgoing places the engine's call on the IAX2 trunk.
func (tr *gwTrunk) outgoing(tx *sip.ServerTx, m *sip.Message) {
	offer, err := sdp.Parse(m.Body)
	if err != nil || offer.Audio() == nil {
		tx.Respond(sip.NewResponse(m, 488, ""))
		return
	}
	allowed := map[string]bool{}
	for _, n := range tr.allowed() {
		allowed[n] = true
	}
	var capab, pref uint32
	for _, c := range offer.Audio().Codecs() {
		n := strings.ToUpper(c.Name)
		if f := formatsByName[n]; f != 0 && allowed[n] {
			capab |= f
			if pref == 0 {
				pref = f
			}
		}
	}
	if pref == 0 {
		tx.Respond(sip.NewResponse(m, 488, "No Codec The IAX2 Trunk Allows"))
		return
	}
	far, err := tr.farAddr()
	if err != nil {
		tx.Respond(sip.NewResponse(m, 503, err.Error()))
		return
	}
	from, _ := sip.ParseAddress(m.Get("From"))
	called := ""
	if u, err := sip.ParseURI(m.RequestURI); err == nil {
		called = u.User
	}
	p := DialParams{Called: called, Username: tr.t.Username, Secret: tr.t.Secret, Formats: capab, Preferred: pref}
	if from != nil && from.URI != nil {
		p.Calling, p.CallingName = from.URI.User, from.Display
	}
	c := tr.g.node.Dial(far, p)
	// Wait to be accepted with a codec, then answer the engine with it.
	var format uint32
	select {
	case e, ok := <-c.Events:
		switch {
		case !ok:
			tx.Respond(sip.NewResponse(m, 503, ""))
			return
		case e.Kind == "accepted":
			format = e.Format
		case e.Kind == "hangup":
			r := sip.NewResponse(m, sipFor(e.Code), e.Cause)
			tx.Respond(r)
			return
		}
	case <-time.After(15 * time.Second):
		c.Hangup("no answer", 18)
		tx.Respond(sip.NewResponse(m, 408, ""))
		return
	}
	name := nameOf(format)
	if name == "" {
		c.Hangup("codec not offered", 58)
		tx.Respond(sip.NewResponse(m, 488, ""))
		return
	}
	gc, err := tr.newCall(c, false, name)
	if err != nil {
		c.Hangup("no media port", 34)
		tx.Respond(sip.NewResponse(m, 503, ""))
		return
	}
	// Use the engine's own number for the codec.
	for _, cd := range offer.Audio().Codecs() {
		if strings.EqualFold(cd.Name, name) {
			gc.pt = cd.PT
		}
		if strings.EqualFold(cd.Name, "telephone-event") {
			gc.eventPT = cd.PT
		}
	}
	gc.callID = m.CallID()
	gc.invite = tx
	gc.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	gc.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		gc.target = ct.URI.String()
	}
	gc.cseq = 1000
	gc.setRemote(offer)
	tr.add(gc)
	tx.NoAck = func() { gc.end("BitVoice did not confirm the answer", 41, true) }
	r := sip.NewResponse(m, 183, "")
	r.Set("To", gc.localURI)
	r.Set("Contact", tr.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = gc.description().Bytes() // early media: ringback and announcements from the far end
	tx.Respond(r)
	go gc.iaxEvents()
}

// answerEngine answers the engine's call once the IAX2 call is answered.
func (gc *gwCall) answerEngine() {
	gc.mu.Lock()
	if gc.answered || gc.ended || gc.invite == nil {
		gc.mu.Unlock()
		return
	}
	gc.answered = true
	tx := gc.invite
	gc.mu.Unlock()
	r := sip.NewResponse(tx.Req, 200, "")
	r.Set("To", gc.localURI)
	r.Set("Contact", gc.tr.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = gc.description().Bytes()
	tx.Respond(r)
}

func (gc *gwCall) request(method string) *sip.Message {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	target := gc.target
	if target == "" {
		target = "sip:" + gc.tr.g.engine.String()
	}
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: target}
	m.Set("From", gc.localURI)
	m.Set("To", gc.remoteURI)
	m.Set("Call-ID", gc.callID)
	if method != sip.ACK {
		gc.cseq++
	}
	m.Set("CSeq", strconv.Itoa(gc.cseq)+" "+method)
	m.Set("Contact", gc.tr.contact())
	return m
}

// end finishes both sides. fromSIP: the engine ended it, so only the IAX2 side is told.
func (gc *gwCall) end(cause string, code int, fromSIP bool) {
	gc.mu.Lock()
	if gc.ended {
		gc.mu.Unlock()
		return
	}
	gc.ended = true
	answered, invite, client, inbound := gc.answered, gc.invite, gc.client, gc.inbound
	gc.mu.Unlock()
	gc.tr.mu.Lock()
	delete(gc.tr.calls, gc.callID)
	gc.tr.mu.Unlock()
	if fromSIP {
		gc.iax.Hangup(cause, code)
	} else {
		peer := sip.Peer{Transport: "UDP", Addr: gc.tr.g.engine}
		switch {
		case answered:
			go func() {
				for range gc.tr.stack.Request(gc.request(sip.BYE), peer).Responses {
				}
			}()
		case !inbound && invite != nil:
			r := sip.NewResponse(invite.Req, sipFor(code), cause)
			r.Set("To", gc.localURI)
			invite.Respond(r)
		case inbound && client != nil:
			client.Cancel()
		}
	}
	gc.rtp.Close()
}

// Service opens the IAX2 port when the first IAX2 trunk appears and keeps the gateway's trunks in step
// with the configuration. Until then nothing listens on 4569, so an Asterisk on the same computer
// keeps it.
type Service struct {
	Addr   string // ":4569"
	Engine func() netip.AddrPort
	Log    *slog.Logger

	mu    sync.Mutex
	ctx   context.Context
	node  *Node
	gw    *Gateway
	token string
	err   error
}

// NewService makes the service; Apply starts it.
func NewService(ctx context.Context, addr string, engine func() netip.AddrPort, log *slog.Logger) *Service {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(rand.Uint32())
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Addr: addr, Engine: engine, Log: log, ctx: ctx, token: hexOf(b)}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2*len(b))
	for i, v := range b {
		out[2*i], out[2*i+1] = digits[v>>4], digits[v&15]
	}
	return string(out)
}

// Apply puts the IAX2 trunks in force.
func (s *Service) Apply(trunks []Trunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(trunks) == 0 && s.node == nil {
		return
	}
	if s.node == nil {
		n, err := Listen(s.ctx, s.Addr, s.Log)
		if err != nil {
			if s.err == nil {
				s.Log.Error("IAX2 trunks are off: the IAX2 port cannot be opened", "addr", s.Addr, "err", err)
			}
			s.err = err
			return
		}
		s.node, s.err = n, nil
		s.gw = NewGateway(s.ctx, n, s.Engine(), s.token, s.Log)
		s.Log.Info("IAX2 is on", "port", n.Port())
	}
	s.gw.Apply(trunks)
}

// SIPPort is the loopback port standing for a trunk.
func (s *Service) SIPPort(id string) int {
	s.mu.Lock()
	gw := s.gw
	s.mu.Unlock()
	if gw == nil {
		return 0
	}
	return gw.SIPPort(id)
}

// Token is the secret the engine sends the gateway.
func (s *Service) Token() string { return s.token }

// Status is a trunk's state.
func (s *Service) Status(id string) string {
	s.mu.Lock()
	gw := s.gw
	s.mu.Unlock()
	if gw == nil {
		return "not running"
	}
	return gw.Status(id)
}

// Port is where IAX2 listens, 0 when it does not.
func (s *Service) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.node == nil {
		return 0
	}
	return s.node.Port()
}

// Close stops it.
func (s *Service) Close() {
	s.mu.Lock()
	n := s.node
	s.mu.Unlock()
	if n != nil {
		n.Close()
	}
}
