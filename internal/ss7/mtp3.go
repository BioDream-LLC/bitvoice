package ss7

import (
	"bytes"
	"errors"
	"sync"
	"time"
)

// MTP3 for one link set to an adjacent signalling point (the usual for a PBX on a carrier's SS7:
// A-links to its STP, or F-links to its switch): routing labels, the link test (Q.707 SLTM/SLTA),
// traffic restart allowed (TRA), and user part messages in and out. A link set has up to 16
// signalling links (each its own signalling link code); traffic is shared between the links in
// service by the label's SLS, and when a link fails its traffic changes over to the others
// (Q.704 5: COO/COA with the retrieval of what it had not got acknowledged, or ECO/ECA, or by time
// when the far end does not answer), and changes back when it returns (Q.704 6: CBD/CBA).

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

// Signalling network management messages (Q.704 15.4, H0 low nibble, H1 high).
const (
	snmCOO = 0x11
	snmCOA = 0x21
	snmCBD = 0x51
	snmCBA = 0x61
	snmECO = 0x12
	snmECA = 0x22
	snmTRA = 0x17
)

// MaxLinks is the most signalling links in a link set (Q.704: SLCs 0 to 15).
const MaxLinks = 16

// Label is a routing label.
type Label struct {
	DPC, OPC int
	SLS      int
}

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
	if len(b) < labelSize(ansi) {
		return Label{}, false
	}
	if ansi {
		return Label{DPC: int(b[0]) | int(b[1])<<8 | int(b[2])<<16, OPC: int(b[3]) | int(b[4])<<8 | int(b[5])<<16, SLS: int(b[6] & 0x1F)}, true
	}
	v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return Label{DPC: int(v & 0x3FFF), OPC: int(v >> 14 & 0x3FFF), SLS: int(v >> 28)}, true
}

// Q.707 and Q.704 timers.
var (
	SLTWait   = 4 * time.Second  // Q.707 T1: waiting for SLTA
	SLTPeriod = 30 * time.Second // Q.707 T2: between link tests
	T2MTP3    = 2 * time.Second  // Q.704 T2: waiting for a changeover acknowledgement
	T1MTP3    = time.Second      // Q.704 T1: time-controlled changeover, before traffic moves
	T4MTP3    = time.Second      // Q.704 T4: waiting for a changeback acknowledgement
)

var testPattern = []byte{0x42, 0x69, 0x74, 0x56, 0x6F, 0x69, 0x63, 0x65} // "BitVoice"

// MTPConfig is the link set.
type MTPConfig struct {
	ANSI bool
	OPC  int // our point code
	APC  int // the adjacent point code
	NI   int // network indicator: NINational (the usual) or NIInternational
	// SLC is the signalling link code of a link set's only link (New); AddLink names each link's own.
	SLC int
	// Emergency aligns with the emergency proving period: always for a link set of one link (Q.704);
	// a link set of several aligns normally unless this is set.
	Emergency bool
}

// ErrLinkSetFull: a link set has no room for another link, or the code is in use.
var ErrLinkSetFull = errors.New("the link set already has 16 links, or one with this signalling link code")

// Link states for changeover.
const (
	coNone       = iota // in service, or never was
	coWaiting           // failed: COO or ECO sent, waiting for COA/ECA
	coDone              // changed over: its traffic goes on the other links
	coChangeback        // back in service: CBD sent, waiting for CBA
)

type sigLink struct {
	slc      int
	l2       *mtp2
	testOK   bool // the link test passed since it came in service
	sltTimer *time.Timer
	sltFails int
	co       int // coNone ... coChangeback
	coTimer  *time.Timer
	cbCode   byte
	left     leftover // what it held when it failed, until the changeover sends it on
	added    bool
}

// available: in service and tested, carrying traffic.
func (l *sigLink) available() bool { return l.testOK && l.co == coNone }

