// Package isdn speaks ISDN signalling on a D-channel: primary rate (PRI, a T1 or E1 line) and basic
// rate (BRI, two B-channels, point to point or point to multipoint): the data link (Q.921 LAPD, with
// TEI management on a multipoint BRI) and basic call control (Q.931, with the parts National ISDN-2,
// 5ESS, DMS-100 and EuroISDN share for ordinary calls), as either the user side (a PBX on a phone
// company's line) or the network side (BitVoice as the network for another PBX, or for ISDN phones).
// Written from the ITU-T recommendations; tested against libpri.
package isdn

import (
	"errors"
	"sync"
	"time"
)

// Link carries D-channel frames (HDLC framing, flags and the FCS handled below it).
type Link interface {
	ReadFrame() ([]byte, error)
	WriteFrame([]byte) error
}

// Q.921 timers and counts.
var (
	T200 = time.Second      // waiting for an acknowledgement
	T203 = 10 * time.Second // with nothing heard
	N200 = 3                // retransmissions
	K    = 7                // frames sent and not yet acknowledged
)

const (
	ctlSABME = 0x6F
	ctlDM    = 0x0F
	ctlUI    = 0x03
	ctlDISC  = 0x43
	ctlUA    = 0x63
	ctlFRMR  = 0x87
	ctlRR    = 0x01
	ctlRNR   = 0x05
	ctlREJ   = 0x09
	pfU      = 0x10 // the poll/final bit in a U frame
)

// ErrDown is returned when the data link is not established.
var ErrDown = errors.New("isdn: the D-channel is down")

// lapd is one Q.921 data link, SAPI 0, on one TEI (0 on a PRI or a point-to-point BRI).
type lapd struct {
	write   func([]byte)
	network bool
	tei     int
	// onDemand (a multipoint BRI): the link is set up when there is something to send and stays
	// released when the other side releases it, rather than being kept up.
	onDemand bool
	deliver func([]byte) // a received layer 3 message
	up      func(bool)

	mu         sync.Mutex
	state      int // 0 down, 1 establishing, 2 established
	vs, va, vr int
	queue      [][]byte       // waiting to be sent (outside the window)
	unacked    map[int][]byte // sent, by N(S)
	peerBusy   bool
	rejected   bool
	retries    int
	t200, t203 *time.Timer
	closed     bool
	d200, d203 time.Duration // T200 and T203 as they were when the link was made
	n200       int
}

func newLAPD(write func([]byte), network bool, tei int, deliver func([]byte), up func(bool)) *lapd {
	return &lapd{write: write, network: network, tei: tei, deliver: deliver, up: up, unacked: map[int][]byte{}, d200: T200, d203: T203, n200: N200}
}

// address: SAPI 0, C/R as Q.921 table 1 says (commands from the network and responses from the user
// carry 1), the link's TEI.
func (l *lapd) addr(command bool) []byte {
	cr := byte(0)
	if command == l.network {
		cr = 2
	}
	return []byte{cr, byte(l.tei<<1 | 1)}
}

func (l *lapd) isCommand(a byte) bool {
	cr := a&2 != 0
	return cr != l.network // the network's commands have C/R 1, so a 1 arriving at the user is a command
}

func (l *lapd) send(f []byte) { l.write(f) }

func (l *lapd) sendU(ctl byte, command bool) { l.send(append(l.addr(command), ctl)) }

func (l *lapd) sendS(ctl byte, command, pf bool) {
	b := byte(l.vr << 1)
	if pf {
		b |= 1
	}
	l.send(append(l.addr(command), ctl, b))
}

// start begins establishing the link, and keeps trying while it is down.
func (l *lapd) start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.establish()
}

// establish sends SABME. Called with l.mu held.
func (l *lapd) establish() {
	if l.closed {
		return
	}
	l.state = 1
	l.retries = 0
	l.sendU(ctlSABME|pfU, true)
	l.armT200()
}

func (l *lapd) armT200() {
	if l.t200 != nil {
		l.t200.Stop()
	}
	l.t200 = time.AfterFunc(l.d200, l.onT200)
}

func (l *lapd) armT203() {
	if l.t203 != nil {
		l.t203.Stop()
	}
	l.t203 = time.AfterFunc(l.d203, l.onT203)
}

func (l *lapd) stopT200() {
	if l.t200 != nil {
		l.t200.Stop()
		l.t200 = nil
	}
}

func (l *lapd) onT200() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.retries++
	switch l.state {
	case 1:
		if l.retries > l.n200 && l.onDemand {
			l.state = 0
			l.queue = nil // what was waiting cannot be sent
			return
		}
		if l.retries > l.n200 {
			// Nobody answers: try again in a while, for as long as the program runs.
			l.state = 0
			l.t200 = time.AfterFunc(5*l.d200, func() { l.mu.Lock(); l.establish(); l.mu.Unlock() })
			return
		}
		l.sendU(ctlSABME|pfU, true)
		l.armT200()
	case 2:
		if l.retries > l.n200 {
			l.down()
			if !l.onDemand {
				l.establish()
			}
			return
		}
		// Timer recovery: ask the other side where it is (RR with poll), and resend what it lacks.
		l.sendS(ctlRR, true, true)
		l.armT200()
	}
}

func (l *lapd) onT203() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.state != 2 {
		return
	}
	l.sendS(ctlRR, true, true)
	l.armT200()
}

// down marks the link down and drops what was waiting. Called with l.mu held.
func (l *lapd) down() {
	was := l.state == 2
	l.state = 0
	l.queue, l.unacked = nil, map[int][]byte{}
	l.stopT200()
	if was {
		go l.up(false)
	}
}

