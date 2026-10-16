package pbx

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"strings"
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

// bcfIdentity checks a signed call's STIR/SHAKEN Identity header at the ESInet edge (System.Core.BCF.
// VerifyIdentity): it reports whether the signature failed (the call is then marked suspicious and the
// decision logged), and keeps the verdict with the call.
func (e *Engine) bcfIdentity(cfg *config.Config, c *Call, identity, to string, source netip.Addr) bool {
	b := cfg.System.Core.BCF
	k := e.checker.Load()
	if !b.Enabled || !b.VerifyIdentity || identity == "" || k == nil {
		return false
	}
	v := k.Check(identity, c.from, to)
	c.screen = &v
	if v.Verified != "failed" {
		return false
	}
	ev := BCFEvent{At: time.Now(), Source: source.Unmap().String(), From: c.from, Action: "marked", Why: "the caller ID's signature failed: " + v.Why}
	e.bcf.mu.Lock()
	e.bcf.log = append(e.bcf.log, ev)
	if len(e.bcf.log) > 200 {
		e.bcf.log = e.bcf.log[len(e.bcf.log)-200:]
	}
	e.bcf.mu.Unlock()
	e.publish("bcf.event", ev)
	return true
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

// Source IDs and bad actors (NENA-STA-010.3 section 5.2.1.6): each source the BCF admits gets an
// unguessable ID, sent on with its calls (Call-Info purpose=emergency-source); a downstream element
// that finds a source attacking reports the ID, and the BCF then refuses that source's calls, or
// raises their suspicion score, for a while.

type badActorState struct {
	mu      sync.Mutex
	ids     map[string]string    // source address -> ID
	sources map[string]string    // ID -> source address
	seen    map[string]time.Time // ID -> last call
	bad     map[string]BadActor  // source address -> report
}

// BadActor is a source reported as a bad actor.
type BadActor struct {
	SourceID string    `json:"sourceId"`
	Source   string    `json:"source"`
	At       time.Time `json:"at"`
	Until    time.Time `json:"until"`
	By       string    `json:"by"`
}

// sourceIDFor is a source address's ID, made on first use: <random>@<this BCF's name>.
func (e *Engine) sourceIDFor(src string) string {
	b := &e.badActors
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ids == nil {
		b.ids, b.sources, b.seen, b.bad = map[string]string{}, map[string]string{}, map[string]time.Time{}, map[string]BadActor{}
	}
	id, ok := b.ids[src]
	if !ok {
		host := e.Config().System.Core.Source
		if host == "" {
			host = e.Config().System.PublicAddress
		}
		if host == "" {
			host = "bcf.invalid"
		}
		id = randomToken(12) + "@" + host
		b.ids[src], b.sources[id] = id, src
	}
	b.seen[id] = time.Now()
	if len(b.seen) > 20000 { // forget sources quiet for a week
		for k, t := range b.seen {
			if time.Since(t) > 7*24*time.Hour {
				delete(b.sources, k)
				delete(b.seen, k)
				for s, v := range b.ids {
					if v == k {
						delete(b.ids, s)
					}
				}
			}
		}
	}
	return id
}

// ErrUnknownSource and ErrAlreadyReported are the bad actor service's 433 and 432.
var (
	ErrUnknownSource   = errors.New("no such source ID")
	ErrAlreadyReported = errors.New("that source is already reported")
)

// ReportBadActor marks a source, by the ID its calls carried, as a bad actor.
func (e *Engine) ReportBadActor(sourceID, by string) (BadActor, error) {
	id := strings.TrimSpace(sourceID)
	id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	id = strings.TrimPrefix(strings.TrimPrefix(id, "sips:"), "sip:")
	cfg := e.Config()
	hours := cfg.System.Core.BCF.BadActorHours
	if hours == 0 {
		hours = 24
	}
	b := &e.badActors
	b.mu.Lock()
	defer b.mu.Unlock()
	src, ok := b.sources[id]
	if !ok {
		return BadActor{}, ErrUnknownSource
	}
	if r, ok := b.bad[src]; ok && time.Now().Before(r.Until) {
		return r, ErrAlreadyReported
	}
	r := BadActor{SourceID: id, Source: src, At: time.Now(), Until: time.Now().Add(time.Duration(hours) * time.Hour), By: by}
	b.bad[src] = r
	ev := BCFEvent{At: r.At, Source: src, Action: "reported", Why: "reported as a bad actor by " + by}
	e.bcf.mu.Lock()
	e.bcf.log = append(e.bcf.log, ev)
	e.bcf.mu.Unlock()
	go e.publish("bcf.event", ev)
	return r, nil
}

// BadActors lists the sources reported, still in force.
func (e *Engine) BadActors() []BadActor {
	b := &e.badActors
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []BadActor{}
	for _, r := range b.bad {
		if time.Now().Before(r.Until) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b BadActor) int { return b.At.Compare(a.At) })
	return out
}

// ClearBadActor lifts a report early.
func (e *Engine) ClearBadActor(sourceID string) bool {
	b := &e.badActors
	b.mu.Lock()
	defer b.mu.Unlock()
	for src, r := range b.bad {
		if r.SourceID == sourceID {
			delete(b.bad, src)
			return true
		}
	}
	return false
}

// badActorCheck: a reported source's call is refused (BadActorBlock) or has its suspicion raised.
func (e *Engine) badActorCheck(cfg *config.Config, c *Call, src string) (refuse bool) {
	bc := cfg.System.Core.BCF
	if !bc.Enabled || !bc.BadActors {
		return false
	}
	c.sourceID = e.sourceIDFor(src)
	e.badActors.mu.Lock()
	r, bad := e.badActors.bad[src]
	e.badActors.mu.Unlock()
	if !bad || time.Now().After(r.Until) {
		return false
	}
	action, why := "marked", "a reported bad actor"
	if bc.BadActorBlock {
		action = "refused"
	} else {
		score := bc.BadActorScore
		if score == 0 {
			score = 50
		}
		c.suspicion += score
		c.suspicious = true
	}
	ev := BCFEvent{At: time.Now(), Source: src, From: c.from, Action: action, Why: why}
	e.bcf.mu.Lock()
	e.bcf.log = append(e.bcf.log, ev)
	if len(e.bcf.log) > 200 {
		e.bcf.log = e.bcf.log[len(e.bcf.log)-200:]
	}
	e.bcf.mu.Unlock()
	e.publish("bcf.event", ev)
	return bc.BadActorBlock
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