type mtp3 struct {
	cfg     MTPConfig
	deliver func(si int, l Label, data []byte) // user part messages (ISUP)
	up      func(bool)

	mu      sync.Mutex
	links   []*sigLink
	traSent bool
	traGot  bool
	isUp    bool
	closed  bool
	cbSeq   byte
}

func newLinkSet(cfg MTPConfig, deliver func(int, Label, []byte), up func(bool)) *mtp3 {
	return &mtp3{cfg: cfg, deliver: deliver, up: up}
}

// add makes a signalling link of the link set over a frame link.
func (m *mtp3) add(link Link, slc int) (*sigLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.links) >= MaxLinks || slc < 0 || slc >= MaxLinks {
		return nil, ErrLinkSetFull
	}
	for _, l := range m.links {
		if l.slc == slc {
			return nil, ErrLinkSetFull
		}
	}
	sl := &sigLink{slc: slc, added: true}
	emergency := m.cfg.Emergency || len(m.links) == 0
	sl.l2 = newMTP2(link, emergency, func(msu []byte) { m.receive(sl, msu) }, func(up bool) { m.l2Up(sl, up) })
	m.links = append(m.links, sl)
	return sl, nil
}

// remove takes a link out of the link set (its frame link has gone).
func (m *mtp3) remove(sl *sigLink) {
	m.mu.Lock()
	for i, l := range m.links {
		if l == sl {
			m.links = append(m.links[:i], m.links[i+1:]...)
			break
		}
	}
	stopTimer(&sl.sltTimer)
	stopTimer(&sl.coTimer)
	m.mu.Unlock()
	m.check()
}

func stopTimer(t **time.Timer) {
	if *t != nil {
		(*t).Stop()
		*t = nil
	}
}

func (m *mtp3) sio(si int) byte {
	b := byte(si&0xF) | byte(m.cfg.NI&3)<<6
	if m.cfg.ANSI && si != siISUP {
		b |= 3 << 4 // network management at the highest priority (ANSI)
	}
	return b
}

func (m *mtp3) label(dpc, sls int) Label { return Label{DPC: dpc, OPC: m.cfg.OPC, SLS: sls} }

func (m *mtp3) msu(si int, l Label, data []byte) []byte {
	return append(append([]byte{m.sio(si)}, l.bytes(m.cfg.ANSI)...), data...)
}

// pick is the link for an SLS: the link set's links in service share the SLS values (load
// sharing; the SLS goes through its Gray code first, so the odd or even circuits one end controls,
// whose SLS values are all odd or all even, still spread over every link); nil when none is in
// service. Called with m.mu held.
func (m *mtp3) pick(sls int) *sigLink {
	var av []*sigLink
	for _, l := range m.links {
		if l.available() {
			av = append(av, l)
		}
	}
	if len(av) == 0 {
		return nil
	}
	return av[(sls^sls>>1)%len(av)]
}

// send sends a user part (or test) message on the link its SLS gives.
func (m *mtp3) send(si int, l Label, data []byte) error {
	m.mu.Lock()
	sl := m.pick(l.SLS)
	m.mu.Unlock()
	if sl == nil {
		return ErrDown
	}
	return sl.l2.Send(m.msu(si, l, data))
}

// sendOn sends on one link, whatever its state (the link test, and messages about that link).
func (m *mtp3) sendOn(sl *sigLink, si int, l Label, data []byte) error {
	return sl.l2.Send(m.msu(si, l, data))
}

// sendAbout sends a network management message about link sl (its SLC as the label's SLS) on
// another link in service, else on sl itself. False when no link can carry it.
func (m *mtp3) sendAbout(sl *sigLink, data []byte) bool {
	m.mu.Lock()
	var via *sigLink
	for _, l := range m.links {
		if l != sl && l.available() {
			via = l
			break
		}
	}
	m.mu.Unlock()
	if via == nil {
		return false
	}
	return m.sendOn(via, siSNM, m.label(m.cfg.APC, sl.slc), data) == nil
}

