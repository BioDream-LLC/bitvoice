package isdn

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// The frames of one D-channel: one data link (TEI 0) on a PRI or a point-to-point BRI; on a
// point-to-multipoint BRI, TEI management (Q.921 5.3) and a data link per terminal, plus broadcast
// frames (TEI 127) carrying a SETUP to every terminal at once.

// TEI management timers (Q.921 5.3).
var (
	T202 = 2 * time.Second // waiting for a TEI to be assigned
	N202 = 3
)

const (
	teiBroadcast = 127
	sapiCall     = 0
	sapiMgmt     = 63

	teiRequest       = 1
	teiAssigned      = 2
	teiDenied        = 3
	teiCheckRequest  = 4
	teiCheckResponse = 5
	teiRemove        = 6
	teiVerify        = 7
)

type datalink struct {
	link    Link
	network bool
	ptmp    bool
	deliver func(tei int, msg []byte) // a layer 3 message from a TEI (127: broadcast)
	up      func(bool)

	wmu sync.Mutex

	mu      sync.Mutex
	links   map[int]*lapd
	tei     int // the user side's own TEI on a multipoint line; -1 until assigned
	ri      uint16
	tries   int
	t202    *time.Timer
	nextTEI int
	isUp    bool
	closed  bool
	pending [][]byte // the user side's messages waiting for its TEI (an answer to a broadcast SETUP)
}

func newDatalink(link Link, network, ptmp bool, deliver func(int, []byte), up func(bool)) *datalink {
	return &datalink{link: link, network: network, ptmp: ptmp, deliver: deliver, up: up, links: map[int]*lapd{}, tei: -1, nextTEI: 64}
}

func (d *datalink) write(f []byte) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	_ = d.link.WriteFrame(f)
}

// newLink makes the data link for a TEI. Called with d.mu held.
func (d *datalink) newLink(tei int) *lapd {
	l := newLAPD(d.write, d.network, tei, func(m []byte) { d.deliver(tei, m) }, func(up bool) {
		if !d.ptmp {
			d.setUp(up) // point to point: the data link is the D-channel
		}
	})
	l.onDemand = d.ptmp
	d.links[tei] = l
	return l
}

func (d *datalink) setUp(up bool) {
	d.mu.Lock()
	was := d.isUp
	d.isUp = up
	d.mu.Unlock()
	if was != up {
		d.up(up)
	}
}

func (d *datalink) start() {
	d.mu.Lock()
	switch {
	case !d.ptmp:
		l := d.newLink(0)
		d.mu.Unlock()
		l.start()
		return
	case d.network:
		d.mu.Unlock()
		d.setUp(true) // terminals ask for their TEIs when they are plugged in
		return
	}
	d.mu.Unlock()
	d.requestTEI()
}

// run reads frames until the link fails.
func (d *datalink) run() error {
	for {
		f, err := d.link.ReadFrame()
		if err != nil {
			d.mu.Lock()
			d.closed = true
			links := make([]*lapd, 0, len(d.links))
			for _, l := range d.links {
				links = append(links, l)
			}
			if d.t202 != nil {
				d.t202.Stop()
			}
			d.mu.Unlock()
			for _, l := range links {
				l.fail()
			}
			d.setUp(false)
			return err
		}
		d.receive(f)
	}
}

func (d *datalink) close() {
	d.mu.Lock()
	d.closed = true
	if d.t202 != nil {
		d.t202.Stop()
	}
	links := make([]*lapd, 0, len(d.links))
	for _, l := range d.links {
		links = append(links, l)
	}
	d.mu.Unlock()
	for _, l := range links {
		l.close()
	}
}

