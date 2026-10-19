package dahdi

import (
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/ss7"
)

// Primary rate ISDN: a T1 or E1 span's B-channels are lines of the card trunk, signalled on its
// D-channel (Q.921 and Q.931, internal/isdn) instead of by hook and tones: the called number and the
// caller ID go with every call, and the phone company says when the far end answers.

// SpanConf is one PRI span as the bridge needs it.
type SpanConf struct {
	D       int    `json:"d"`       // the D-channel's DAHDI number
	B       []int  `json:"b"`       // the B-channels' DAHDI numbers, in Q.931 order
	Q931    []int  `json:"q931"`    // the same channels as Q.931 numbers them
	Network bool   `json:"network"` // BitVoice is the network side
	Switch  string `json:"switch"`
	// NetworkTransfer: transfers joining two calls on this span go to the phone company.
	NetworkTransfer bool `json:"networkTransfer,omitempty"`
	// Charges asks the phone company for advice of charge on calls out (EuroISDN).
	Charges bool `json:"charges,omitempty"`
	// BRI is a basic rate span; Multipoint, several terminals on it.
	BRI        bool `json:"bri,omitempty"`
	Multipoint bool `json:"multipoint,omitempty"`
	// SS7: the span's voice channels are ISUP circuits, D is their signalling link.
	SS7 *SS7Conf `json:"ss7,omitempty"`
}

// OpenD opens a span's D-channel (HDLC frames); tests replace it.
var OpenD = openDChannel

type span struct {
	b     *Bridge
	conf  SpanConf
	pri   *isdn.PRI
	ss7   *ss7.SS7
	byQ   map[int]int // Q.931 channel -> DAHDI channel
	byCIC map[int]int // CIC -> DAHDI channel
	byD   map[int]int
	err   string
}

// startSpans opens each span's D-channel and its B-channels' ports.
func (b *Bridge) startSpans() {
	for _, sc := range b.cfg.Spans {
		s := &span{b: b, conf: sc, byQ: map[int]int{}, byD: map[int]int{}, byCIC: map[int]int{}}
		for j, ch := range sc.B {
			if j < len(sc.Q931) {
				s.byQ[sc.Q931[j]], s.byD[ch] = ch, sc.Q931[j]
			}
			p := &port{b: b, conf: ChannelConf{Channel: ch, Kind: "bchan"}, ssrc: uint32(time.Now().UnixNano()) ^ uint32(ch)<<16}
			if b.cfg.ALaw {
				p.law = 8
			}
			b.mu.Lock()
			b.ports[ch] = p
			b.mu.Unlock()
			go p.run()
		}
		b.mu.Lock()
		b.spans = append(b.spans, s)
		b.mu.Unlock()
	}
	// Every span is known before any runs: the spans of one SS7 link set find each other.
	b.mu.Lock()
	spans := append([]*span(nil), b.spans...)
	b.mu.Unlock()
	for i, s := range spans {
		go s.run(i)
	}
}

