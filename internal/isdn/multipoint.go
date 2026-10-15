package isdn

// A call the network side of a multipoint BRI offered to every terminal (a broadcast SETUP): each
// terminal that wants it replies on its own data link, the first to answer (CONNECT) gets it, and
// the others are released with cause 26, non-selected user clearing (Q.931 5.2.5).

// CauseNonSelected releases the terminals that did not get the call.
const CauseNonSelected = 26

// multipoint handles a message for a call offered to every terminal. It reports whether it took it.
func (c *Call) multipoint(m *Message) bool {
	p := c.p
	p.mu.Lock()
	offered := c.ours && c.responders != nil
	unbound := c.tei == teiBroadcast
	from := m.tei
	p.mu.Unlock()
	if !offered {
		return false
	}
	release := func(t int, typ byte, cause int) {
		r := c.msg(typ)
		r.tei = t
		if typ != MsgReleaseComplete || cause != 0 {
			r.add(ieCause, causeIE(cause, p.cfg.Network))
		}
		_ = p.send(r)
	}
	if !unbound {
		if from == c.tei {
			return false // the terminal that has the call: as on any line
		}
		// Another terminal, late: it did not get the call.
		switch m.Type {
		case MsgRelease:
			release(from, MsgReleaseComplete, 0)
		case MsgReleaseComplete:
		default:
			release(from, MsgRelease, CauseNonSelected)
		}
		return true
	}
	cause := CauseNormal
	if d, ok := m.Get(ieCause); ok {
		cause = parseCause(d)
	}
	switch m.Type {
	case MsgConnect:
		p.mu.Lock()
		c.tei = from
		var others []int
		for t := range c.responders {
			if t != from {
				others = append(others, t)
			}
		}
		c.responders = map[int]bool{from: true}
		p.mu.Unlock()
		for _, t := range others {
			release(t, MsgRelease, CauseNonSelected)
		}
		return false // CONNECT itself goes on as on any line, now from the chosen terminal
	case MsgDisconnect, MsgRelease, MsgReleaseComplete:
		// One terminal declines; the call goes on while others may still take it.
		if m.Type == MsgDisconnect {
			release(from, MsgRelease, cause)
		} else if m.Type == MsgRelease {
			release(from, MsgReleaseComplete, 0)
		}
		p.mu.Lock()
		wasResponder := c.responders[from]
		delete(c.responders, from)
		left := len(c.responders)
		c.declined = cause
		p.mu.Unlock()
		if left == 0 && wasResponder {
			c.free(cause)
		}
		// A terminal declining before replying at all (busy, say): others may still answer; T303
		// ends the call with its cause if none does.
		return true
	default:
		p.mu.Lock()
		c.responders[from] = true
		p.mu.Unlock()
		return false // CALL PROCEEDING, ALERTING, PROGRESS: as on any line
	}
}
