package dahdi

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Lines (FXO ports): the card trunk. The engine reaches it at the line port on this computer; a
// line that rings calls the engine from there, to the line's number.

// RingGap is how long without a ring before a ringing line counts as given up by its caller.
var RingGap = 8 * time.Second

type lineCall struct {
	p                   *port
	callID              string
	inbound             bool // the line rang: we called the engine
	invite              *sip.ServerTx
	client              *sip.ClientTx
	localURI, remoteURI string
	target              string
	cseq                int
	answered            bool
	ended               bool
	mu                  sync.Mutex
}

func (b *Bridge) engine() sip.Peer {
	return sip.Peer{Transport: "UDP", Addr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(b.cfg.SIPPort))}
}

func (b *Bridge) contact() string {
	return "<sip:cards@127.0.0.1:" + strconv.Itoa(b.cfg.LinePort) + ">"
}

func (p *port) description(conn *net.UDPConn) *sdp.Session {
	name := "PCMU"
	if p.law == 8 {
		name = "PCMA"
	}
	return sdp.New("127.0.0.1", conn.LocalAddr().(*net.UDPAddr).Port, uint64(time.Now().Unix()), []sdp.Codec{{PT: p.law, Name: name, ClockRate: 8000}}, 101)
}

func remoteOf(body []byte) netip.AddrPort {
	s, err := sdp.Parse(body)
	if err != nil || s.Audio() == nil {
		return netip.AddrPort{}
	}
	ip, err := netip.ParseAddr(s.Connection(s.Audio()))
	if err != nil {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(s.Audio().Port))
}

func (lc *lineCall) request(method string) *sip.Message {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	target := lc.target
	if target == "" {
		target = "sip:" + lc.p.b.engine().Addr.String()
	}
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: target}
	m.Set("From", lc.localURI)
	m.Set("To", lc.remoteURI)
	m.Set("Call-ID", lc.callID)
	if method != sip.ACK {
		lc.cseq++
	}
	m.Set("CSeq", strconv.Itoa(lc.cseq)+" "+method)
	m.Set("Contact", lc.p.b.contact())
	return m
}

// end finishes the SIP side; hang: the line is put back on hook too.
func (lc *lineCall) end(hang bool) {
	lc.mu.Lock()
	if lc.ended {
		lc.mu.Unlock()
		return
	}
	lc.ended = true
	answered, inbound, invite, client := lc.answered, lc.inbound, lc.invite, lc.client
	lc.mu.Unlock()
	b := lc.p.b
	switch {
	case answered:
		go func() {
			for range b.lines.Request(lc.request(sip.BYE), b.engine()).Responses {
			}
		}()
	case inbound && client != nil:
		client.Cancel()
	case !inbound && invite != nil:
		r := sip.NewResponse(invite.Req, 480, "")
		r.Set("To", lc.localURI)
		invite.Respond(r)
	}
	if hang {
		_ = lc.p.ch.Hook(HookOn)
	}
}

func (p *port) lineEvent(ev int) {
	p.mu.Lock()
	st, lc := p.state, p.line
	p.mu.Unlock()
	switch ev {
	case EventRingBegin, EventRingerOn:
		p.mu.Lock()
		p.lastRing = time.Now()
		p.mu.Unlock()
		if st == stIdle {
			p.set(stRinging)
			go p.lineRang()
		}
	case EventOnHook, EventPolarity:
		// The far end hung up (loop current dropped, or the polarity turned back).
		if ev == EventPolarity && st != stTalking {
			return
		}
		if lc != nil && (st == stTalking || st == stCalling) {
			p.mu.Lock()
			p.line = nil
			p.mu.Unlock()
			lc.end(true)
			p.closeRTP()
			p.set(stIdle)
		}
	case EventAlarm:
		p.mu.Lock()
		p.err = "the card reports an alarm on the line"
		p.mu.Unlock()
	}
}

// lineRang calls the engine for a ringing line, and answers the line when the engine answers.
func (p *port) lineRang() {
	b := p.b
	conn, err := p.openRTP(netip.AddrPort{})
	if err != nil {
		p.set(stIdle)
		return
	}
	to := p.conf.Number
	if to == "" {
		to = "s"
	}
	lc := &lineCall{p: p, callID: sip.NewCallID(), inbound: true, cseq: 1}
	lc.localURI = "<sip:anonymous@127.0.0.1>;tag=" + sip.NewTag()
	lc.remoteURI = "<sip:" + to + "@127.0.0.1>"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + to + "@" + b.engine().Addr.String()}
	inv.Set("From", lc.localURI)
	inv.Set("To", lc.remoteURI)
	inv.Set("Call-ID", lc.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", b.contact())
	inv.Set("Content-Type", "application/sdp")
	inv.Body = p.description(conn).Bytes()
	p.mu.Lock()
	p.line = lc
	p.mu.Unlock()
	tx := b.lines.Request(inv, b.engine())
	lc.mu.Lock()
	lc.client = tx
	lc.mu.Unlock()
	// A caller who gives up stops the ringing: cancel the engine's call.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				p.mu.Lock()
				gone := p.state == stRinging && time.Since(p.lastRing) > RingGap
				p.mu.Unlock()
				if gone {
					tx.Cancel()
					return
				}
			}
		}
	}()
	var fin *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			fin = r
		}
	}
	close(stop)
	if fin == nil || fin.StatusCode >= 300 {
		p.mu.Lock()
		if p.line == lc {
			p.line = nil
		}
		p.mu.Unlock()
		p.closeRTP()
		p.set(stIdle)
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
	_ = p.ch.Hook(HookOff)
	p.set(stTalking)
}