// Send sends a layer 3 message to a terminal (the network side of a multipoint line) or on the one
// data link (anywhere else).
func (d *datalink) Send(tei int, msg []byte) error {
	d.mu.Lock()
	if !d.ptmp {
		tei = 0
	} else if !d.network {
		tei = d.tei
	}
	l := d.links[tei]
	if l == nil && d.ptmp && d.network && tei >= 0 && tei < teiBroadcast {
		l = d.newLink(tei)
	}
	if l == nil && d.ptmp && !d.network && !d.closed {
		d.pending = append(d.pending, msg) // sent once the network gives this terminal its TEI
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	if l == nil {
		return ErrDown
	}
	return l.Send(msg)
}

// Broadcast sends a layer 3 message to every terminal in one UI frame (the network side of a
// multipoint line: a SETUP).
func (d *datalink) Broadcast(msg []byte) error {
	d.write(append([]byte{sapiCall<<2 | 2, teiBroadcast<<1 | 1, ctlUI}, msg...))
	return nil
}

func (d *datalink) receive(f []byte) {
	if len(f) < 3 || f[0]&1 != 0 || f[1]&1 != 1 {
		return
	}
	sapi, tei := int(f[0]>>2), int(f[1]>>1)
	switch {
	case sapi == sapiMgmt && tei == teiBroadcast && f[2]&^pfU == ctlUI:
		if d.ptmp {
			d.management(f[3:])
		}
		return
	case sapi != sapiCall:
		return
	case tei == teiBroadcast:
		if f[2]&^pfU == ctlUI && d.ptmp && !d.network {
			d.deliver(teiBroadcast, append([]byte(nil), f[3:]...))
		}
		return
	}
	d.mu.Lock()
	var l *lapd
	switch {
	case !d.ptmp:
		l = d.links[0]
		if tei != 0 {
			l = nil
		}
	case d.network:
		l = d.links[tei]
		if l == nil {
			l = d.newLink(tei) // a terminal with a fixed TEI, or one assigned before a restart
		}
	default:
		if tei == d.tei {
			l = d.links[tei]
		}
	}
	d.mu.Unlock()
	if l != nil {
		l.receive(f)
	}
}

// mgmt sends a TEI management message.
func (d *datalink) mgmt(ri uint16, typ byte, ai int) {
	cr := byte(0)
	if d.network {
		cr = 2 // the network's commands carry C/R 1
	}
	d.write([]byte{sapiMgmt<<2 | cr, teiBroadcast<<1 | 1, ctlUI, 0x0F, byte(ri >> 8), byte(ri), typ, byte(ai<<1 | 1)})
}

func randomRi() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}

// requestTEI asks the network for a TEI (the user side of a multipoint line).
func (d *datalink) requestTEI() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.ri = randomRi()
	d.tries++
	ri := d.ri
	if d.t202 != nil {
		d.t202.Stop()
	}
	d.t202 = time.AfterFunc(T202, func() {
		d.mu.Lock()
		waiting, tries := d.tei < 0 && !d.closed, d.tries
		d.mu.Unlock()
		if !waiting {
			return
		}
		if tries >= N202 {
			// No answer: the line may be unplugged; keep asking, less often.
			d.mu.Lock()
			d.tries = 0
			d.t202 = time.AfterFunc(5*T202, d.requestTEI)
			d.mu.Unlock()
			return
		}
		d.requestTEI()
	})
	d.mu.Unlock()
	d.mgmt(ri, teiRequest, teiBroadcast)
}

func (d *datalink) management(b []byte) {
	if len(b) < 5 || b[0] != 0x0F {
		return
	}
	ri, typ, ai := uint16(b[1])<<8|uint16(b[2]), b[3], int(b[4]>>1)
	if d.network {
		switch typ {
		case teiRequest:
			d.mu.Lock()
			tei := -1
			for n := 0; n < 63; n++ {
				t := 64 + (d.nextTEI-64+n)%63
				if d.links[t] == nil {
					tei = t
					break
				}
			}
			if tei >= 0 {
				d.nextTEI = tei + 1
				d.newLink(tei)
			}
			d.mu.Unlock()
			if tei < 0 {
				d.mgmt(ri, teiDenied, teiBroadcast)
				return
			}
			d.mgmt(ri, teiAssigned, tei)
		case teiVerify:
			// A terminal unsure of its TEI: check it, which it answers if it still has it.
			d.mgmt(0, teiCheckRequest, ai)
		}
		return
	}
	switch typ {
	case teiAssigned:
		d.mu.Lock()
		if d.tei >= 0 || ri != d.ri {
			d.mu.Unlock()
			return
		}
		d.tei = ai
		d.tries = 0
		if d.t202 != nil {
			d.t202.Stop()
		}
		l := d.newLink(ai)
		pending := d.pending
		d.pending = nil
		d.mu.Unlock()
		for _, m := range pending {
			_ = l.Send(m)
		}
		d.setUp(true)
	case teiDenied:
		// Another try after T202 (the timer is already running).
	case teiCheckRequest:
		d.mu.Lock()
		mine := d.tei >= 0 && (ai == teiBroadcast || ai == d.tei)
		tei := d.tei
		d.mu.Unlock()
		if mine {
			d.mgmt(randomRi(), teiCheckResponse, tei)
		}
	case teiRemove:
		d.mu.Lock()
		if d.tei < 0 || (ai != teiBroadcast && ai != d.tei) {
			d.mu.Unlock()
			return
		}
		l := d.links[d.tei]
		delete(d.links, d.tei)
		d.tei = -1
		d.tries = 0
		d.mu.Unlock()
		if l != nil {
			l.fail()
		}
		d.setUp(false)
		d.requestTEI()
	}
}

func (d *datalink) IsUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isUp
}