func (m *mtp3) l2Up(sl *sigLink, up bool) {
	m.mu.Lock()
	sl.testOK = false
	stopTimer(&sl.sltTimer)
	if !up {
		sl.left = sl.l2.takeLeftover()
	}
	closed := m.closed
	m.mu.Unlock()
	if closed {
		m.check()
		return
	}
	if up {
		m.linkTest(sl)
		return
	}
	m.changeover(sl)
	m.check()
}

// linkTest sends an SLTM on a link; an SLTA with the same pattern proves it.
func (m *mtp3) linkTest(sl *sigLink) {
	si := siSTM
	if m.cfg.ANSI {
		si = siSpecTest
	}
	h1 := byte(len(testPattern) << 4)
	if m.cfg.ANSI {
		h1 |= byte(sl.slc & 0xF)
	}
	_ = m.sendOn(sl, si, m.label(m.cfg.APC, sl.slc), append([]byte{0x11, h1}, testPattern...))
	m.mu.Lock()
	stopTimer(&sl.sltTimer)
	sl.sltTimer = time.AfterFunc(SLTWait, func() {
		m.mu.Lock()
		ok := sl.testOK
		sl.sltFails++
		fails := sl.sltFails
		m.mu.Unlock()
		if !ok && fails < 2 {
			m.linkTest(sl) // Q.707: try once more
		} else if !ok {
			sl.l2.mu.Lock()
			sl.l2.failed()
			sl.l2.mu.Unlock()
		}
	})
	m.mu.Unlock()
}

// tested: a link passed its link test and may carry traffic (after changing back, when its traffic
// had moved to other links).
func (m *mtp3) tested(sl *sigLink) {
	m.mu.Lock()
	first := !sl.testOK
	sl.testOK, sl.sltFails = true, 0
	stopTimer(&sl.sltTimer)
	sl.sltTimer = time.AfterFunc(SLTPeriod, func() { m.linkTest(sl) })
	co := sl.co
	m.mu.Unlock()
	if !first {
		return
	}
	if co == coWaiting || co == coDone {
		m.changeback(sl)
	}
	m.sendTRA(sl)
	m.check()
}

func (m *mtp3) setUp(up bool) {
	m.mu.Lock()
	changed := m.isUp != up
	m.isUp = up
	if !up {
		m.traSent, m.traGot = false, false
	}
	m.mu.Unlock()
	if changed {
		m.up(up)
	}
}

// check: the link set is up while one of its links carries traffic and the restart (TRA both
// ways) is done; down when none is left.
func (m *mtp3) check() {
	m.mu.Lock()
	any := false
	for _, l := range m.links {
		if l.available() {
			any = true
		}
	}
	ready := any && m.traSent && m.traGot
	m.mu.Unlock()
	if ready {
		m.setUp(true)
	} else if !any {
		m.setUp(false)
	}
}

// sendTRA sends traffic restart allowed once per restart of the link set.
func (m *mtp3) sendTRA(sl *sigLink) {
	m.mu.Lock()
	if m.traSent {
		m.mu.Unlock()
		return
	}
	m.traSent = true
	m.mu.Unlock()
	_ = m.sendOn(sl, siSNM, m.label(m.cfg.APC, sl.slc), []byte{snmTRA})
}

// fsnByte is a changeover message's FSN field: ITU one octet; ANSI the SLC and the FSN in two.
func (m *mtp3) fsnField(slc, fsn int) []byte {
	if m.cfg.ANSI {
		return []byte{byte(slc&0xF) | byte(fsn&0xF)<<4, byte(fsn>>4) & 0x7}
	}
	return []byte{byte(fsn & 0x7F)}
}

func (m *mtp3) readFSN(b []byte) (int, bool) {
	if m.cfg.ANSI && len(b) >= 2 {
		return int(b[0]>>4) | int(b[1]&0x7)<<4, true
	}
	if len(b) >= 1 {
		return int(b[0] & 0x7F), true
	}
	return 0, false
}

