package mgcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

// Bridge is the engine side: the loopback gateway lines register through, and the media pool.
type Bridge interface {
	OpenAs(kind, agent, id, ext, secret, client string) (*webphone.Phone, error)
}

// Media supplies RTP endpoints.
type Media interface {
	NewMediaEndpoint() (*media.Endpoint, error)
}

// DigitMap is what the gateway collects before it reports a number: digits until a pause or #.
const DigitMap = "(*x.T|x.T|x.#|*x.#)"

// Agent is the call agent: one UDP socket for every gateway.
type Agent struct {
	ctx    context.Context
	bridge Bridge
	media  Media
	secret func(ext string) string
	log    *slog.Logger
	// Retry is the first wait before a command is sent again (doubling, four tries).
	Retry time.Duration

	mu       sync.Mutex
	conn     *net.UDPConn
	port     int
	gateways map[string]*gateway
	pending  map[string]chan *Message
	answered map[string][]byte // responses already sent, by peer and transaction, for repeated commands
	nextTx   uint32
}

type gateway struct {
	cfg   config.MGCPGateway
	addr  netip.AddrPort // where commands go
	lines map[string]*line
}

type lineState int

const (
	idle lineState = iota
	dialTone
	calling  // our call is being set up
	ringing  // a call is ringing the port
	talking  // answered
	finished // the call ended, the handset is still off hook
)

type line struct {
	a        *Agent
	gw       *gateway
	endpoint string // aaln/1
	ext      string
	phone    *webphone.Phone

	mu      sync.Mutex
	state   lineState
	call    *webphone.Call
	callID  string // MGCP call id
	connID  string
	rtp     *media.Endpoint
	req     int
	noCI    bool // the gateway refused caller ID
	pending *webphone.Event
	refused string // why the gateway last refused to watch the port, for the status
}

// LongTran is how long to wait after a provisional response before sending a command again.
var LongTran = 5 * time.Second

// New makes an agent; Apply opens its port.
func New(ctx context.Context, b Bridge, m Media, secret func(ext string) string, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	return &Agent{ctx: ctx, bridge: b, media: m, secret: secret, log: log, Retry: 500 * time.Millisecond,
		gateways: map[string]*gateway{}, pending: map[string]chan *Message{}, answered: map[string][]byte{}}
}

// Port is the agent's UDP port (0 when closed).
func (a *Agent) Port() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.port
}

// Apply puts the gateways in force; with MGCP off the port closes.
func (a *Agent) Apply(c config.MGCP) {
	a.mu.Lock()
	old := a.gateways
	a.gateways = map[string]*gateway{}
	conn := a.conn
	want := c.Port
	if want == 0 {
		want = 2727
	}
	if !c.Enabled || len(c.Gateways) == 0 || (conn != nil && a.port != want && c.Port != 0) {
		if conn != nil {
			conn.Close()
			a.conn, a.port, conn = nil, 0, nil
		}
	}
	a.mu.Unlock()
	for _, g := range old {
		for _, l := range g.lines {
			l.close()
		}
	}
	if !c.Enabled || len(c.Gateways) == 0 {
		return
	}
	if conn == nil {
		uc, err := net.ListenUDP("udp", &net.UDPAddr{Port: want})
		if err != nil {
			a.log.Error("mgcp: cannot open the call agent's port", "port", want, "err", err)
			return
		}
		a.mu.Lock()
		a.conn, a.port = uc, uc.LocalAddr().(*net.UDPAddr).Port
		a.mu.Unlock()
		go a.read(uc)
	}
	for _, gc := range c.Gateways {
		g := &gateway{cfg: gc, lines: map[string]*line{}}
		if gc.Host != "" {
			port := gc.Port
			if port == 0 {
				port = 2427
			}
			if ap, err := resolve(gc.Host, port); err == nil {
				g.addr = ap
			}
		}
		a.mu.Lock()
		a.gateways[gc.Name] = g
		a.mu.Unlock()
		for _, lc := range gc.Lines {
			l := &line{a: a, gw: g, endpoint: lc.Endpoint, ext: lc.Extension}
			g.lines[lc.Endpoint] = l
			go l.open()
		}
	}
}

// Close stops every line and the port.
func (a *Agent) Close() { a.Apply(config.MGCP{}) }

func resolve(host string, port int) (netip.AddrPort, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return netip.AddrPort{}, fmt.Errorf("mgcp: cannot find %s", host)
	}
	ip, _ := netip.AddrFromSlice(ips[0])
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
}

// Status is each line's state, for the GUI: "registered", "idle", "in a call" or why not.
func (a *Agent) Status() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]string{}
	for name, g := range a.gateways {
		for ep, l := range g.lines {
			l.mu.Lock()
			s := "waiting for the gateway"
			switch {
			case l.phone == nil:
				s = "not registered with BitVoice"
			case !g.addr.IsValid():
			case l.refused != "":
				s = l.refused
			case l.state == idle:
				s = "ready"
			default:
				s = "in a call"
			}
			l.mu.Unlock()
			out[ep+"@"+name] = s
		}
	}
	return out
}

