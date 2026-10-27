package dahdi

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/biodream-llc/bitvoice/internal/sigtran/m3ua"
	"github.com/biodream-llc/bitvoice/internal/ss7"
	"github.com/biodream-llc/bitvoice/internal/ss7/sccp"
)

// STPConf makes bitvoice-dahdi a signal transfer point for its SS7 link sets (ITU): messages for
// another point code are passed on, to the link set whose adjacent point it is, else over M3UA to an
// IP STP or signalling gateway; SCCP messages addressed to this point code by global title are
// translated (Translations) and passed on, and those it cannot deliver are returned (UDTS) when the
// sender asked for that.
type STPConf struct {
	OPC          int                `json:"opc"` // this STP's point code: the link sets' own
	NI           int                `json:"ni"`
	Relay        bool               `json:"relay"`          // pass on messages for other point codes
	M3UA         string             `json:"m3ua,omitempty"` // host:port of an IP STP (M3UA over TCP)
	RC           uint32             `json:"rc,omitempty"`   // a configured routing context (0: registered)
	Translations []sccp.Translation `json:"translations,omitempty"`
}

// stp is the bridge's STP at work.
type stp struct {
	b    *Bridge
	conf STPConf
	asp  *m3ua.ASP

	mu    sync.Mutex
	ipErr string

	relayed, translated, returned atomic.Int64
}

// startSTP starts the STP when the bridge's config has one (called once the spans are known).
func (b *Bridge) startSTP() {
	c := b.cfg.STP
	if c == nil {
		return
	}
	s := &stp{b: b, conf: *c}
	b.stp = s
	if c.M3UA == "" {
		return
	}
	keys := []int{c.OPC}
	if c.Relay {
		// The STP takes traffic for the points behind it, too: each link set's adjacent point.
		for _, sp := range b.cfg.Spans {
			if sp.SS7 != nil && sp.SS7.OPC == c.OPC && !sp.SS7.ANSI && !containsInt(keys, sp.SS7.APC) {
				keys = append(keys, sp.SS7.APC)
			}
		}
	}
	s.asp = m3ua.New(m3ua.Config{Addr: c.M3UA, OPC: c.OPC, NI: c.NI, RC: c.RC, Keys: keys})
	s.asp.Receive = func(l m3ua.Label, data []byte) {
		s.route(l.SI, ss7.Label{DPC: l.DPC, OPC: l.OPC, SLS: l.SLS}, data, "ip")
	}
	go s.asp.Run(b.ctx, func(up bool, err error) {
		s.mu.Lock()
		s.ipErr = ""
		if err != nil {
			s.ipErr = err.Error()
		}
		s.mu.Unlock()
		b.log.Info("dahdi: M3UA to the IP STP", "address", c.M3UA, "active", up, "error", err)
	})
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// hooks are what a link set of this STP's point code needs: its relay for other point codes, and
// SCCP for this one.
func (s *stp) hooks(c *SS7Conf) (relay func(int, ss7.Label, []byte), user func(int, ss7.Label, []byte)) {
	if s == nil || c.ANSI || c.OPC != s.conf.OPC {
		return nil, nil
	}
	if s.conf.Relay {
		relay = func(si int, l ss7.Label, data []byte) { s.route(si, l, data, "link set") }
	}
	user = func(si int, l ss7.Label, data []byte) { s.local(si, l, data) }
	return relay, user
}

// route passes a message on towards its destination point code.
func (s *stp) route(si int, l ss7.Label, data []byte, from string) {
	if l.DPC == s.conf.OPC {
		s.local(si, l, data)
		return
	}
	if !s.conf.Relay {
		return
	}
	s.b.mu.Lock()
	var to *ss7.SS7
	for _, ls := range s.b.linkSets {
		if ls.APC() == l.DPC {
			to = ls
		}
	}
	s.b.mu.Unlock()
	switch {
	case to != nil && to.SendMTP(si, l, data) == nil:
		s.relayed.Add(1)
	case to == nil && from != "ip" && s.asp != nil && s.asp.Send(m3ua.Label{OPC: l.OPC, DPC: l.DPC, SI: si, NI: s.conf.NI, SLS: l.SLS}, data) == nil:
		s.relayed.Add(1)
	default:
		s.undeliverable(si, l, data, sccp.CauseMTPFailure)
	}
}

// local takes SCCP for this point code: a global title to translate, or nothing this STP serves.
func (s *stp) local(si int, l ss7.Label, data []byte) {
	if si != sccp.SI {
		return
	}
	m, err := sccp.Decode(data)
	if err != nil || (m.Type != sccp.TypeUDT && m.Type != sccp.TypeXUDT) {
		return // service messages for this point code end here
	}
	called, ok := sccp.Translate(m.Called, s.conf.Translations)
	if ok && called.PC == s.conf.OPC {
		ok = false // translated to this point code, which has no subsystem: never round again
	}
	if !ok {
		cause := sccp.CauseUnequippedUser // routed on a subsystem of ours: we have none
		if !m.Called.RouteOnSSN {
			cause = sccp.CauseNoTranslationForAddress
		}
		s.undeliverable(si, l, data, cause)
		return
	}
	if m.Type == sccp.TypeXUDT {
		if m.HopCounter <= 1 {
			s.undeliverable(si, l, data, sccp.CauseHopCounterViolation)
			return
		}
		m.HopCounter--
	}
	m.Called = called
	if m.Calling.PC < 0 && m.Calling.RouteOnSSN {
		m.Calling.PC = l.OPC // the answer must find its way back
	}
	b, err := m.Encode()
	if err != nil {
		return
	}
	s.translated.Add(1)
	// An SCCP relay sends the message on as itself (Q.714 2.4.1).
	s.route(si, ss7.Label{OPC: s.conf.OPC, DPC: called.PC, SLS: l.SLS}, b, "translation")
}

// undeliverable returns an SCCP message that asked for it to its sender (UDTS, XUDTS).
func (s *stp) undeliverable(si int, l ss7.Label, data []byte, cause int) {
	if si != sccp.SI {
		return
	}
	m, err := sccp.Decode(data)
	if err != nil {
		return
	}
	r, ok := sccp.Return(m, cause)
	if !ok {
		return
	}
	dpc := l.OPC
	if r.Called.PC >= 0 {
		dpc = r.Called.PC
	}
	if dpc == s.conf.OPC {
		return
	}
	b, err := r.Encode()
	if err != nil {
		return
	}
	s.returned.Add(1)
	s.route(si, ss7.Label{OPC: s.conf.OPC, DPC: dpc, SLS: l.SLS}, b, "return")
}

// status says how the STP is doing, for the engine's card screen.
func (s *stp) status() string {
	if s == nil {
		return ""
	}
	out := "STP " + strconv.Itoa(s.conf.OPC)
	if !s.conf.Relay {
		out = "SCCP at point code " + strconv.Itoa(s.conf.OPC)
	}
	if s.asp != nil {
		s.mu.Lock()
		e := s.ipErr
		s.mu.Unlock()
		switch {
		case s.asp.Active():
			out += fmt.Sprintf("; M3UA to %s active (routing context %d)", s.conf.M3UA, s.asp.RC())
		case e != "":
			out += fmt.Sprintf("; M3UA to %s down: %s", s.conf.M3UA, e)
		default:
			out += fmt.Sprintf("; M3UA to %s connecting", s.conf.M3UA)
		}
	}
	return out + fmt.Sprintf("; %d passed on, %d translated, %d returned", s.relayed.Load(), s.translated.Load(), s.returned.Load())
}
