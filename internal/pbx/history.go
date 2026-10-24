package pbx

import (
	"fmt"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

// redirecting tells a carrier whose call a forwarded call is: Diversion (RFC 5806) and History-Info
// (RFC 7044), as the trunk's Redirecting setting says. A call that arrived forwarded keeps its
// earlier history, and the counter counts every forward.
func (c *Call) redirecting(inv *sip.Message, t target, host, ruri string) {
	mode := t.trunk.Redirecting
	c.mu.Lock()
	earlier := c.history
	c.mu.Unlock()
	if mode == "" || mode == "both" || mode == "diversion" {
		counter := 1
		for _, h := range earlier {
			if h.Cause != 0 {
				counter++
			}
		}
		inv.Set("Diversion", fmt.Sprintf("<sip:%s@%s>;reason=%s;counter=%d", t.divertFrom, host, t.divertReason, counter))
	}
	if mode == "" || mode == "both" || mode == "history-info" {
		to, err := sip.ParseURI(ruri)
		if err != nil {
			return
		}
		from := &sip.URI{Scheme: "sip", User: t.divertFrom, Host: host}
		for _, e := range sip.HistoryInfo(earlier, from, to, t.divertReason) {
			inv.Add("History-Info", e)
		}
	}
}

// readForward notes how a call from a carrier was forwarded before it reached BitVoice: the number
// it was first for and the one that sent it here go on the call (and the call record), and the
// earlier history is kept for any forward on.
func (c *Call) readForward(m *sip.Message) {
	f, ok := sip.ForwardOf(m)
	if !ok {
		return
	}
	c.history = sip.ParseHistoryInfo(m)
	c.fwdFrom, c.fwdOriginal, c.fwdReason = f.From, f.Original, f.Reason
	if f.Hidden {
		c.fwdFrom, c.fwdOriginal = "", ""
		c.note(fmt.Sprintf("The call was forwarded to here (%s); the carrier asked to keep the numbers private.", reasonWords(f.Reason)))
		return
	}
	if f.Original != "" && f.Original != f.From {
		c.note(fmt.Sprintf("The call was for %s, forwarded to here by %s (%s).", f.Original, f.From, reasonWords(f.Reason)))
	} else {
		c.note(fmt.Sprintf("The call was forwarded to here by %s (%s).", f.From, reasonWords(f.Reason)))
	}
}

func reasonWords(r string) string {
	switch r {
	case "unconditional":
		return "it forwards every call"
	case "user-busy":
		return "it was busy"
	case "no-answer":
		return "nobody answered"
	case "deflection":
		return "it sent the call on"
	case "do-not-disturb":
		return "do not disturb"
	case "unavailable":
		return "it was unavailable"
	}
	return "for no stated reason"
}