func (a *Agent) read(conn *net.UDPConn) {
	buf := make([]byte, 8192)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		msgs, err := Parse(buf[:n])
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if m.Verb == "" {
				a.mu.Lock()
				ch := a.pending[m.TxID]
				a.mu.Unlock()
				if ch != nil && m.Code >= 100 {
					select {
					case ch <- m:
					default:
					}
				}
				// A final response after a provisional one carries an empty ResponseAck (K): answer
				// it with a response acknowledgement, 000, so the gateway stops sending it again
				// (RFC 3435 3.5.6).
				if m.Code >= 200 && m.Has("K") {
					_, _ = conn.WriteToUDPAddrPort((&Message{Code: 0, TxID: m.TxID, Comment: "OK"}).Bytes(), from)
				}
				continue
			}
			a.command(conn, from, m)
		}
	}
}

func (a *Agent) respond(conn *net.UDPConn, to netip.AddrPort, m *Message, code int) {
	r := &Message{Code: code, TxID: m.TxID}
	b := r.Bytes()
	a.mu.Lock()
	if len(a.answered) > 2000 {
		a.answered = map[string][]byte{}
	}
	a.answered[to.String()+"/"+m.TxID] = b
	a.mu.Unlock()
	_, _ = conn.WriteToUDPAddrPort(b, to)
}

// command handles what a gateway sends: restarts (RSIP), events (NTFY), and the rest politely.
func (a *Agent) command(conn *net.UDPConn, from netip.AddrPort, m *Message) {
	a.mu.Lock()
	if b, ok := a.answered[from.String()+"/"+m.TxID]; ok {
		a.mu.Unlock()
		_, _ = conn.WriteToUDPAddrPort(b, from) // a repeat: the same answer
		return
	}
	local, domain, _ := strings.Cut(strings.ToLower(m.Endpoint), "@")
	domain, _, _ = strings.Cut(domain, ":")
	domain = strings.Trim(domain, "[]")
	g := a.gateways[domain]
	if g == nil {
		a.mu.Unlock()
		a.respond(conn, from, m, 500) // unknown endpoint
		return
	}
	switch {
	case g.cfg.Host != "" && g.addr.IsValid() && from.Addr() != g.addr.Addr():
		// A gateway given an address speaks only from it: MGCP has no passwords.
		a.mu.Unlock()
		return
	case g.cfg.Host == "" && !(from.Addr().IsPrivate() || from.Addr().IsLoopback() || from.Addr().IsLinkLocalUnicast()):
		// Without an address, only from the local network.
		a.mu.Unlock()
		return
	case g.cfg.Host == "" || !g.addr.IsValid():
		g.addr = from
	}
	a.mu.Unlock()
	switch m.Verb {
	case "RSIP":
		a.respond(conn, from, m, 200)
		// The gateway (re)started: every line it names is idle again and watched for off-hook.
		for ep, l := range g.lines {
			if local == "*" || local == ep || strings.HasSuffix(local, "*") && strings.HasPrefix(ep, strings.TrimSuffix(local, "*")) {
				go l.reset()
			}
		}
	case "NTFY":
		a.respond(conn, from, m, 200)
		if l := g.lines[local]; l != nil {
			go l.notify(m.Get("X"), Events(m.Get("O")))
		}
	case "DLCX":
		a.respond(conn, from, m, 250)
		if l := g.lines[local]; l != nil {
			go l.gatewayDropped()
		}
	default:
		a.respond(conn, from, m, 200)
	}
}

// send runs a command to a gateway and waits for its final response.
func (a *Agent) send(g *gateway, m *Message) (*Message, error) {
	a.mu.Lock()
	conn, to := a.conn, g.addr
	a.nextTx = a.nextTx%999999998 + 1
	m.TxID = strconv.FormatUint(uint64(a.nextTx), 10)
	ch := make(chan *Message, 1)
	a.pending[m.TxID] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, m.TxID)
		a.mu.Unlock()
	}()
	if conn == nil || !to.IsValid() {
		return nil, fmt.Errorf("mgcp: gateway %s has not been heard from", g.cfg.Name)
	}
	b := m.Bytes()
	wait := a.Retry
	provisional := false
	for try := 0; try < 4; try++ {
		if _, err := conn.WriteToUDPAddrPort(b, to); err != nil {
			return nil, err
		}
	waiting:
		for {
			select {
			case r := <-ch:
				if r.Code < 200 {
					// Being worked on: wait longer before sending it again (LONGTRAN-TIMER, 5 s).
					provisional, wait = true, LongTran
					continue
				}
				if r.Code >= 300 {
					return r, fmt.Errorf("mgcp: %s refused: %d %s", m.Verb, r.Code, r.Comment)
				}
				return r, nil
			case <-time.After(wait):
				if !provisional {
					wait *= 2
				}
				break waiting
			case <-a.ctx.Done():
				return nil, a.ctx.Err()
			}
		}
	}
	return nil, fmt.Errorf("mgcp: gateway %s did not answer %s", g.cfg.Name, m.Verb)
}

