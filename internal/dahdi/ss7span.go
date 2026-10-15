package dahdi

import (
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/ss7"
)

// SS7 spans: the span's voice circuits are lines of the card trunk, each named by its circuit
// identification code, signalled by ISUP on a signalling link (a timeslot of this span set up as an
// HDLC channel) to the adjacent signalling point (the carrier's STP or switch).

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

func (s *span) runSS7(i int, link isdn.Link) error {
	b := s.b
	c := s.conf.SS7
	cics := make([]int, len(s.conf.B))
	for j := range s.conf.B {
		cics[j] = c.FirstCIC + j
		s.byCIC[cics[j]] = s.conf.B[j]
	}
	l := ss7.New(link, ss7.Config{MTPConfig: ss7.MTPConfig{ANSI: c.ANSI, OPC: c.OPC, APC: c.APC, NI: c.NI, SLC: c.SLC, Emergency: true}, CICs: cics})
	l.Incoming = func(call *ss7.Call) {
		b.mu.Lock()
		p := b.ports[s.byCIC[call.CIC]]
		b.mu.Unlock()
		b.incomingCall(fromSS7(call), p)
	}
	l.Up = func(up bool) { b.log.Info("dahdi: SS7 link set", "span", i+1, "up", up) }
	b.mu.Lock()
	s.ss7, s.err = l, ""
	b.mu.Unlock()
	go func() { <-b.ctx.Done(); l.Close(); closeLink(link) }()
	return l.Run()
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
	p := b.ports[s.byCIC[c.CIC]]
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