func (s *span) run(i int) {
	b := s.b
	for b.ctx.Err() == nil {
		link, err := OpenD(s.conf.D)
		if err != nil {
			b.mu.Lock()
			s.err = err.Error()
			b.mu.Unlock()
			b.log.Error("dahdi: D-channel not opened", "span", i+1, "channel", s.conf.D, "err", err)
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		if s.conf.SS7 != nil {
			err = s.runSS7(i, link)
			b.mu.Lock()
			s.err = "the signalling link stopped: " + errString(err)
			b.mu.Unlock()
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		p := isdn.New(link, isdn.Config{Network: s.conf.Network, Channels: s.conf.Q931, ALaw: b.cfg.ALaw, Log: b.log, Switch: s.conf.Switch,
			RequestCharge: s.conf.Charges && !s.conf.Network, BRI: s.conf.BRI, PTMP: s.conf.Multipoint})
		p.Incoming = s.incoming
		p.Up = func(up bool) { b.log.Info("dahdi: ISDN D-channel", "span", i+1, "up", up) }
		b.mu.Lock()
		s.pri, s.err = p, ""
		b.mu.Unlock()
		go func() { <-b.ctx.Done(); p.Close(); closeLink(link) }()
		err = p.Run()
		b.mu.Lock()
		s.err = "the D-channel stopped: " + errString(err)
		b.mu.Unlock()
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

func closeLink(l isdn.Link) {
	if c, ok := l.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

func (s *span) status() string {
	s.b.mu.Lock()
	p, l, err := s.pri, s.ss7, s.err
	s.b.mu.Unlock()
	if s.conf.SS7 != nil {
		if l == nil || !l.IsUp() {
			if err != "" {
				return "SS7 link set down: " + err
			}
			return "SS7 link set down"
		}
		st := "SS7 link set up"
		if links := l.Links(); len(links) > 1 {
			// A link set of several spans: this span's own signalling link, and how many are in service.
			n := 0
			for _, v := range links {
				if v == "in service" {
					n++
				}
			}
			own := links[s.conf.SS7.SLC]
			if own == "" {
				own = "down"
			}
			st += " (" + strconv.Itoa(n) + " of " + strconv.Itoa(len(links)) + " links in service; this span's link " + own + ")"
		}
		return st + ", " + strconv.Itoa(len(l.Busy())) + " circuits in calls"
	}
	if p == nil || !p.IsUp() {
		if err != "" {
			return "D-channel down: " + err
		}
		return "D-channel down"
	}
	return "D-channel up, " + strconv.Itoa(len(p.Busy())) + " of " + strconv.Itoa(len(s.conf.B)) + " B-channels in calls"
}

// incoming: the phone company (or the PBX on our network side) calls: the engine is called, to the
// called number, from the caller's.
func (s *span) incoming(ic *isdn.Call) {
	s.b.mu.Lock()
	p := s.b.ports[s.byQ[ic.Channel]]
	s.b.mu.Unlock()
	s.b.incomingCall(fromISDN(ic), p)
}

// incomingCall: a call on a digital trunk (ISDN or SS7) reaches the engine.
func (b *Bridge) incomingCall(c *tcall, p *port) {
	if p == nil || !p.is(stIdle) {
		c.Hangup(isdn.CauseChannelUnavailable)
		return
	}
	p.set(stRinging)
	conn, err := p.openRTP(netip.AddrPort{})
	if err != nil {
		p.set(stIdle)
		c.Hangup(isdn.CauseTemporaryFailure)
		return
	}
	to := c.Called
	if to == "" {
		to = "s"
	}
	from := c.Calling
	if from == "" {
		from = "anonymous"
	}
	lc := &lineCall{p: p, callID: sip.NewCallID(), inbound: true, cseq: 1, pri: c.pri, hang: c.Hangup}
	lc.localURI = sipDisplay(c.Name) + "<sip:" + from + "@127.0.0.1>;tag=" + sip.NewTag()
	lc.remoteURI = "<sip:" + to + "@127.0.0.1>"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + to + "@" + b.engine().Addr.String()}
	inv.Set("From", lc.localURI)
	inv.Set("To", lc.remoteURI)
	inv.Set("Call-ID", lc.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", b.contact())
	if c.Redirecting != "" {
		inv.Set("Diversion", "<sip:"+c.Redirecting+"@127.0.0.1>;reason="+divReason[c.RedirectReason])
	}
	inv.Set("Content-Type", "application/sdp")
	inv.Body = p.description(conn).Bytes()
	p.mu.Lock()
	p.line = lc
	p.mu.Unlock()
	tx := b.lines.Request(inv, b.engine())
	lc.mu.Lock()
	lc.client = tx
	lc.mu.Unlock()
	// The caller hanging up before the answer: cancel the engine's call.
	go b.watch(c, lc, p)
	var fin *sip.Message
	alerted := false
	for r := range tx.Responses {
		if r.StatusCode == 180 || r.StatusCode == 183 {
			if !alerted {
				alerted = true
				_ = c.Alerting()
			}
			if r.StatusCode == 183 && len(r.Body) > 0 {
				p.mu.Lock()
				p.remote = remoteOf(r.Body)
				p.mu.Unlock()
			}
		}
		if r.StatusCode >= 200 {
			fin = r
		}
	}
	if fin == nil || fin.StatusCode >= 300 {
		cause := isdn.CauseNoAnswer
		if fin != nil {
			cause = causeOf(fin.StatusCode)
		}
		lc.mu.Lock()
		lc.ended = true
		lc.mu.Unlock()
		c.Hangup(cause)
		return
	}
	lc.mu.Lock()
	lc.remoteURI = fin.Get("To")
	if ct, err := sip.ParseAddress(fin.Get("Contact")); err == nil && ct.URI != nil {
		lc.target = ct.URI.String()
	}
	lc.answered = true
	lc.mu.Unlock()
	p.mu.Lock()
	p.remote = remoteOf(fin.Body)
	p.mu.Unlock()
	n, _ := fin.CSeq()
	ack := lc.request(sip.ACK)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = b.lines.Stateless(ack, b.engine())
	tx.SetAck(ack)
	_ = c.Answer()
	p.set(stTalking)
}

// watch follows the ISDN side of a call: the far end clearing ends the SIP side and frees the port.
func (b *Bridge) watch(c *tcall, lc *lineCall, p *port) {
	for e := range c.Events {
		switch e.Kind {
		case "charge":
			lc.charged(e.Charge)
		case "disconnected", "released":
			p.mu.Lock()
			mine := p.line == lc
			if mine {
				p.line = nil
			}
			p.mu.Unlock()
			if mine {
				lc.end(false)
				p.closeRTP()
				p.set(stIdle)
			}
			if e.Kind == "released" {
				return
			}
		}
	}
}

// causeOf turns a SIP failure into the Q.850 cause the network hears.
func causeOf(code int) int {
	switch code {
	case 404, 484:
		return isdn.CauseUnallocated
	case 486, 600:
		return isdn.CauseBusy
	case 480, 408:
		return isdn.CauseNoAnswer
	case 403, 603:
		return isdn.CauseRejected
	case 503:
		return isdn.CauseNoCircuit
	case 487:
		return isdn.CauseNormal
	}
	return isdn.CauseNormalUnspecified
}

// sipOf turns a Q.850 cause into the SIP answer the engine hears (RFC 3398).
func sipOf(cause int) (int, string) {
	switch cause {
	case isdn.CauseUnallocated, isdn.CauseInvalidNumber:
		return 404, "Not Found"
	case isdn.CauseBusy:
		return 486, "Busy Here"
	case isdn.CauseNoAnswer, 18:
		return 480, "Temporarily Unavailable"
	case isdn.CauseRejected:
		return 603, "Decline"
	case isdn.CauseNoCircuit, isdn.CauseChannelUnavailable, isdn.CauseTemporaryFailure, 38, 42:
		return 503, "Service Unavailable"
	}
	return 480, "Temporarily Unavailable"
}

// priOut places the engine's call on a PRI: the first span with its D-channel up and a free
// B-channel. It reports false when no span can take it (the analogue lines are tried next).
func (b *Bridge) priOut(tx *sip.ServerTx, m *sip.Message, number string) bool {
	b.mu.Lock()
	spans := slices.Clone(b.spans)
	b.mu.Unlock()
	digits := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' || r == '+' {
				return r
			}
			return -1
		}, s)
	}
	setup := isdn.Setup{Called: number}
	if f, err := sip.ParseAddress(m.Get("From")); err == nil && f.URI != nil {
		setup.Calling, setup.Name = digits(f.URI.User), f.Display
	}
	if d := m.Get("Diversion"); d != "" {
		if a, err := sip.ParseAddress(d); err == nil && a.URI != nil {
			setup.Redirecting = digits(a.URI.User)
			setup.RedirectReason = divCode(d)
		}
	}
	for _, s := range spans {
		b.mu.Lock()
		pri, link := s.pri, s.ss7
		b.mu.Unlock()
		if link != nil && link.IsUp() {
			if b.ss7Out(tx, m, s, link, setup) {
				return true
			}
			continue
		}
		if pri == nil || !pri.IsUp() {
			continue
		}
		c, err := pri.DialWith(setup)
		if err != nil {
			continue
		}
		b.mu.Lock()
		p := b.ports[s.byQ[c.Channel]]
		b.mu.Unlock()
		if p == nil {
			c.Hangup(isdn.CauseChannelUnavailable)
			continue
		}
		b.callOnSpan(tx, m, fromISDN(c), p)
		return true
	}
	return false
}

func (b *Bridge) callOnSpan(tx *sip.ServerTx, m *sip.Message, c *tcall, p *port) {
	p.set(stCalling)
	conn, err := p.openRTP(remoteOf(m.Body))
	if err != nil {
		c.Hangup(isdn.CauseTemporaryFailure)
		p.set(stIdle)
		tx.Respond(sip.NewResponse(m, 503, ""))
		return
	}
	lc := &lineCall{p: p, callID: m.CallID(), invite: tx, cseq: 1000, pri: c.pri, hang: c.Hangup}
	lc.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	lc.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		lc.target = ct.URI.String()
	}
	p.mu.Lock()
	p.line = lc
	p.mu.Unlock()
	tx.NoAck = func() { c.Hangup(isdn.CauseNormal); p.lineDone() }
	early := func(code int) {
		r := sip.NewResponse(m, code, "")
		r.Set("To", lc.localURI)
		r.Set("Contact", b.contact())
		r.Set("Content-Type", "application/sdp")
		r.Body = p.description(conn).Bytes() // ringback and announcements come in band from the network
		tx.Respond(r)
	}
	for e := range c.Events {
		switch e.Kind {
		case "proceeding", "progress":
			early(183)
		case "alerting":
			early(180)
		case "charge":
			lc.charged(e.Charge)
		case "connected":
			lc.mu.Lock()
			if lc.ended {
				lc.mu.Unlock()
				c.Hangup(isdn.CauseNormal)
				continue
			}
			lc.answered = true
			lc.mu.Unlock()
			ok := sip.NewResponse(m, 200, "")
			ok.Set("To", lc.localURI)
			ok.Set("Contact", b.contact())
			ok.Set("Content-Type", "application/sdp")
			ok.Body = p.description(conn).Bytes()
			tx.Respond(ok)
			p.set(stTalking)
		case "disconnected", "released":
			lc.mu.Lock()
			answered, ended := lc.answered, lc.ended
			lc.ended = true
			lc.mu.Unlock()
			p.mu.Lock()
			mine := p.line == lc
			if mine {
				p.line = nil
			}
			p.mu.Unlock()
			if !ended {
				if answered {
					go func() {
						for range b.lines.Request(lc.bye(), b.engine()).Responses {
						}
					}()
				} else {
					code, reason := sipOf(e.Cause)
					r := sip.NewResponse(m, code, reason)
					r.Set("To", lc.localURI)
					tx.Respond(r)
				}
			}
			if mine {
				p.closeRTP()
				p.set(stIdle)
			}
			if e.Kind == "released" {
				return
			}
		}
	}
}