func (a *Agent) addrOf(g *gateway) netip.AddrPort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return g.addr
}

// localFor is the address of this computer the gateway reaches it on.
func localFor(to netip.AddrPort) string {
	c, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(to))
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func (l *line) name() string { return l.endpoint + "@" + l.gw.cfg.Name }

func (l *line) open() {
	id := strings.NewReplacer("/", "-", "@", "-", "*", "x", "$", "x").Replace(l.gw.cfg.Name + "-" + l.endpoint)
	p, err := l.a.bridge.OpenAs("mgcp", "BitVoice MGCP line", id, l.ext, l.a.secret(l.ext), "")
	if err != nil {
		l.a.log.Warn("mgcp: line not registered", "line", l.name(), "err", err)
		return
	}
	l.mu.Lock()
	l.phone = p
	l.mu.Unlock()
	go l.events(p)
	l.reset()
}

func (l *line) close() {
	l.mu.Lock()
	p, c, ep := l.phone, l.call, l.rtp
	l.phone, l.call, l.rtp = nil, nil, nil
	l.mu.Unlock()
	if c != nil {
		c.Hangup()
	}
	if ep != nil {
		ep.Close()
	}
	if p != nil {
		p.Close()
	}
}

// rqnt asks the gateway to watch for events and play signals on the port.
func (l *line) rqnt(watch, signals string) error {
	l.mu.Lock()
	l.req++
	x := strconv.FormatInt(int64(l.req), 16)
	l.mu.Unlock()
	m := &Message{Verb: "RQNT", Endpoint: l.name()}
	m.Set("X", x)
	if to := l.a.addrOf(l.gw); to.IsValid() {
		m.Set("N", "ca@"+localFor(to)+":"+strconv.Itoa(l.a.Port()))
	}
	m.Set("R", watch)
	if signals != "" {
		m.Set("S", signals)
	}
	if strings.Contains(watch, "D/") {
		m.Set("D", DigitMap)
	}
	r, err := l.a.send(l.gw, m)
	l.mu.Lock()
	switch {
	case r != nil && r.Code >= 300:
		c := r.Comment
		if c == "" {
			c = reason(r.Code)
		}
		l.refused = fmt.Sprintf("the gateway refused to watch the port: %d %s", r.Code, c)
	case err == nil:
		l.refused = ""
	}
	l.mu.Unlock()
	return err
}

// reset makes the port idle: no connection, no signal, waiting for the handset to lift.
func (l *line) reset() {
	l.drop()
	l.mu.Lock()
	l.state = idle
	l.mu.Unlock()
	_ = l.rqnt("L/hd(N)", "")
}

// drop ends this port's call and connection, if any.
func (l *line) drop() {
	l.mu.Lock()
	c, conn, callID, ep := l.call, l.connID, l.callID, l.rtp
	l.call, l.connID, l.callID, l.rtp = nil, "", "", nil
	l.mu.Unlock()
	if c != nil {
		c.Hangup()
	}
	if conn != "" {
		m := &Message{Verb: "DLCX", Endpoint: l.name()}
		m.Set("C", callID)
		m.Set("I", conn)
		_, _ = l.a.send(l.gw, m)
	}
	if ep != nil {
		ep.Close()
	}
}

// connect creates the port's connection to a new endpoint of ours, both ways at once.
func (l *line) connect() (*media.Endpoint, error) {
	ep, err := l.a.media.NewMediaEndpoint()
	if err != nil {
		return nil, err
	}
	callID := strings.ToUpper(strconv.FormatInt(time.Now().UnixNano()&0xffffffffff, 16))
	ours := sdp.New(localFor(l.a.addrOf(l.gw)), ep.Port(), uint64(time.Now().Unix()), []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101)
	m := &Message{Verb: "CRCX", Endpoint: l.name(), SDP: ours.Bytes()}
	m.Set("C", callID)
	m.Set("L", "p:20, a:PCMU")
	m.Set("M", "sendrecv")
	r, err := l.a.send(l.gw, m)
	if err != nil {
		ep.Close()
		return nil, err
	}
	if s, err := sdp.Parse(r.SDP); err == nil && s.Audio() != nil {
		ep.SetRemote(s.Connection(s.Audio()), s.Audio().Port)
	}
	ep.Start()
	l.mu.Lock()
	l.connID, l.callID, l.rtp = r.Get("I"), callID, ep
	l.mu.Unlock()
	return ep, nil
}