func (l *lapd) established() {
	was := l.state == 2
	l.state = 2
	l.vs, l.va, l.vr = 0, 0, 0
	l.unacked = map[int][]byte{}
	l.peerBusy, l.rejected, l.retries = false, false, 0
	l.stopT200()
	l.armT203()
	l.pump() // what waited for the link (set up on demand)
	if !was {
		go l.up(true)
	}
}

// Send queues a layer 3 message. A link set up on demand is set up for it.
func (l *lapd) Send(msg []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != 2 && l.onDemand && !l.closed {
		l.queue = append(l.queue, msg)
		if l.state == 0 {
			l.establish()
		}
		return nil
	}
	if l.state != 2 {
		return ErrDown
	}
	l.queue = append(l.queue, msg)
	l.pump()
	return nil
}

// pump sends queued I frames while the window allows, and reports whether it sent any (each carries
// N(R), acknowledging what has arrived). Called with l.mu held.
func (l *lapd) pump() bool {
	sent := false
	for len(l.queue) > 0 && !l.peerBusy && (l.vs-l.va+128)%128 < K {
		msg := l.queue[0]
		l.queue = l.queue[1:]
		f := append(l.addr(true), byte(l.vs<<1), byte(l.vr<<1))
		f = append(f, msg...)
		l.unacked[l.vs] = f
		l.vs = (l.vs + 1) % 128
		l.send(f)
		sent = true
		if l.t200 == nil {
			l.armT200()
		}
	}
	return sent
}

// ack takes an N(R): everything before it has arrived. Called with l.mu held.
func (l *lapd) ack(nr int) {
	if (nr-l.va+128)%128 > (l.vs-l.va+128)%128 {
		return // an N(R) outside what was sent: ignored
	}
	for l.va != nr {
		delete(l.unacked, l.va)
		l.va = (l.va + 1) % 128
		l.retries = 0
	}
	if l.va == l.vs {
		l.stopT200()
		l.armT203()
	} else {
		l.armT200()
	}
}

// resend sends again everything unacknowledged from N(R) on. Called with l.mu held.
func (l *lapd) resend(from int) {
	for n := from; n != l.vs; n = (n + 1) % 128 {
		if f, ok := l.unacked[n]; ok {
			g := append([]byte(nil), f...)
			g[3] = byte(l.vr << 1)
			l.send(g)
		}
	}
	if l.va != l.vs {
		l.armT200()
	}
}

// fail is the frame link failing: the data link is down for good.
func (l *lapd) fail() {
	l.mu.Lock()
	l.closed = true
	l.down()
	if l.t203 != nil {
		l.t203.Stop()
	}
	l.mu.Unlock()
}

func (l *lapd) close() {
	l.mu.Lock()
	l.closed = true
	l.stopT200()
	if l.t203 != nil {
		l.t203.Stop()
	}
	l.mu.Unlock()
}

// receive takes a frame for this link (SAPI 0, its TEI).
func (l *lapd) receive(f []byte) {
	cmd := l.isCommand(f[0])
	ctl := f[2]
	l.mu.Lock()
	var out [][]byte
	defer func() {
		l.mu.Unlock()
		for _, m := range out {
			l.deliver(m)
		}
	}()
	switch {
	case ctl&1 == 0: // I frame
		if len(f) < 4 || l.state != 2 {
			if l.state != 2 && cmd {
				l.sendU(ctlDM|pfU, false)
			}
			return
		}
		ns, nr, p := int(ctl>>1), int(f[3]>>1), f[3]&1 != 0
		l.ack(nr)
		if ns == l.vr {
			l.vr = (l.vr + 1) % 128
			l.rejected = false
			out = append(out, append([]byte(nil), f[4:]...))
			if !l.pump() || p {
				l.sendS(ctlRR, false, p)
			}
		} else if !l.rejected {
			l.rejected = true
			l.sendS(ctlREJ, false, p)
		} else if p {
			l.sendS(ctlRR, false, true)
		}
		l.armT203()
	case ctl&3 == 1: // S frame
		if len(f) < 4 || l.state != 2 {
			if l.state != 2 && cmd && len(f) >= 4 && f[3]&1 != 0 {
				l.sendU(ctlDM|pfU, false)
			}
			return
		}
		nr, pf := int(f[3]>>1), f[3]&1 != 0
		switch ctl {
		case ctlRR:
			l.peerBusy = false
		case ctlRNR:
			l.peerBusy = true
		case ctlREJ:
			l.peerBusy = false
		}
		l.ack(nr)
		if cmd && pf {
			l.sendS(ctlRR, false, true)
		}
		if ctl == ctlREJ || (!cmd && pf) {
			l.resend(nr) // the answer to our enquiry, or a rejection: send again what it lacks
		}
		if !cmd && pf && l.va == l.vs {
			l.stopT200()
		}
		l.armT203()
		l.pump()
	default: // U frame
		switch ctl &^ pfU {
		case ctlSABME:
			l.sendU(ctlUA|ctl&pfU, false)
			l.established()
		case ctlUA:
			if l.state == 1 {
				l.established()
			}
		case ctlDISC:
			l.sendU(ctlUA|ctl&pfU, false)
			l.down()
			if !l.onDemand {
				l.establish()
			}
		case ctlDM:
			if l.state == 2 {
				l.down()
			}
			if l.state != 1 && !l.onDemand {
				l.establish()
			}
		case ctlFRMR:
			l.down()
			if !l.onDemand {
				l.establish()
			}
		}
	}
}
