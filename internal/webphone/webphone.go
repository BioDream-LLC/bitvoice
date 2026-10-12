// Package webphone connects browsers to the call engine. Each signed-in browser phone is registered to
// the engine over loopback SIP as one more device of its extension, so to the engine a browser is just a
// phone: ring groups, queues, transfer and voicemail all work unchanged. The gateway bridges the browser's
// WebRTC media endpoint to a plain RTP endpoint on the loopback side.
package webphone

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Event is something the browser is told about one of its calls.
type Event struct {
	Type     string `json:"type"` // incoming, ringing, answered, ended, failed
	CallID   string `json:"callId"`
	From     string `json:"from,omitempty"`
	FromName string `json:"fromName,omitempty"`
	To       string `json:"to,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// PBXCall is BitVoice's call ID for an incoming call (the one a mobile push carries).
	PBXCall string `json:"pbxCall,omitempty"`
}

// Media supplies endpoints (the engine's pool).
type Media interface {
	NewMediaEndpoint() (*media.Endpoint, error)
}

// Gateway is the SIP side of every browser phone.
type Gateway struct {
	stack  *sip.Stack
	engine sip.Peer
	media  Media
	ctx    context.Context

	mu     sync.Mutex
	phones map[string]*Phone // by contact user
}

// New starts a gateway that registers to the engine at enginePort on loopback.
func New(ctx context.Context, enginePort int, m Media) (*Gateway, error) {
	g := &Gateway{media: m, ctx: ctx, phones: map[string]*Phone{},
		engine: sip.Peer{Transport: "UDP", Addr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(enginePort))}}
	g.stack = sip.NewStack(g.handle)
	g.stack.Host = "127.0.0.1"
	if err := g.stack.Listen("127.0.0.1:0"); err != nil {
		return nil, err
	}
	g.stack.Serve(ctx)
	return g, nil
}

// Phone is one browser signed in as an extension.
type Phone struct {
	g      *Gateway
	ID     string
	Ext    string
	secret string
	Events chan Event
	user   string // contact user, unique per browser
	// Client is the address the browser or app connects from, for 911 location; set by the API.
	Client string
	// Headers go on every call this phone places (a call link's pass).
	Headers map[string]string
	guest   bool // a caller from a call link: not registered, places calls only

	mu    sync.Mutex
	calls map[string]*Call
	stop  chan struct{}
}

// Call is one call of a browser phone.
type Call struct {
	p        *Phone
	ID       string // the SIP Call-ID
	incoming bool
	browser  *media.Endpoint // the WebRTC side, supplied by the caller of Dial/Answer
	sipSide  *media.Endpoint
	invite   *sip.ServerTx // incoming: the engine's INVITE
	client   *sip.ClientTx // outgoing: our INVITE
	inviteRq *sip.Message
	dialog   struct {
		localURI, remoteURI, target string
		seq                         uint32
	}
	answered bool
	ended    bool
	finished bool
	cleanup  func()
}

// Open registers a browser phone for an extension and keeps the registration fresh until Close.
func (g *Gateway) Open(id, ext, secret, client string) (*Phone, error) {
	p := &Phone{g: g, ID: id, Ext: ext, secret: secret, Client: client, Events: make(chan Event, 32), user: ext + "-web-" + id,
		calls: map[string]*Call{}, stop: make(chan struct{})}
	if err := p.register(300); err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.phones[p.user] = p
	g.mu.Unlock()
	go func() {
		t := time.NewTicker(120 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = p.register(300)
			case <-p.stop:
				return
			case <-g.ctx.Done():
				return
			}
		}
	}()
	return p, nil
}

// Guest makes a phone that places calls without registering: for a caller who came by a call link.
// headers carry the pass BitVoice checks instead of a password.
func (g *Gateway) Guest(id, user, client string, headers map[string]string) *Phone {
	p := &Phone{g: g, ID: id, Ext: user, Client: client, Headers: headers, guest: true, Events: make(chan Event, 32),
		user: user + "-guest-" + id, calls: map[string]*Call{}, stop: make(chan struct{})}
	g.mu.Lock()
	g.phones[p.user] = p
	g.mu.Unlock()
	return p
}

// Close unregisters the phone and ends its calls.
func (p *Phone) Close() {
	p.mu.Lock()
	select {
	case <-p.stop:
		p.mu.Unlock()
		return
	default:
		close(p.stop)
	}
	calls := make([]*Call, 0, len(p.calls))
	for _, c := range p.calls {
		calls = append(calls, c)
	}
	p.mu.Unlock()
	for _, c := range calls {
		c.Hangup()
	}
	if !p.guest {
		_ = p.register(0)
	}
	p.g.mu.Lock()
	delete(p.g.phones, p.user)
	p.g.mu.Unlock()
}

func (p *Phone) emit(e Event) {
	select {
	case p.Events <- e:
	default: // a browser not listening loses events, never the call
	}
}

func (p *Phone) contact() string {
	return fmt.Sprintf("<sip:%s@127.0.0.1:%d>", p.user, p.g.stack.Port())
}

func (p *Phone) aor() string { return "<sip:" + p.Ext + "@127.0.0.1>" }

// send runs a request to the engine, answering one digest challenge.
func (p *Phone) send(m *sip.Message) *sip.Message {
	resp := final(p.g.stack.Request(m.Clone(), p.g.engine))
	if resp == nil || (resp.StatusCode != 401 && resp.StatusCode != 407) {
		return resp
	}
	h, hdr := resp.Get("WWW-Authenticate"), "Authorization"
	if resp.StatusCode == 407 {
		h, hdr = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
	}
	ch, err := sip.ParseChallenge(h)
	if err != nil {
		return resp
	}
	cr, err := sip.Answer(ch, p.Ext, p.secret, m.Method, m.RequestURI)
	if err != nil {
		return resp
	}
	next := m.Clone()
	n, method := m.CSeq()
	next.Set("CSeq", strconv.FormatUint(uint64(n+1), 10)+" "+method)
	next.Set(hdr, cr.String())
	return final(p.g.stack.Request(next, p.g.engine))
}

func final(tx *sip.ClientTx) *sip.Message {
	var f *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			f = r
		}
	}
	return f
}

var regCallIDs sync.Map // phone user -> Call-ID, so refreshes keep one binding

func (p *Phone) register(expires int) error {
	cid, _ := regCallIDs.LoadOrStore(p.user, sip.NewCallID())
	m := &sip.Message{IsRequest: true, Method: sip.REGISTER, RequestURI: "sip:127.0.0.1"}
	m.Set("From", p.aor()+";tag="+sip.NewTag())
	m.Set("To", p.aor())
	m.Set("Call-ID", cid.(string))
	m.Set("CSeq", strconv.FormatInt(time.Now().Unix()%1000000*2, 10)+" REGISTER")
	m.Set("Contact", p.contact())
	m.Set("Expires", strconv.Itoa(expires))
	m.Set("User-Agent", "BitVoice web phone")
	if p.Client != "" {
		m.Set("X-BitVoice-Client", p.Client)
	}
	r := p.send(m)
	if r == nil {
		return errors.New("webphone: BitVoice did not answer the registration")
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("webphone: registration refused (%d %s)", r.StatusCode, r.Reason)
	}
	return nil
}

// offerFor describes the loopback side's media: the codec the browser uses, and telephone-event 101.
func offerFor(ep *media.Endpoint, pt int) *sdp.Session {
	c := sdp.Codec{PT: pt, Name: "PCMU", ClockRate: 8000}
	switch pt {
	case 8:
		c.Name = "PCMA"
	case media.PTOpus:
		c = sdp.Codec{PT: pt, Name: "opus", ClockRate: 48000, Channels: 2}
	}
	s := sdp.New("127.0.0.1", ep.Port(), uint64(time.Now().Unix()), []sdp.Codec{c}, 101)
	if pt == media.PTOpus {
		// Key presses go at Opus's clock, as the browser sends them.
		for i, l := range s.Audio().Lines {
			if l.Value == "rtpmap:101 telephone-event/8000" {
				s.Audio().Lines[i].Value = "rtpmap:101 telephone-event/48000"
			}
		}
	}
	return s
}

// bridge connects the browser's endpoint to the loopback endpoint, renumbering telephone-event.
func bridge(browser, sipSide *media.Endpoint, browserEventPT int) {
	if browserEventPT >= 0 && browserEventPT != 101 {
		sipSide.MapPayloadType(browserEventPT, 101)
		browser.MapPayloadType(101, browserEventPT)
	}
	media.Connect(browser, sipSide)
}

// Dial calls a number from the browser. browser is the browser's media endpoint, already answered with
// pt (0 or 8) and its telephone-event payload type (-1 for none).
func (p *Phone) Dial(number string, browser *media.Endpoint, pt, eventPT int) (*Call, error) {
	ep, err := p.g.media.NewMediaEndpoint()
	if err != nil {
		return nil, err
	}
	number = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '*' || r == '#' || r == '+' {
			return r
		}
		return -1
	}, number)
	if number == "" {
		ep.Close()
		return nil, errors.New("enter a number to call")
	}
	c := &Call{p: p, ID: sip.NewCallID(), browser: browser, sipSide: ep}
	c.dialog.localURI = p.aor() + ";tag=" + sip.NewTag()
	c.dialog.remoteURI = "<sip:" + number + "@127.0.0.1>"
	c.dialog.seq = 1
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + number + "@127.0.0.1"}
	inv.Set("From", c.dialog.localURI)
	inv.Set("To", c.dialog.remoteURI)
	inv.Set("Call-ID", c.ID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", p.contact())
	inv.Set("User-Agent", "BitVoice web phone")
	if p.Client != "" {
		inv.Set("X-BitVoice-Client", p.Client) // trusted by BitVoice only from this computer
	}
	for k, v := range p.Headers {
		inv.Set(k, v)
	}
	inv.Set("Content-Type", "application/sdp")
	inv.Body = offerFor(ep, pt).Bytes()
	c.inviteRq = inv
	bridge(browser, ep, eventPT)
	ep.Start()
	p.add(c)
	go c.watchOutgoing(inv, number)
	return c, nil
}

func (p *Phone) add(c *Call) {
	p.mu.Lock()
	p.calls[c.ID] = c
	p.mu.Unlock()
}

// Find returns one of the phone's calls.
func (p *Phone) Find(id string) *Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[id]
}

func (c *Call) watchOutgoing(inv *sip.Message, number string) {
	tx := c.p.g.stack.Request(inv.Clone(), c.p.g.engine)
	authed := false
	for {
		c.p.mu.Lock()
		c.client = tx
		c.p.mu.Unlock()
		var fin *sip.Message
		for r := range tx.Responses {
			if r.StatusCode == 180 || r.StatusCode == 183 {
				c.p.emit(Event{Type: "ringing", CallID: c.ID, To: number})
			}
			if r.StatusCode >= 200 {
				fin = r
			}
		}
		if fin == nil {
			c.finish("failed", "BitVoice did not answer")
			return
		}
		if (fin.StatusCode == 401 || fin.StatusCode == 407) && !authed {
			authed = true
			h, hdr := fin.Get("WWW-Authenticate"), "Authorization"
			if fin.StatusCode == 407 {
				h, hdr = fin.Get("Proxy-Authenticate"), "Proxy-Authorization"
			}
			ch, err := sip.ParseChallenge(h)
			if err != nil {
				c.finish("failed", "authentication")
				return
			}
			cr, _ := sip.Answer(ch, c.p.Ext, c.p.secret, sip.INVITE, inv.RequestURI)
			inv = inv.Clone()
			inv.Set("CSeq", "2 INVITE")
			inv.Set(hdr, cr.String())
			c.dialog.seq = 2
			c.p.mu.Lock()
			c.inviteRq = inv
			c.p.mu.Unlock()
			tx = c.p.g.stack.Request(inv.Clone(), c.p.g.engine)
			continue
		}
		if fin.StatusCode >= 300 {
			c.finish("failed", fmt.Sprintf("%d %s", fin.StatusCode, reasonOf(fin)))
			return
		}
		c.p.mu.Lock()
		ended := c.ended
		c.answered = true
		c.dialog.remoteURI = fin.Get("To")
		if ct, err := sip.ParseAddress(fin.Get("Contact")); err == nil && ct.URI != nil {
			c.dialog.target = ct.URI.String()
		}
		c.p.mu.Unlock()
		if s, err := sdp.Parse(fin.Body); err == nil && s.Audio() != nil {
			c.sipSide.SetRemote(s.Connection(s.Audio()), s.Audio().Port)
		}
		n, _ := fin.CSeq()
		ack := c.request(sip.ACK)
		ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
		_ = c.p.g.stack.Stateless(ack, c.p.g.engine)
		tx.SetAck(ack)
		if ended {
			c.bye()
			return
		}
		c.p.emit(Event{Type: "answered", CallID: c.ID})
		return
	}
}

func reasonOf(m *sip.Message) string {
	if m.Reason != "" {
		return m.Reason
	}
	return sip.ReasonPhrase(m.StatusCode)
}

func (c *Call) request(method string) *sip.Message {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	target := c.dialog.target
	if target == "" {
		target = c.inviteRq.RequestURI
	}
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: target}
	m.Set("From", c.dialog.localURI)
	m.Set("To", c.dialog.remoteURI)
	m.Set("Call-ID", c.ID)
	if method != sip.ACK {
		c.dialog.seq++
	}
	m.Set("CSeq", strconv.FormatUint(uint64(c.dialog.seq), 10)+" "+method)
	m.Set("Contact", c.p.contact())
	return m
}

func (c *Call) bye() {
	final(c.p.g.stack.Request(c.request(sip.BYE), c.p.g.engine))
}

// Hangup ends the call from the browser: BYE once answered, CANCEL or a refusal before.
func (c *Call) Hangup() {
	c.p.mu.Lock()
	if c.ended {
		c.p.mu.Unlock()
		return
	}
	c.ended = true
	answered, incoming, client, invite := c.answered, c.incoming, c.client, c.invite
	c.p.mu.Unlock()
	switch {
	case answered:
		go c.bye()
	case incoming && invite != nil:
		r := sip.NewResponse(invite.Req, 603, "Decline")
		r.Set("To", c.dialog.localURI)
		invite.Respond(r)
	case client != nil:
		client.Cancel()
	}
	c.finish("ended", "you hung up")
}

func (c *Call) finish(typ, reason string) {
	c.p.mu.Lock()
	if c.finished {
		c.p.mu.Unlock()
		return
	}
	c.finished = true
	delete(c.p.calls, c.ID)
	c.ended = true
	side := c.sipSide
	c.p.mu.Unlock()
	if side != nil {
		side.Close()
	}
	if c.cleanup != nil {
		c.cleanup()
	}
	c.p.emit(Event{Type: typ, CallID: c.ID, Reason: reason})
}

// SetCleanup sets what to release when the call ends (the browser endpoint and its WebRTC session).
func (c *Call) SetCleanup(f func()) {
	c.p.mu.Lock()
	c.cleanup = f
	c.p.mu.Unlock()
}

// handle receives the engine's requests to browser phones.
func (g *Gateway) handle(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	if tx == nil {
		return
	}
	user := ""
	if u, err := sip.ParseURI(m.RequestURI); err == nil {
		user = u.User
	}
	g.mu.Lock()
	p := g.phones[user]
	g.mu.Unlock()
	if p == nil {
		// In-dialog requests are addressed to the contact, so a missing phone means it has gone.
		tx.Respond(sip.NewResponse(m, 481, ""))
		return
	}
	switch m.Method {
	case sip.INVITE:
		to, _ := sip.ParseAddress(m.Get("To"))
		if to != nil && to.Tag() != "" {
			// A re-INVITE: media stays on the same endpoints; answer with what we have.
			c := p.Find(m.CallID())
			if c == nil {
				tx.Respond(sip.NewResponse(m, 481, ""))
				return
			}
			r := sip.NewResponse(m, 200, "")
			r.Set("Contact", p.contact())
			r.Set("Content-Type", "application/sdp")
			r.Body = offerFor(c.sipSide, ptOf(m.Body)).Bytes()
			tx.Respond(r)
			return
		}
		p.incoming(tx, m)
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if c := p.Find(m.CallID()); c != nil && !c.answered {
			r := sip.NewResponse(c.invite.Req, 487, "")
			r.Set("To", c.dialog.localURI)
			c.invite.Respond(r)
			c.finish("ended", "the caller hung up")
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if c := p.Find(m.CallID()); c != nil {
			c.p.mu.Lock()
			c.ended = true
			c.p.mu.Unlock()
			c.finish("ended", "the other party hung up")
		}
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

func ptOf(body []byte) int {
	if s, err := sdp.Parse(body); err == nil && s.Audio() != nil {
		for _, c := range s.Audio().Codecs() {
			if strings.EqualFold(c.Name, "opus") {
				return media.PTOpus
			}
			if strings.EqualFold(c.Name, "PCMA") {
				return 8
			}
			if strings.EqualFold(c.Name, "PCMU") {
				return 0
			}
		}
	}
	return 0
}

func (p *Phone) incoming(tx *sip.ServerTx, m *sip.Message) {
	from, _ := sip.ParseAddress(m.Get("From"))
	c := &Call{p: p, ID: m.CallID(), incoming: true, invite: tx}
	c.dialog.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	c.dialog.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		c.dialog.target = ct.URI.String()
	}
	c.dialog.seq = 1000
	c.inviteRq = m
	p.add(c)
	r := sip.NewResponse(m, 180, "")
	r.Set("To", c.dialog.localURI)
	r.Set("Contact", p.contact())
	tx.Respond(r)
	tx.NoAck = func() { c.finish("ended", "BitVoice did not confirm the answer") }
	ev := Event{Type: "incoming", CallID: c.ID, PBXCall: m.Get("X-BitVoice-Call")}
	if from != nil {
		ev.From, ev.FromName = from.URI.User, from.Display
	}
	p.emit(ev)
}

// Offer returns the engine's SDP offer for an incoming call, so the browser side can pick a codec.
func (c *Call) Offer() []byte { return c.inviteRq.Body }

// Answer answers an incoming call with the browser's endpoint, codec and telephone-event type.
func (c *Call) Answer(browser *media.Endpoint, pt, eventPT int) error {
	c.p.mu.Lock()
	if c.ended || c.answered || !c.incoming {
		c.p.mu.Unlock()
		return errors.New("this call can no longer be answered")
	}
	c.answered = true
	c.browser = browser
	c.p.mu.Unlock()
	ep, err := c.p.g.media.NewMediaEndpoint()
	if err != nil {
		return err
	}
	c.sipSide = ep
	if s, err := sdp.Parse(c.inviteRq.Body); err == nil && s.Audio() != nil {
		ep.SetRemote(s.Connection(s.Audio()), s.Audio().Port)
	}
	bridge(browser, ep, eventPT)
	ep.Start()
	r := sip.NewResponse(c.invite.Req, 200, "")
	r.Set("To", c.dialog.localURI)
	r.Set("Contact", c.p.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = offerFor(ep, pt).Bytes()
	c.invite.Respond(r)
	c.p.emit(Event{Type: "answered", CallID: c.ID})
	return nil
}
