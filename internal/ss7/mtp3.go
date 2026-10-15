package ss7

import (
	"bytes"
	"sync"
	"time"
)

// MTP3 for one link set of one link to an adjacent signalling point (the usual for a PBX on a
// carrier's SS7: an A-link to its STP or switch): routing labels, the link test (Q.707 SLTM/SLTA),
// traffic restart allowed (TRA), and user part messages in and out.

// Service indicators.
const (
	siSNM      = 0 // signalling network management
	siSTM      = 1 // signalling network testing and maintenance (ITU)
	siSpecTest = 2 // special testing (ANSI)
	siISUP     = 5
)

// Network indicators.
const (
	NIInternational = 0
	NINational      = 2
)

// Label is a routing label.
type Label struct {
	DPC, OPC int
	SLS      int
}

// Point codes and labels.
func (l Label) bytes(ansi bool) []byte {
	if ansi {
		return []byte{byte(l.DPC), byte(l.DPC >> 8), byte(l.DPC >> 16), byte(l.OPC), byte(l.OPC >> 8), byte(l.OPC >> 16), byte(l.SLS)}
	}
	v := uint32(l.DPC&0x3FFF) | uint32(l.OPC&0x3FFF)<<14 | uint32(l.SLS&0xF)<<28
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func labelSize(ansi bool) int {
	if ansi {
		return 7
	}
	return 4
}

func parseLabel(b []byte, ansi bool) (Label, bool) {
	if ansi {
		if len(b) < 7 {
			return Label{}, false
		}
		return Label{DPC: int(b[0]) | int(b[1])<<8 | int(b[2])<<16, OPC: int(b[3]) | int(b[4])<<8 | int(b[5])<<16, SLS: int(b[6])}, true
	}
	if len(b) < 4 {
		return Label{}, false
	}
	v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return Label{DPC: int(v & 0x3FFF), OPC: int(v >> 14 & 0x3FFF), SLS: int(v >> 28)}, true
}

// Q.707 timers.
var (
	SLTWait   = 4 * time.Second  // T1: waiting for SLTA
	SLTPeriod = 30 * time.Second // T2: between link tests
)

var testPattern = []byte{0x42, 0x69, 0x74, 0x56, 0x6F, 0x69, 0x63, 0x65} // "BitVoice"

// MTPConfig is the link set.
type MTPConfig struct {
	ANSI      bool
	OPC       int  // our point code
	APC       int  // the adjacent point code
	NI        int  // network indicator: NINational (the usual) or NIInternational
	SLC       int  // the link's signalling link code
	Emergency bool // align with the emergency proving period (a link set of one link: always, Q.704)
}

type mtp3 struct {
	cfg     MTPConfig
	l2      *mtp2
	deliver func(si int, l Label, data []byte) // user part messages (ISUP)
	up      func(bool)

	mu       sync.Mutex
	testOK   bool
	traSent  bool
	traGot   bool
	isUp     bool
	sltTimer *time.Timer
	sltFails int
	closed   bool
}

func newMTP3(link Link, cfg MTPConfig, deliver func(int, Label, []byte), up func(bool)) *mtp3 {
	m := &mtp3{cfg: cfg, deliver: deliver, up: up}
	m.l2 = newMTP2(link, cfg.Emergency, m.receive, m.l2Up)
	return m
}

func (m *mtp3) sio(si int) byte {
	b := byte(si&0xF) | byte(m.cfg.NI&3)<<6
	if m.cfg.ANSI && si != siISUP {
		b |= 3 << 4 // network management at the highest priority (ANSI)
	}
	return b
}

func (m *mtp3) label(dpc, sls int) Label { return Label{DPC: dpc, OPC: m.cfg.OPC, SLS: sls} }

func (m *mtp3) send(si int, l Label, data []byte) error {
	msu := append([]byte{m.sio(si)}, l.bytes(m.cfg.ANSI)...)
	return m.l2.Send(append(msu, data...))
}

func (m *mtp3) l2Up(up bool) {
	m.mu.Lock()
	m.testOK, m.traSent, m.traGot = false, false, false
	if m.sltTimer != nil {
		m.sltTimer.Stop()
		m.sltTimer = nil
	}
	m.mu.Unlock()
	if up {
		m.linkTest()
		return
	}
	m.setUp(false)
}

// linkTest sends an SLTM; an SLTA with the same pattern proves the link.
func (m *mtp3) linkTest() {
	si := siSTM
	if m.cfg.ANSI {
		si = siSpecTest
	}
	h1 := byte(len(testPattern) << 4)
	if m.cfg.ANSI {
		h1 |= byte(m.cfg.SLC & 0xF)
	}
	_ = m.send(si, m.label(m.cfg.APC, m.cfg.SLC), append([]byte{0x11, h1}, testPattern...))
	m.mu.Lock()
	if m.sltTimer != nil {
		m.sltTimer.Stop()
	}
	m.sltTimer = time.AfterFunc(SLTWait, func() {
		m.mu.Lock()
		ok := m.testOK
		m.sltFails++
		fails := m.sltFails
		m.mu.Unlock()
		if !ok && fails < 2 {
			m.linkTest() // Q.707: try once more
		} else if !ok {
			m.l2.mu.Lock()
			m.l2.failed()
			m.l2.mu.Unlock()
		}
	})
	m.mu.Unlock()
}

func (m *mtp3) setUp(up bool) {
	m.mu.Lock()
	changed := m.isUp != up
	m.isUp = up
	m.mu.Unlock()
	if changed {
		m.up(up)
	}
}

func (m *mtp3) check() {
	m.mu.Lock()
	ready := m.testOK && m.traSent && m.traGot
	m.mu.Unlock()
	if ready {
		m.setUp(true)
	}
}

func (m *mtp3) sendTRA() {
	m.mu.Lock()
	if m.traSent {
		m.mu.Unlock()
		return
	}
	m.traSent = true
	m.mu.Unlock()
	_ = m.send(siSNM, m.label(m.cfg.APC, m.cfg.SLC), []byte{0x17})
}

func (m *mtp3) receive(msu []byte) {
	if len(msu) < 1+labelSize(m.cfg.ANSI) {
		return
	}
	si := int(msu[0] & 0xF)
	l, ok := parseLabel(msu[1:], m.cfg.ANSI)
	if !ok || l.DPC != m.cfg.OPC {
		return // not for us (a PBX does not route)
	}
	data := msu[1+labelSize(m.cfg.ANSI):]
	switch si {
	case siSTM, siSpecTest:
		if len(data) < 2 {
			return
		}
		n := int(data[1] >> 4)
		pattern := data[2:]
		if len(pattern) > n {
			pattern = pattern[:n]
		}
		switch data[0] {
		case 0x11: // SLTM: answer with SLTA and the same pattern
			h1 := byte(n << 4)
			if m.cfg.ANSI {
				h1 |= byte(m.cfg.SLC & 0xF)
			}
			_ = m.send(si, Label{DPC: l.OPC, OPC: m.cfg.OPC, SLS: l.SLS}, append([]byte{0x21, h1}, pattern...))
		case 0x21: // SLTA
			if bytes.Equal(pattern, testPattern) {
				m.mu.Lock()
				first := !m.testOK
				m.testOK, m.sltFails = true, 0
				if m.sltTimer != nil {
					m.sltTimer.Stop()
				}
				m.sltTimer = time.AfterFunc(SLTPeriod, m.linkTest)
				m.mu.Unlock()
				if first {
					m.sendTRA()
				}
				m.check()
			}
		}
	case siSNM:
		if len(data) < 1 {
			return
		}
		switch data[0] {
		case 0x17: // TRA
			m.mu.Lock()
			m.traGot = true
			m.mu.Unlock()
			m.sendTRA()
			m.check()
		}
		// Other network management (TFP, TFA, COO ...): a single link to one point, nothing to reroute.
	default:
		m.mu.Lock()
		up := m.isUp
		m.mu.Unlock()
		if up {
			m.deliver(si, l, data)
		}
	}
}
