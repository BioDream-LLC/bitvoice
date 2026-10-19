// Package ss7 speaks Signalling System No. 7 on a signalling link of a T1 or E1: MTP level 2 (ITU-T
// Q.703, basic error correction), MTP level 3 for one link set to an adjacent signalling point
// (Q.704 routing labels, Q.707 link test, traffic restart), and ISUP basic call control (Q.763 formats,
// Q.764 procedures) for the voice circuits, each named by its circuit identification code (CIC). ITU
// and ANSI (T1.111, T1.113) routing labels and messages. Written from the recommendations; tested
// against libss7.
package ss7

import (
	"errors"
	"sync"
	"time"
)

// Link carries signal units (the HDLC flags and FCS handled below it).
type Link interface {
	ReadFrame() ([]byte, error)
	WriteFrame([]byte) error
}

// MTP2 timers (Q.703 12.3; ANSI T1.111.3).
var (
	T1MTP2 = 45 * time.Second // aligned/ready: waiting for the far end to be in service
	T2MTP2 = 50 * time.Second // not aligned: waiting for alignment
	T3MTP2 = 1500 * time.Millisecond
	T4N    = 8500 * time.Millisecond // proving period, normal
	T4E    = 500 * time.Millisecond  // proving period, emergency
	T7MTP2 = 1250 * time.Millisecond // excessive delay of acknowledgement
	// Fill is how often a fill-in signal unit (FISU or the current LSSU) is sent when nothing else is.
	Fill = 20 * time.Millisecond
)

// LSSU status indications.
const (
	sio  = 0
	sin  = 1
	sie  = 2
	sios = 3
	sipo = 4
	sib  = 5
)

// MTP2 states.
const (
	l2OutOfService = iota
	l2NotAligned
	l2Aligned
	l2Proving
	l2AlignedReady
	l2InService
)

// ErrDown is returned when the link is not in service.
var ErrDown = errors.New("ss7: the signalling link is not in service")

type mtp2 struct {
	link      Link
	deliver   func([]byte) // a received MSU: SIO and SIF
	up        func(bool)
	emergency bool

	wmu sync.Mutex // one frame on the wire at a time

	mu        sync.Mutex
	state     int
	fsn, fib  int // our last sent FSN, and our forward indicator bit
	bsn, bib  int // the last FSN accepted from the far end, and our backward indicator bit
	lastAcked int // the far end's last BSN
	rtb       [][]byte
	rtbFirst  int // the FSN of rtb[0]
	queue     [][]byte
	lastRxSU  int // the last LSSU type heard, -1 for none
	t1, t2    *time.Timer
	t3, t4    *time.Timer
	t7        *time.Timer
	fill      *time.Ticker
	closed    bool
	sending   int // the LSSU being sent while not in service; -1: FISUs
	gotFISU   bool
	retrans   bool // the far end asked for retransmission and we are doing it
	left      leftover // what the link held when it failed, until MTP3 takes it

	// Link up and down reach MTP3 in the order they happened: each change is numbered, and a late
	// one (sent from a goroutine) is dropped if a newer one has already been told.
	upGen, upTold int
	upMu          sync.Mutex
}

// tellUp numbers a change of the link's state; called with m.mu held. Pass the number to report.
// leftover is what a failed link held: for MTP3 to send on another link of the link set.
type leftover struct {
	rtb      [][]byte
	rtbFirst int
	queue    [][]byte
	bsn      int
	ok       bool
}

// takeLeftover hands over (once) what the link held when it last failed.
func (m *mtp2) takeLeftover() leftover {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.left
	m.left = leftover{}
	return l
}

// lastAccepted is the FSN of the last MSU accepted from the far end (for a COA).
func (m *mtp2) lastAccepted() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.left.ok {
		return m.left.bsn
	}
	return m.bsn
}

func (m *mtp2) tellUp() int { m.upGen++; return m.upGen }

// report tells MTP3 change gen, unless a newer one was told first. Called without m.mu.
func (m *mtp2) report(gen int, up bool) {
	m.upMu.Lock()
	defer m.upMu.Unlock()
	if gen <= m.upTold {
		return
	}
	m.upTold = gen
	m.up(up)
}

func newMTP2(link Link, emergency bool, deliver func([]byte), up func(bool)) *mtp2 {
	return &mtp2{link: link, emergency: emergency, deliver: deliver, up: up, fsn: 127, bsn: 127, fib: 1, bib: 1,
		lastAcked: 127, rtbFirst: 0, lastRxSU: -1, sending: sios}
}

func (m *mtp2) write(f []byte) {
	m.wmu.Lock()
	_ = m.link.WriteFrame(f)
	m.wmu.Unlock()
}

