package media

import (
	"encoding/binary"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Media inspection (the BCF's, at the ESInet edge): every RTP packet a call receives is checked
// before it is used or passed on. A packet that breaks a rule is dropped and counted; the call is
// never ended for it (a 911 call is never cut off by the border). The first time a rule is broken,
// and again each time its count passes a further power of ten, Report is told.

// InspectRules says what media may come in on one call.
type InspectRules struct {
	// Source, when valid, is the only address media may come from (the one its SDP gave).
	Source netip.Addr
	// PayloadTypes are the RTP payload types the call's SDP offered; any other is dropped.
	PayloadTypes []int
	// PerSecond is the most packets taken in any second (0: no limit); the rest are dropped.
	PerSecond int
	// MaxBytes is the largest packet taken (0: no limit).
	MaxBytes int
	// Report is told when a rule is broken: the finding's kind and how many so far.
	Report func(f Finding)
}

// Finding is one kind of broken rule on a call, with its count so far.
type Finding struct {
	Kind  string // source, payload, flood, size, malformed
	Count int
	What  string // in plain words
}

// Inspector applies InspectRules to an endpoint's incoming packets.
type Inspector struct {
	r      InspectRules
	mu     sync.Mutex
	second time.Time
	inSec  int
	counts map[string]int
}

// NewInspector makes an inspector for rules r.
func NewInspector(r InspectRules) *Inspector {
	return &Inspector{r: r, counts: map[string]int{}}
}

// Counts is how many packets each rule dropped.
func (in *Inspector) Counts() map[string]int {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := make(map[string]int, len(in.counts))
	for k, v := range in.counts {
		out[k] = v
	}
	return out
}

// Check reports whether an RTP packet (version 2 already, SRTP removed) from an address may be
// used.
func (in *Inspector) Check(pkt []byte, from netip.Addr, now time.Time) bool {
	kind, what := "", ""
	switch {
	case in.r.Source.IsValid() && from.Unmap() != in.r.Source.Unmap():
		kind, what = "source", "media from "+from.Unmap().String()+", not the address the call's SDP gave ("+in.r.Source.Unmap().String()+")"
	case !wellFormed(pkt):
		kind, what = "malformed", "RTP packets whose header lengths do not fit the packet"
	case in.r.MaxBytes > 0 && len(pkt) > in.r.MaxBytes:
		kind, what = "size", "RTP packets over "+strconv.Itoa(in.r.MaxBytes)+" bytes"
	case len(in.r.PayloadTypes) > 0 && !slices.Contains(in.r.PayloadTypes, int(pkt[1]&0x7f)):
		kind, what = "payload", "RTP payload type "+strconv.Itoa(int(pkt[1]&0x7f))+", which the call's SDP did not offer"
	}
	in.mu.Lock()
	if kind == "" && in.r.PerSecond > 0 {
		if now.Sub(in.second) >= time.Second {
			in.second, in.inSec = now, 0
		}
		in.inSec++
		if in.inSec > in.r.PerSecond {
			kind, what = "flood", "more than "+strconv.Itoa(in.r.PerSecond)+" RTP packets in a second"
		}
	}
	if kind == "" {
		in.mu.Unlock()
		return true
	}
	in.counts[kind]++
	n := in.counts[kind]
	in.mu.Unlock()
	if report := in.r.Report; report != nil && powerOfTen(n) {
		report(Finding{Kind: kind, Count: n, What: what})
	}
	return false
}

func powerOfTen(n int) bool {
	for n >= 10 && n%10 == 0 {
		n /= 10
	}
	return n == 1
}

// wellFormed checks an RTP header's own lengths (RFC 3550 5.1): CSRCs, the header extension and the
// padding must all fit in the packet.
func wellFormed(pkt []byte) bool {
	if len(pkt) < 12 {
		return false
	}
	n := 12 + 4*int(pkt[0]&0x0f)
	if pkt[0]&0x10 != 0 {
		if len(pkt) < n+4 {
			return false
		}
		n += 4 + 4*int(binary.BigEndian.Uint16(pkt[n+2:]))
	}
	if n > len(pkt) {
		return false
	}
	if pkt[0]&0x20 != 0 {
		pad := int(pkt[len(pkt)-1])
		if pad == 0 || n+pad > len(pkt) {
			return false
		}
	}
	return true
}

// SetInspector has every RTP packet this endpoint receives checked first (nil: none).
func (e *Endpoint) SetInspector(in *Inspector) {
	e.mu.Lock()
	e.inspect = in
	e.mu.Unlock()
}