// changeover moves a failed link's traffic to the others: COO with the last FSN it accepted, then
// on COA the messages the far end had not received are sent again on the others, with those never
// sent; with no answer in T2, time-controlled changeover (only those never sent). With no other link
// in service there is nowhere to move it: the link set goes down instead.
func (m *mtp3) changeover(sl *sigLink) {
	m.mu.Lock()
	if sl.co == coChangeback {
		sl.co = coDone
	}
	if sl.co != coNone {
		m.mu.Unlock()
		return
	}
	sl.co = coWaiting
	bsn := sl.left.bsn
	if !sl.left.ok {
		bsn = sl.l2.lastAccepted()
	}
	m.mu.Unlock()
	if !m.sendAbout(sl, append([]byte{snmCOO}, m.fsnField(sl.slc, bsn)...)) {
		m.mu.Lock()
		sl.co, sl.left = coDone, leftover{} // nothing to send it on: lost with the link set
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	stopTimer(&sl.coTimer)
	sl.coTimer = time.AfterFunc(T2MTP3, func() { m.timedChangeover(sl) })
	m.mu.Unlock()
}

// changedOver completes a changeover: the far end has accepted up to fsn on the failed link.
func (m *mtp3) changedOver(sl *sigLink, fsn int, retrieve bool) {
	m.mu.Lock()
	stopTimer(&sl.coTimer)
	if sl.co == coDone {
		m.mu.Unlock()
		return
	}
	sl.co = coDone
	left := sl.left
	sl.left = leftover{}
	m.mu.Unlock()
	var again [][]byte
	if retrieve && left.ok {
		for i, msu := range left.rtb {
			if (left.rtbFirst+i-fsn-1+128)%128 < 64 { // after the last the far end accepted
				again = append(again, msu)
			}
		}
	}
	again = append(again, left.queue...)
	for _, msu := range again {
		m.resend(msu)
	}
	m.check()
}

// resend sends a retrieved message again on the link its SLS now gives.
func (m *mtp3) resend(msu []byte) {
	if len(msu) < 1+labelSize(m.cfg.ANSI) {
		return
	}
	l, _ := parseLabel(msu[1:], m.cfg.ANSI)
	m.mu.Lock()
	sl := m.pick(l.SLS)
	m.mu.Unlock()
	if sl != nil {
		_ = sl.l2.Send(msu)
	}
}

func (m *mtp3) timedChangeover(sl *sigLink) {
	m.mu.Lock()
	waiting := sl.co == coWaiting
	m.mu.Unlock()
	if waiting {
		time.AfterFunc(T1MTP3, func() { m.changedOver(sl, 0, false) })
	}
}

// changeback brings a restored link's traffic back: CBD on another link, its traffic on the link
// again when CBA comes (or after T4 regardless). With no other link in service, at once.
func (m *mtp3) changeback(sl *sigLink) {
	m.mu.Lock()
	stopTimer(&sl.coTimer)
	sl.left = leftover{}
	m.cbSeq++
	sl.cbCode, sl.co = m.cbSeq, coChangeback
	code := sl.cbCode
	m.mu.Unlock()
	data := []byte{snmCBD}
	if m.cfg.ANSI {
		data = append(data, byte(sl.slc&0xF)|code<<4, code>>4)
	} else {
		data = append(data, code)
	}
	if !m.sendAbout(sl, data) {
		m.changedBack(sl)
		return
	}
	m.mu.Lock()
	sl.coTimer = time.AfterFunc(T4MTP3, func() { m.changedBack(sl) })
	m.mu.Unlock()
}

func (m *mtp3) changedBack(sl *sigLink) {
	m.mu.Lock()
	stopTimer(&sl.coTimer)
	if sl.co == coChangeback {
		sl.co = coNone
	}
	m.mu.Unlock()
	m.check()
}

// bySLC is the link a network management message is about (its label's SLS is the SLC).
func (m *mtp3) bySLC(slc int) *sigLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.links {
		if l.slc == slc {
			return l
		}
	}
	return nil
}

