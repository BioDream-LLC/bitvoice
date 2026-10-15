package dahdi

import (
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/mf"
	"github.com/biodream-llc/bitvoice/internal/r2"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// MFC-R2 circuits: the channels of an E1 span set up as CAS (cas in /etc/dahdi/system.conf), each a
// line of the card trunk. internal/r2 runs the signalling; this joins it to the channel: the ABCD
// bits through the driver, the compelled tones in the channel's audio.

type r2io struct {
	p   *port
	mu  sync.Mutex
	gen *mf.Generator
	fwd bool // the tones made are forward ones (we placed the call)
}

func (x *r2io) SetBits(abcd int) {
	if c, ok := x.p.ch.(CAS); ok {
		_ = c.TxBits(abcd)
	}
}

func (x *r2io) SetTone(n int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	set := mf.Backward
	if x.fwd {
		set = mf.Forward
	}
	if x.gen == nil || x.gen.Signal() == 0 {
		x.gen = mf.NewGenerator(set, -10)
	}
	x.gen.Set(n)
}

// fill gives the next frame of tone, or nil when none is being sent.
func (x *r2io) fill(law int) []byte {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.gen == nil || x.gen.Signal() == 0 {
		return nil
	}
	pcm := make([]int16, FrameBytes)
	x.gen.Fill(pcm)
	out := make([]byte, FrameBytes)
	for i, v := range pcm {
		out[i] = encode(v, law)
	}
	return out
}

func (p *port) isR2() bool { return p.conf.Kind == "r2" }

// r2Start makes the circuit's signalling once the channel is open.
func (p *port) r2Start() {
	v := r2.Variants[p.conf.Variant]
	if v == nil {
		v = r2.Variants["itu"]
	}
	x := &r2io{p: p}
	var ch *r2.Chan
	ch = r2.New(v, x, r2.Events{
		Offered:      func(ani, dnis string, _ int, restricted bool) { go p.r2Offered(ani, dnis, restricted) },
		Accepted:     func(bool) { p.r2Event("accepted") },
		Answered:     func() { p.r2Event("answered") },
		Disconnected: func(c r2.Cause) { p.r2Disconnected(c) },
		Ended:        func() { p.r2Ended() },
	})
	ch.MaxDNIS = 0
	ch.MaxANI = 15
	p.mu.Lock()
	p.r2, p.r2io = ch, x
	p.fwdDet, p.backDet = mf.NewDetector(mf.Forward), mf.NewDetector(mf.Backward)
	p.mu.Unlock()
	ch.Start()
	if c, ok := p.ch.(CAS); ok {
		if b, err := c.RxBits(); err == nil {
			ch.Bits(b)
		}
	}
}

// r2Audio feeds the tones heard to the signalling, and sends the tone being made.
func (p *port) r2Audio(b []byte) []byte {
	p.mu.Lock()
	ch, x := p.r2, p.r2io
	fwdDet, backDet := p.fwdDet, p.backDet
	p.mu.Unlock()
	if ch == nil {
		return nil
	}
	pcm := make([]int16, len(b))
	for i, v := range b {
		pcm[i] = decode(v, p.law)
	}
	x.mu.Lock()
	forward := x.fwd
	x.mu.Unlock()
	det := fwdDet // the called end hears forward signals
	if forward {
		det = backDet
	}
	for _, n := range det.Feed(pcm) {
		ch.Tone(n)
	}
	return x.fill(p.law)
}

func (p *port) r2Bits() {
	c, ok := p.ch.(CAS)
	if !ok {
		return
	}
	if b, err := c.RxBits(); err == nil {
		p.mu.Lock()
		ch := p.r2
		p.mu.Unlock()
		if ch != nil {
			ch.Bits(b)
		}
	}
}

func (p *port) r2Event(kind string) {
	p.mu.Lock()
	w := p.r2wait
	p.mu.Unlock()
	if w != nil {
		select {
		case w <- kind:
		default:
		}
	}
}

// r2Offered: a call in, with its number and the caller's: the engine is called; the circuit
// accepts when the engine rings and answers when it answers.
func (p *port) r2Offered(ani, dnis string, restricted bool) {
	b := p.b
	p.mu.Lock()
	ch := p.r2
	p.state = stRinging
	p.mu.Unlock()
	conn, err := p.openRTP(netip.AddrPort{})
	if err != nil {
		ch.Reject(r2.Congestion)
		return
	}
	to := dnis
	if to == "" {
		to = p.conf.Number
	}
	if to == "" {
		to = "s"
	}
	from := ani
	if from == "" || restricted {
		from = "anonymous"
	}
	lc := &lineCall{p: p, callID: sip.NewCallID(), inbound: true, cseq: 1}
	lc.localURI = "<sip:" + from + "@127.0.0.1>;tag=" + sip.NewTag()
	lc.remoteURI = "<sip:" + to + "@127.0.0.1>"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + to + "@" + b.engine().Addr.String()}
	inv.Set("From", lc.localURI)
	inv.Set("To", lc.remoteURI)
	inv.Set("Call-ID", lc.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", b.contact())
	if restricted {
		inv.Set("Privacy", "id")
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
	accepted := false
	var fin *sip.Message
	for r := range tx.Responses {
		if (r.StatusCode == 180 || r.StatusCode == 183) && !accepted {
			accepted = true
			ch.Accept(true)
		}
		if r.StatusCode >= 200 {
			fin = r
		}
	}
	if fin == nil || fin.StatusCode >= 300 {
		p.mu.Lock()
		if p.line == lc {
			p.line = nil
		}
		p.mu.Unlock()
		p.closeRTP()
		if accepted {
			ch.Hangup()
			return
		}
		code := 480
		if fin != nil {
			code = fin.StatusCode
		}
		ch.Reject(r2CauseOf(code))
		return
	}
	if !accepted {
		ch.Accept(true)
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
	ch.Answer()
	p.set(stTalking)
}

func r2CauseOf(code int) r2.Cause {
	switch code {
	case 486, 600:
		return r2.Busy
	case 404, 484:
		return r2.Unallocated
	case 503:
		return r2.Congestion
	}
	return r2.Busy
}

// r2Out places the engine's call on the circuit.
func (b *Bridge) r2Out(p *port, tx *sip.ServerTx, m *sip.Message, number, ani string) {
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
	wait := make(chan string, 8)
	p.mu.Lock()
	p.line, p.r2wait = lc, wait
	ch, x := p.r2, p.r2io
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.r2wait == wait {
			p.r2wait = nil
		}
		p.mu.Unlock()
	}()
	tx.NoAck = func() { p.lineDone() }
	trying := sip.NewResponse(m, 100, "")
	tx.Respond(trying) // the compelled signalling takes seconds: say the call is under way
	x.mu.Lock()
	x.fwd = true
	x.mu.Unlock()
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, number)
	if err := ch.Dial(ani, digits, r2.CategoryNational, ani == ""); err != nil {
		p.mu.Lock()
		p.line = nil
		p.mu.Unlock()
		p.closeRTP()
		x.mu.Lock()
		x.fwd = false
		x.mu.Unlock()
		p.set(stIdle)
		tx.Respond(sip.NewResponse(m, 503, "Circuit Not Idle"))
		return
	}
	respond := func(code int, sdp bool) {
		r := sip.NewResponse(m, code, "")
		r.Set("To", lc.localURI)
		r.Set("Contact", b.contact())
		if sdp {
			r.Set("Content-Type", "application/sdp")
			r.Body = p.description(conn).Bytes()
		}
		tx.Respond(r)
	}
	for kind := range wait {
		switch kind {
		case "accepted":
			respond(180, true)
		case "answered":
			lc.mu.Lock()
			if lc.ended {
				lc.mu.Unlock()
				return
			}
			lc.answered = true
			lc.mu.Unlock()
			respond(200, true)
			p.set(stTalking)
			return
		case "failed":
			return
		}
	}
}

func (p *port) r2Disconnected(c r2.Cause) {
	p.mu.Lock()
	lc, ch, w := p.line, p.r2, p.r2wait
	p.line = nil
	p.mu.Unlock()
	if lc != nil {
		lc.mu.Lock()
		answered, invite := lc.answered, lc.invite
		lc.ended = true
		lc.mu.Unlock()
		b := p.b
		switch {
		case answered:
			go func() {
				for range b.lines.Request(lc.bye(), b.engine()).Responses {
				}
			}()
		case invite != nil:
			code, reason := 480, "Temporarily Unavailable"
			switch c {
			case r2.Busy:
				code, reason = 486, "Busy Here"
			case r2.Unallocated, r2.NumberChanged:
				code, reason = 404, "Not Found"
			case r2.Congestion, r2.OutOfOrder, r2.Glare:
				code, reason = 503, "Service Unavailable"
			}
			r := sip.NewResponse(invite.Req, code, reason)
			r.Set("To", lc.localURI)
			invite.Respond(r)
		case lc.client != nil:
			lc.client.Cancel()
		}
	}
	if w != nil {
		select {
		case w <- "failed":
		default:
		}
	}
	p.closeRTP()
	ch.Hangup()
}

func (p *port) r2Ended() {
	p.mu.Lock()
	x := p.r2io
	p.line = nil
	p.mu.Unlock()
	if x != nil {
		x.mu.Lock()
		x.fwd = false
		x.mu.Unlock()
	}
	p.closeRTP()
	p.set(stIdle)
}