// header writes BSN/BIB, FSN/FIB and LI. Called with m.mu held.
func (m *mtp2) header(fsn, li int) []byte {
	return []byte{byte(m.bsn | m.bib<<7), byte(fsn | m.fib<<7), byte(li & 0x3F)}
}

func (m *mtp2) lssu(status int) []byte { return append(m.header(m.fsn, 1), byte(status)) }

func (m *mtp2) fisu() []byte { return m.header(m.fsn, 0) }

// start begins alignment, and keeps a fill-in signal unit going.
func (m *mtp2) start() {
	m.mu.Lock()
	m.fill = time.NewTicker(Fill)
	m.mu.Unlock()
	go func() {
		for range m.fill.C {
			m.mu.Lock()
			if m.closed {
				m.mu.Unlock()
				return
			}
			var f []byte
			if m.sending >= 0 {
				f = m.lssu(m.sending)
			} else {
				f = m.fisu()
			}
			m.mu.Unlock()
			m.write(f)
		}
	}()
	m.mu.Lock()
	m.align()
	m.mu.Unlock()
}

func stop(t **time.Timer) {
	if *t != nil {
		(*t).Stop()
		*t = nil
	}
}

func (m *mtp2) stopAll() {
	stop(&m.t1)
	stop(&m.t2)
	stop(&m.t3)
	stop(&m.t4)
	stop(&m.t7)
}

// align (re)starts initial alignment: SIO until the far end is heard. Called with m.mu held.
func (m *mtp2) align() {
	if m.closed {
		return
	}
	was := m.state == l2InService
	m.stopAll()
	m.state = l2NotAligned
	m.sending = sio
	m.fsn, m.bsn, m.fib, m.bib, m.lastAcked = 127, 127, 1, 1, 127
	m.rtb, m.queue, m.lastRxSU, m.gotFISU = nil, nil, -1, false
	m.t2 = time.AfterFunc(T2MTP2, func() { m.mu.Lock(); m.failed(); m.mu.Unlock() })
	if was {
		g := m.tellUp()
		go m.report(g, false)
	}
}

// failed: the link failed; out of service (SIOS) for a moment, then alignment again. Called with m.mu held.
func (m *mtp2) failed() {
	if m.closed {
		return
	}
	was := m.state == l2InService
	m.stopAll()
	m.state = l2OutOfService
	m.sending = sios
	if was {
		// Kept for MTP3's changeover (Q.704 5.4): what was sent and not acknowledged, with the FSN of
		// the first, and what was not sent yet; and the last FSN accepted from the far end.
		m.left = leftover{rtb: m.rtb, rtbFirst: m.rtbFirst, queue: m.queue, bsn: m.bsn, ok: true}
	}
	m.rtb, m.queue = nil, nil
	if was {
		g := m.tellUp()
		go m.report(g, false)
	}
	time.AfterFunc(10*Fill, func() { m.mu.Lock(); m.align(); m.mu.Unlock() })
}

func (m *mtp2) proving() time.Duration {
	if m.emergency {
		return T4E
	}
	return T4N
}

func (m *mtp2) receive(f []byte) {
	if len(f) < 3 {
		return
	}
	bsn, bib := int(f[0]&0x7F), int(f[0]>>7)
	fsn, fib := int(f[1]&0x7F), int(f[1]>>7)
	li := int(f[2] & 0x3F)
	m.mu.Lock()
	var out []byte
	nowUp := 0
	defer func() {
		m.mu.Unlock()
		// In order: MTP3 hears that the link is in service before the first message on it, or the
		// link test and traffic restart that message carries would be undone by a late "link up".
		if nowUp > 0 {
			m.report(nowUp, true)
		}
		if out != nil {
			m.deliver(out)
		}
	}()
	switch {
	case li == 1 || li == 2:
		if len(f) < 4 {
			return
		}
		m.lssuIn(int(f[3] & 7))
		return
	}
	// FISU or MSU.
	switch m.state {
	case l2AlignedReady:
		m.inService()
		nowUp = m.tellUp()
	case l2InService:
	default:
		return
	}
	m.ack(bsn, bib)
	if li == 0 {
		return
	}
	// An MSU: accept it if it is the next, ask again if one was lost.
	next := (m.bsn + 1) % 128
	switch {
	case fsn == next && fib == m.bib:
		m.bsn = fsn
		out = append([]byte(nil), f[3:]...)
	case fsn == m.bsn:
		// a retransmitted one we already have
	case fib == m.bib:
		// one lost: negative acknowledgement, the BIB turned over
		m.bib ^= 1
	}
}