// handleLine takes the engine's requests on the card trunk.
func (b *Bridge) handleLine(tx *sip.ServerTx, in *sip.Incoming) {
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
	p, lc := b.lineOf(m.CallID())
	switch m.Method {
	case sip.INVITE:
		if inDialog {
			if lc == nil {
				tx.Respond(sip.NewResponse(m, 481, ""))
				return
			}
			if r := remoteOf(m.Body); r.IsValid() {
				p.mu.Lock()
				p.remote = r
				p.mu.Unlock()
			}
			p.mu.Lock()
			conn := p.rtp
			p.mu.Unlock()
			r := sip.NewResponse(m, 200, "")
			r.Set("Contact", b.contact())
			if conn != nil {
				r.Set("Content-Type", "application/sdp")
				r.Body = p.description(conn).Bytes()
			}
			tx.Respond(r)
			return
		}
		go b.lineOut(tx, m)
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if lc != nil && !lc.answered {
			lc.mu.Lock()
			inv := lc.invite
			lc.ended = true
			lc.mu.Unlock()
			if inv != nil {
				r := sip.NewResponse(inv.Req, 487, "")
				r.Set("To", lc.localURI)
				inv.Respond(r)
			}
			p.lineDone()
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if lc != nil {
			lc.mu.Lock()
			lc.ended = true
			lc.mu.Unlock()
			p.lineDone()
		}
	case sip.ACK:
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

func (p *port) lineDone() {
	p.mu.Lock()
	p.line = nil
	p.out = nil
	p.mu.Unlock()
	_ = p.ch.Hook(HookOn)
	p.closeRTP()
	p.set(stIdle)
}

func (b *Bridge) lineOf(callID string) (*port, *lineCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.ports {
		p.mu.Lock()
		lc := p.line
		p.mu.Unlock()
		if lc != nil && lc.callID == callID {
			return p, lc
		}
	}
	return nil, nil
}

// freeLine claims an idle line, the lowest channel first.
func (b *Bridge) freeLine() *port {
	b.mu.Lock()
	var cands []*port
	for _, p := range b.ports {
		if p.conf.Kind == "line" {
			cands = append(cands, p)
		}
	}
	b.mu.Unlock()
	slices.SortFunc(cands, func(x, y *port) int { return x.conf.Channel - y.conf.Channel })
	for _, p := range cands {
		p.mu.Lock()
		if p.state == stIdle && p.ch != nil {
			p.state = stCalling
			p.mu.Unlock()
			return p
		}
		p.mu.Unlock()
	}
	return nil
}

// lineOut places the engine's call on a free line: off hook, dial the number, then answer.
func (b *Bridge) lineOut(tx *sip.ServerTx, m *sip.Message) {
	number := ""
	if u, err := sip.ParseURI(m.RequestURI); err == nil {
		number = u.User
	}
	number = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '*' || r == '#' || r == ',' {
			return r
		}
		return -1
	}, number)
	if number == "" {
		tx.Respond(sip.NewResponse(m, 484, ""))
		return
	}
	p := b.freeLine()
	if p == nil {
		tx.Respond(sip.NewResponse(m, 486, "All Lines Busy"))
		return
	}
	conn, err := p.openRTP(remoteOf(m.Body))
	if err != nil {
		p.set(stIdle)
		tx.Respond(sip.NewResponse(m, 503, ""))
		return
	}
	lc := &lineCall{p: p, callID: m.CallID(), invite: tx, cseq: 1000}
	lc.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	lc.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		lc.target = ct.URI.String()
	}
	p.mu.Lock()
	p.line = lc
	p.mu.Unlock()
	tx.NoAck = func() { p.lineDone() }
	if err := p.ch.Hook(HookOff); err != nil {
		p.lineDone()
		tx.Respond(sip.NewResponse(m, 503, "Line Not Available"))
		return
	}
	r := sip.NewResponse(m, 183, "")
	r.Set("To", lc.localURI)
	r.Set("Contact", b.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = p.description(conn).Bytes()
	tx.Respond(r)
	p.mu.Lock()
	p.out = append(p.out, dtmfFrames(number, p.law, true)...)
	p.mu.Unlock()
	// Analogue lines say nothing when the far end answers: answer once the number is dialled.
	for {
		time.Sleep(20 * time.Millisecond)
		p.mu.Lock()
		left, gone := len(p.out), p.line != lc
		p.mu.Unlock()
		if gone {
			return
		}
		if left == 0 {
			break
		}
	}
	lc.mu.Lock()
	if lc.ended {
		lc.mu.Unlock()
		return
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
}
