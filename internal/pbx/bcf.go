package pbx

import (
	"net/netip"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The BCF (border control function, NENA i3): calls from ESInet trunks pass it before anything
// else. It refuses calls from denied addresses, and calls from an address that starts too many in
// a minute or holds too many at once (a telephony denial-of-service attack), or lets those through
// marked suspicious for the policy rules to deal with.

type bcfState struct {
	mu     sync.Mutex
	starts map[string][]time.Time // recent call starts by source address
	log    []BCFEvent             // the latest decisions, newest last
}

// BCFEvent is one call the BCF refused or marked.
type BCFEvent struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	From   string    `json:"from"`
	Action string    `json:"action"` // refused or marked
	Why    string    `json:"why"`
}

// BCFLog is the latest BCF decisions, newest first.
func (e *Engine) BCFLog() []BCFEvent {
	e.bcf.mu.Lock()
	defer e.bcf.mu.Unlock()
	out := make([]BCFEvent, 0, len(e.bcf.log))
	for i := len(e.bcf.log) - 1; i >= 0; i-- {
		out = append(out, e.bcf.log[i])
	}
	return out
}

// bcfCheck decides on a call from an ESInet trunk: refuse it (a reason), or let it through, marked
// suspicious or not.
func (e *Engine) bcfCheck(cfg *config.Config, c *Call, source netip.Addr) (refuse string, suspicious bool) {
	b := cfg.System.Core.BCF
	if !b.Enabled {
		return "", false
	}
	src := source.Unmap().String()
	why := ""
	for _, d := range b.Deny {
		if p, err := netip.ParsePrefix(d); err == nil && p.Contains(source.Unmap()) {
			why = "the address is denied"
		} else if a, err := netip.ParseAddr(d); err == nil && a.Unmap() == source.Unmap() {
			why = "the address is denied"
		}
	}
	now := time.Now()
	e.bcf.mu.Lock()
	if e.bcf.starts == nil {
		e.bcf.starts = map[string][]time.Time{}
	}
	recent := e.bcf.starts[src][:0]
	for _, t := range e.bcf.starts[src] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	e.bcf.starts[src] = recent
	if len(e.bcf.starts) > 10000 { // forget quiet addresses
		for k, v := range e.bcf.starts {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
				delete(e.bcf.starts, k)
			}
		}
	}
	e.bcf.mu.Unlock()
	if why == "" && b.PerMinute > 0 && len(recent) > b.PerMinute {
		why = "more than " + itoaInt(b.PerMinute) + " calls in a minute from this address"
	}
	if why == "" && b.Concurrent > 0 {
		n := 0
		for _, other := range e.allCalls() {
			if other.srcIP == src {
				n++
			}
		}
		if n >= b.Concurrent {
			why = itoaInt(n) + " calls already up from this address"
		}
	}
	if why == "" {
		return "", false
	}
	action := "refused"
	if b.Mark && why != "the address is denied" {
		action, suspicious = "marked", true
	} else {
		refuse = why
	}
	ev := BCFEvent{At: now, Source: src, From: c.from, Action: action, Why: why}
	e.bcf.mu.Lock()
	e.bcf.log = append(e.bcf.log, ev)
	if len(e.bcf.log) > 200 {
		e.bcf.log = e.bcf.log[len(e.bcf.log)-200:]
	}
	e.bcf.mu.Unlock()
	e.publish("bcf.event", ev)
	return refuse, suspicious
}