// notify acts on what the port reports.
func (l *line) notify(_ string, events []string) {
	var digits strings.Builder
	for _, e := range events {
		switch {
		case e == "hd":
			l.offHook()
		case e == "hu":
			l.onHook()
		case e == "hf":
			// A hook flash: nothing yet (transfer is done from the extension's other phones).
		case len(e) == 1 && strings.ContainsAny(e, "0123456789*#"):
			digits.WriteString(e)
		}
	}
	if digits.Len() > 0 {
		l.dial(strings.TrimSuffix(digits.String(), "#"))
	}
}

func (l *line) offHook() {
	l.mu.Lock()
	st, c := l.state, l.call
	l.mu.Unlock()
	switch st {
	case idle:
		l.mu.Lock()
		l.state = dialTone
		l.mu.Unlock()
		_ = l.rqnt("L/hu(N), L/hf(N), D/[0-9#*T](D)", "L/dl")
	case ringing:
		ep, err := l.connect()
		if err != nil || c == nil || c.Answer(ep, 0, 101) != nil {
			l.a.log.Warn("mgcp: could not answer", "line", l.name(), "err", err)
			l.mu.Lock()
			l.state = finished
			l.mu.Unlock()
			l.drop()
			_ = l.rqnt("L/hu(N)", "L/ro")
			return
		}
		l.mu.Lock()
		l.state = talking
		l.mu.Unlock()
		_ = l.rqnt("L/hu(N), L/hf(N)", "")
	}
}

func (l *line) onHook() {
	l.reset()
}

func (l *line) gatewayDropped() {
	l.mu.Lock()
	l.connID = ""
	l.mu.Unlock()
	l.drop()
}

func (l *line) dial(number string) {
	l.mu.Lock()
	if l.state != dialTone || l.phone == nil {
		l.mu.Unlock()
		return
	}
	l.state = calling
	p := l.phone
	l.mu.Unlock()
	ep, err := l.connect()
	var c *webphone.Call
	if err == nil {
		c, err = p.Dial(number, ep, 0, 101)
	}
	if err != nil {
		l.a.log.Warn("mgcp: call not placed", "line", l.name(), "err", err)
		l.mu.Lock()
		l.state = finished
		l.mu.Unlock()
		l.drop()
		_ = l.rqnt("L/hu(N)", "L/ro")
		return
	}
	l.mu.Lock()
	l.call = c
	l.mu.Unlock()
	_ = l.rqnt("L/hu(N), L/hf(N)", "")
}

// events follows the engine's side of the port's calls.
func (l *line) events(p *webphone.Phone) {
	for {
		select {
		case <-l.a.ctx.Done():
			return
		case ev := <-p.Events:
			l.event(p, ev)
		}
	}
}

func (l *line) event(p *webphone.Phone, ev webphone.Event) {
	l.mu.Lock()
	st, mine := l.state, l.call != nil && l.call.ID == ev.CallID
	l.mu.Unlock()
	switch ev.Type {
	case "incoming":
		c := p.Find(ev.CallID)
		if st != idle || c == nil {
			if c != nil {
				c.Hangup() // busy: the extension's other phones still ring
			}
			return
		}
		l.mu.Lock()
		l.state, l.call = ringing, c
		noCI := l.noCI
		l.mu.Unlock()
		signals := "L/rg"
		if !noCI {
			signals += ", L/ci(" + time.Now().Format("01/02/15/04") + "," + quote(ev.From) + "," + quote(ev.FromName) + ")"
		}
		if err := l.rqnt("L/hd(N)", signals); err != nil && !noCI {
			// Some gateways do not take caller ID: ring without it, and never ask again.
			l.mu.Lock()
			l.noCI = true
			l.mu.Unlock()
			_ = l.rqnt("L/hd(N)", "L/rg")
		}
	case "ringing":
		if mine {
			_ = l.rqnt("L/hu(N), L/hf(N)", "G/rt")
		}
	case "answered":
		if mine && st == calling {
			l.mu.Lock()
			l.state = talking
			l.mu.Unlock()
			_ = l.rqnt("L/hu(N), L/hf(N)", "")
		}
	case "ended", "failed":
		if !mine {
			return
		}
		l.mu.Lock()
		l.call = nil
		l.mu.Unlock()
		if st == ringing {
			l.reset() // the caller gave up: stop ringing
			return
		}
		l.drop()
		l.mu.Lock()
		l.state = finished
		l.mu.Unlock()
		sig := "L/ro"
		if ev.Type == "failed" && strings.HasPrefix(ev.Reason, "486") {
			sig = "L/bz"
		}
		_ = l.rqnt("L/hu(N)", sig)
	}
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`"`, "", ",", " ", "\r", "", "\n", "").Replace(s) + `"`
}
