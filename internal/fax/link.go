package fax

import "sync"

// Link carries IFP packets over UDPTL: each packet goes out with copies of the two before it, and
// packets come in once each, in order, those lost recovered from the copies in later ones.
type Link struct {
	send func([]byte)

	mu      sync.Mutex
	seq     uint16
	history [][]byte // the last packets sent, newest first
	started bool
	next    uint16 // the next sequence number expected in
}

// NewLink sends datagrams with send.
func NewLink(send func([]byte)) *Link { return &Link{send: send} }

// Send sends one IFP packet.
func (l *Link) Send(p IFP) {
	b := p.Marshal()
	l.mu.Lock()
	d := MarshalUDPTL(l.seq, b, l.history)
	l.seq++
	l.history = append([][]byte{b}, l.history...)
	if len(l.history) > 2 {
		l.history = l.history[:2]
	}
	l.mu.Unlock()
	l.send(d)
}

// Receive takes one datagram and delivers the IFP packets it brings that were not delivered yet.
func (l *Link) Receive(d []byte, deliver func(IFP)) {
	u, err := ParseUDPTL(d)
	if err != nil {
		return
	}
	l.mu.Lock()
	if !l.started {
		l.started, l.next = true, u.Seq
	}
	ahead := u.Seq - l.next // how many were lost before this one
	if ahead >= 0x8000 {
		l.mu.Unlock()
		return // a repeat or a late copy
	}
	var out []IFP
	for k := int(ahead) - 1; k >= 0; k-- { // oldest first
		if k < len(u.Secondary) {
			if p, err := ParseIFP(u.Secondary[k]); err == nil {
				out = append(out, p)
			}
		}
	}
	if p, err := ParseIFP(u.Primary); err == nil {
		out = append(out, p)
	}
	l.next = u.Seq + 1
	l.mu.Unlock()
	for _, p := range out {
		deliver(p)
	}
}