// divReason names a redirect reason as SIP's Diversion header does (RFC 5806).
var divReason = map[int]string{isdn.RedirectUnknown: "unknown", isdn.RedirectBusy: "user-busy", isdn.RedirectNoReply: "no-answer",
	isdn.RedirectDeflection: "deflection", isdn.RedirectUnconditional: "unconditional"}

// divCode reads a Diversion header's reason.
func divCode(h string) int {
	l := strings.ToLower(h)
	for code, name := range divReason {
		if code != isdn.RedirectUnknown && strings.Contains(l, "reason="+name) {
			return code
		}
	}
	return isdn.RedirectUnknown
}

// sipDisplay quotes a caller's name for a From header (nothing when there is none).
func sipDisplay(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return `"` + name + `" `
}

// referPRI joins two calls on one span through the phone company (a SIP REFER from BitVoice on one
// call, naming the other with Replaces): Two B-Channel Transfer on National ISDN-2 and 5ESS, explicit
// call transfer on EuroISDN. On success both B-channels are freed when the network clears the calls.
func (b *Bridge) referPRI(tx *sip.ServerTx, m *sip.Message, lc *lineCall) {
	refTo := m.Get("Refer-To")
	i := strings.Index(strings.ToLower(refTo), "replaces=")
	var sp *span
	b.mu.Lock()
	for _, s := range b.spans {
		if s.pri != nil && lc.pri != nil && s.pri == lc.pri.PRI() {
			sp = s
		}
	}
	b.mu.Unlock()
	if lc.pri == nil || i < 0 || sp == nil || !sp.conf.NetworkTransfer {
		tx.Respond(sip.NewResponse(m, 603, "Only Calls On A PRI Are Handed To The Network"))
		return
	}
	v := refTo[i+len("replaces="):]
	if j := strings.IndexAny(v, ";&>"); j >= 0 {
		v = v[:j]
	}
	other, _ := url.QueryUnescape(v)
	if j := strings.IndexByte(other, ';'); j >= 0 {
		other = other[:j] // the Call-ID, before its tags
	}
	_, olc := b.lineOf(other)
	if olc == nil || olc.pri == nil || olc == lc {
		tx.Respond(sip.NewResponse(m, 481, ""))
		return
	}
	if err := lc.pri.Transfer(olc.pri); err != nil {
		b.log.Warn("dahdi: the network did not take the transfer", "err", err)
		tx.Respond(sip.NewResponse(m, 603, "Network Transfer Refused"))
		return
	}
	r := sip.NewResponse(m, 202, "")
	r.Set("Refer-Sub", "false")
	tx.Respond(r)
}

// tcall is a call on a digital trunk, ISDN or SS7, as the bridge needs it.
type tcall struct {
	Called, Calling, Name string
	Redirecting           string
	RedirectReason        int
	Events                <-chan isdn.Event
	alerting, answer      func() error
	hangup                func(int)
	pri                   *isdn.Call
}

func (c *tcall) Alerting() error  { return c.alerting() }
func (c *tcall) Answer() error    { return c.answer() }
func (c *tcall) Hangup(cause int) { c.hangup(cause) }

func fromISDN(c *isdn.Call) *tcall {
	return &tcall{Called: c.Called, Calling: c.Calling, Name: c.Name, Redirecting: c.Redirecting, RedirectReason: c.RedirectReason,
		Events: c.Events, alerting: c.Alerting, answer: c.Answer, hangup: c.Hangup, pri: c}
}
