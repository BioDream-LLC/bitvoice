package dahdi

import (
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/ss7"
)

// SS7 spans: the span's voice channels are lines of the card trunk, each named by its circuit
// identification code, signalled by ISUP on a signalling link (a timeslot of this span set up as an
// HDLC channel) to the adjacent signalling point (the carrier's STP or switch). Spans with the same
// point codes, network indicator and variant form one link set: each span's signalling link is one
// of its links (with its own signalling link code), traffic is shared between them, and when one
// fails the others carry its traffic; every span's circuits belong to the link set.

// SS7Conf is a span's link set as bitvoice-dahdi needs it.
type SS7Conf struct {
	ANSI bool `json:"ansi,omitempty"`
	OPC  int  `json:"opc"` // our point code
	APC  int  `json:"apc"` // the adjacent point code
	NI   int  `json:"ni"`  // network indicator: 2 national, 0 international
	SLC  int  `json:"slc"`
	// FirstCIC is the CIC of the span's first voice channel; the others follow in order.
	FirstCIC int `json:"firstCic"`
}

func (c SS7Conf) key() [4]int {
	a := 0
	if c.ANSI {
		a = 1
	}
	return [4]int{a, c.OPC, c.APC, c.NI}
}

// linkSet is the link set a span's SS7 belongs to, made by its first span; every span of it adds its
// circuits at once (calls on them may come over any link).
func (b *Bridge) linkSet(s *span) *ss7.SS7 {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := s.conf.SS7.key()
	if b.linkSets == nil {
		b.linkSets = map[[4]int]*ss7.SS7{}
	}
	if l := b.linkSets[k]; l != nil {
		return l
	}
	var cics []int
	var members []*span
	for _, o := range b.spans {
		if o.conf.SS7 != nil && o.conf.SS7.key() == k {
			members = append(members, o)
		}
	}
	for _, o := range members {
		for j := range o.conf.B {
			cic := o.conf.SS7.FirstCIC + j
			cics = append(cics, cic)
			o.byCIC[cic] = o.conf.B[j]
		}
	}
	c := s.conf.SS7
	relay, user := b.stp.hooks(c)
	l := ss7.NewLinkSet(ss7.Config{MTPConfig: ss7.MTPConfig{ANSI: c.ANSI, OPC: c.OPC, APC: c.APC, NI: c.NI, SLC: c.SLC,
		Emergency: len(members) == 1, Relay: relay}, CICs: cics})
	l.User = user
	l.Incoming = func(call *ss7.Call) {
		b.mu.Lock()
		p := b.ports[b.channelOfCIC(k, call.CIC)]
		b.mu.Unlock()
		b.incomingCall(fromSS7(call), p)
	}
	l.Up = func(up bool) { b.log.Info("dahdi: SS7 link set", "opc", c.OPC, "apc", c.APC, "up", up) }
	b.linkSets[k] = l
	go func() { <-b.ctx.Done(); l.Close() }()
	return l
}

// channelOfCIC is the DAHDI channel of a link set's circuit, on whichever span holds it. Called with
// b.mu held.
func (b *Bridge) channelOfCIC(k [4]int, cic int) int {
	for _, o := range b.spans {
		if o.conf.SS7 != nil && o.conf.SS7.key() == k {
			if ch, ok := o.byCIC[cic]; ok {
				return ch
			}
		}
	}
	return 0
}

func (s *span) runSS7(i int, link isdn.Link) error {
	b := s.b
	l := b.linkSet(s)
	b.mu.Lock()
	s.ss7, s.err = l, ""
	b.mu.Unlock()
	go func() { <-b.ctx.Done(); closeLink(link) }()
	return l.AddLink(link, s.conf.SS7.SLC)
}

// ss7Out places the engine's call on a free circuit of an SS7 span.
func (b *Bridge) ss7Out(tx *sip.ServerTx, m *sip.Message, s *span, l *ss7.SS7, setup isdn.Setup) bool {
	restricted := false
	if p := m.Get("Privacy"); p != "" && (contains(p, "id") || contains(p, "user")) {
		restricted = true
	}
	c, err := l.Dial(ss7.Setup{Called: setup.Called, Calling: setup.Calling, Name: setup.Name, Restricted: restricted, Redirecting: setup.Redirecting})
	if err != nil {
		return false
	}
	b.mu.Lock()
	p := b.ports[b.channelOfCIC(s.conf.SS7.key(), c.CIC)]
	b.mu.Unlock()
	if p == nil {
		c.Hangup(ss7.CauseNoCircuit)
		return false
	}
	b.callOnSpan(tx, m, fromSS7(c), p)
	return true
}

func contains(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}

// fromSS7 adapts an ISUP call to the bridge's trunk call, its events translated.
func fromSS7(c *ss7.Call) *tcall {
	ev := make(chan isdn.Event, 16)
	go func() {
		for e := range c.Events {
			ev <- isdn.Event{Kind: e.Kind, Cause: e.Cause}
			if e.Kind == "released" {
				// ISUP has no separate disconnect: the bridge hears both from REL
				return
			}
		}
	}()
	// The bridge waits for "disconnected" then "released" from ISDN; ISUP's REL is both at once.
	out := make(chan isdn.Event, 16)
	go func() {
		defer close(out)
		for e := range ev {
			if e.Kind == "released" {
				out <- isdn.Event{Kind: "disconnected", Cause: e.Cause}
				out <- e
				return
			}
			out <- e
		}
	}()
	return &tcall{Called: c.Called, Calling: c.Calling, Name: c.Name, Redirecting: c.Redirecting, Events: out,
		alerting: c.Alerting, answer: c.Answer, hangup: c.Hangup}
}