func (m *mtp3) receive(on *sigLink, msu []byte) {
	if len(msu) < 1+labelSize(m.cfg.ANSI) {
		return
	}
	si := int(msu[0] & 0xF)
	l, ok := parseLabel(msu[1:], m.cfg.ANSI)
	if !ok || l.DPC != m.cfg.OPC {
		return // not for us (a PBX does not route)
	}
	data := msu[1+labelSize(m.cfg.ANSI):]
	reply := Label{DPC: l.OPC, OPC: m.cfg.OPC, SLS: l.SLS}
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
		case 0x11: // SLTM: answer with SLTA and the same pattern, on the same link
			h1 := byte(n << 4)
			if m.cfg.ANSI {
				h1 |= byte(on.slc & 0xF)
			}
			_ = m.sendOn(on, si, reply, append([]byte{0x21, h1}, pattern...))
		case 0x21: // SLTA
			if bytes.Equal(pattern, testPattern) {
				m.tested(on)
			}
		}
	case siSNM:
		if len(data) < 1 {
			return
		}
		about := m.bySLC(l.SLS)
		if m.cfg.ANSI && len(data) >= 2 && (data[0] == snmCOO || data[0] == snmCOA || data[0] == snmCBD || data[0] == snmCBA) {
			about = m.bySLC(int(data[1] & 0xF)) // ANSI names the link in the message
		}
		switch data[0] {
		case snmTRA:
			m.mu.Lock()
			m.traGot = true
			m.mu.Unlock()
			m.sendTRA(on)
			m.check()
		case snmCOO, snmECO: // the far end's link failed: acknowledge, and move its traffic
			if about == nil {
				return
			}
			fsn, _ := m.readFSN(data[1:])
			m.mu.Lock()
			if about.co == coNone || about.co == coChangeback {
				// Our side may not have seen the failure yet: take what the link holds now.
				stopTimer(&about.coTimer)
				about.co = coWaiting
				if !about.left.ok {
					about.left = about.l2.takeLeftover()
				}
			}
			ours := about.left.bsn
			if !about.left.ok {
				ours = about.l2.lastAccepted()
			}
			m.mu.Unlock()
			if data[0] == snmECO {
				_ = m.sendOn(on, siSNM, reply, []byte{snmECA})
				m.changedOver(about, 0, false)
				return
			}
			_ = m.sendOn(on, siSNM, reply, append([]byte{snmCOA}, m.fsnField(about.slc, ours)...))
			m.changedOver(about, fsn, true)
		case snmCOA:
			if about != nil {
				fsn, _ := m.readFSN(data[1:])
				m.changedOver(about, fsn, true)
			}
		case snmECA:
			if about != nil {
				m.changedOver(about, 0, false)
			}
		case snmCBD: // the far end's link is back: acknowledge with the same code
			if len(data) < 2 {
				return
			}
			_ = m.sendOn(on, siSNM, reply, append([]byte{snmCBA}, data[1:]...))
			if about != nil {
				m.mu.Lock()
				back := about.testOK
				if back {
					about.co = coChangeback
				}
				m.mu.Unlock()
				if back {
					m.changedBack(about)
				}
			}
		case snmCBA:
			if about != nil {
				m.changedBack(about)
			}
		}
		// Other network management (TFP, TFA ...): the adjacent point is the only destination.
	default:
		m.mu.Lock()
		up := m.isUp
		m.mu.Unlock()
		if up {
			m.deliver(si, l, data)
		}
	}
}

// close stops every link.
func (m *mtp3) close() {
	m.mu.Lock()
	m.closed = true
	links := append([]*sigLink(nil), m.links...)
	m.mu.Unlock()
	for _, l := range links {
		l.l2.close()
	}
}