func (m *mtp2) lssuIn(status int) {
	if status == m.lastRxSU && m.state != l2InService && m.state != l2AlignedReady {
		return
	}
	m.lastRxSU = status
	if status == sie {
		// the far end aligns in emergency: so do we (Q.703 7.3)
	}
	switch m.state {
	case l2OutOfService:
		// waiting to restart; nothing
	case l2NotAligned:
		if status != sio && status != sin && status != sie {
			return
		}
		stop(&m.t2)
		if status == sio {
			m.state = l2Aligned
			m.sending = m.alignSU()
			m.t3 = time.AfterFunc(T3MTP2, func() { m.mu.Lock(); m.failed(); m.mu.Unlock() })
			return
		}
		m.startProving()
	case l2Aligned:
		switch status {
		case sios:
			m.failed()
		case sin, sie:
			stop(&m.t3)
			m.startProving()
		}
	case l2Proving:
		switch status {
		case sios:
			m.failed()
		case sio:
			stop(&m.t4)
			m.state = l2Aligned
			m.t3 = time.AfterFunc(T3MTP2, func() { m.mu.Lock(); m.failed(); m.mu.Unlock() })
		}
	case l2AlignedReady, l2InService:
		if status == sio || status == sios {
			m.failed()
		}
		// SIB (busy): the far end is congested; T7 is not restarted (Q.703 9.3), kept simple here.
		if status == sib && m.t7 != nil {
			m.t7.Reset(T7MTP2)
		}
	}
}

func (m *mtp2) alignSU() int {
	if m.emergency {
		return sie
	}
	return sin
}

func (m *mtp2) startProving() {
	m.state = l2Proving
	m.sending = m.alignSU()
	m.t4 = time.AfterFunc(m.proving(), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.state != l2Proving {
			return
		}
		// Proved: FISUs, and in service when the far end is too.
		m.state = l2AlignedReady
		m.sending = -1
		m.t1 = time.AfterFunc(T1MTP2, m.lockedFailed)
	})
}

func (m *mtp2) lockedFailed() { m.mu.Lock(); m.failed(); m.mu.Unlock() }

// inService puts the link in service; the caller tells MTP3 once m.mu is released. Called with m.mu held.
func (m *mtp2) inService() {
	stop(&m.t1)
	m.state = l2InService
	m.sending = -1
}

// ack takes the far end's BSN and BIB: acknowledged MSUs leave the retransmission buffer; a BIB
// different from our FIB asks for everything after BSN again. Called with m.mu held.
func (m *mtp2) ack(bsn, bib int) {
	// Acknowledged: rtb[0..] holds FSNs rtbFirst, rtbFirst+1, ...
	n := (bsn - m.rtbFirst + 1 + 128) % 128
	if len(m.rtb) > 0 && n <= len(m.rtb) {
		m.rtb = m.rtb[n:]
		m.rtbFirst = (bsn + 1) % 128
		if len(m.rtb) == 0 {
			stop(&m.t7)
		} else if m.t7 != nil {
			m.t7.Reset(T7MTP2)
		}
	}
	m.lastAcked = bsn
	if bib != m.fib {
		// negative acknowledgement: send again everything not acknowledged, FIB turned over
		m.fib = bib
		for i, msu := range m.rtb {
			fsn := (m.rtbFirst + i) % 128
			f := append(m.header(fsn, len(msu)), msu...)
			m.wmu.Lock()
			_ = m.link.WriteFrame(f)
			m.wmu.Unlock()
		}
	}
	m.pump()
}

// pump sends queued MSUs (up to 127 unacknowledged). Called with m.mu held.
func (m *mtp2) pump() {
	for len(m.queue) > 0 && len(m.rtb) < 127 && m.state == l2InService {
		msu := m.queue[0]
		m.queue = m.queue[1:]
		m.fsn = (m.fsn + 1) % 128
		if len(m.rtb) == 0 {
			m.rtbFirst = m.fsn
		}
		m.rtb = append(m.rtb, msu)
		li := len(msu)
		if li > 63 {
			li = 63
		}
		f := append(m.header(m.fsn, li), msu...)
		if m.t7 == nil {
			m.t7 = time.AfterFunc(T7MTP2, m.lockedFailed)
		}
		m.wmu.Lock()
		_ = m.link.WriteFrame(f)
		m.wmu.Unlock()
	}
}

// Send queues an MSU (SIO and SIF).
func (m *mtp2) Send(msu []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != l2InService {
		return ErrDown
	}
	m.queue = append(m.queue, append([]byte(nil), msu...))
	m.pump()
	return nil
}

func (m *mtp2) inServiceNow() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state == l2InService
}

func (m *mtp2) run() error {
	for {
		f, err := m.link.ReadFrame()
		if err != nil {
			m.close()
			return err
		}
		m.receive(f)
	}
}

func (m *mtp2) close() {
	m.mu.Lock()
	was := m.state == l2InService
	m.closed = true
	m.stopAll()
	if m.fill != nil {
		m.fill.Stop()
	}
	m.state = l2OutOfService
	g := m.tellUp()
	m.mu.Unlock()
	if was {
		m.report(g, false)
	}
}
