package pbx

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// FailBan blocks addresses that keep failing authentication, the first defence against the SIP scanners
// that try every PBX on the internet, and rate-limits every address so a flood cannot starve real calls.
type FailBan struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	duration  time.Duration
	trusted   []netip.Prefix
	failures  map[netip.Addr][]time.Time
	bans      map[netip.Addr]ban
	buckets   map[netip.Addr]*bucket
	now       func() time.Time
	lastFail  time.Time // when an untrusted address last failed three times in a window
}

type ban struct {
	until  time.Time
	reason string
}

type bucket struct {
	tokens float64
	last   time.Time
}

// BanInfo describes a blocked address.
type BanInfo struct {
	IP     string    `json:"ip"`
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

func newFailBan() *FailBan {
	return &FailBan{failures: map[netip.Addr][]time.Time{}, bans: map[netip.Addr]ban{},
		buckets: map[netip.Addr]*bucket{}, now: time.Now}
}

func (f *FailBan) apply(s config.Security) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threshold = s.FailBanThreshold
	f.window = time.Duration(s.FailBanWindowSecs) * time.Second
	f.duration = time.Duration(s.FailBanMinutes) * time.Minute
	f.trusted = nil
	for _, n := range s.TrustedNetworks {
		if p, err := netip.ParsePrefix(n); err == nil {
			f.trusted = append(f.trusted, p)
		}
	}
}

func (f *FailBan) isTrusted(ip netip.Addr) bool {
	for _, p := range f.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// fail records an authentication failure and reports whether the address is now banned.
func (f *FailBan) fail(ip netip.Addr, reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isTrusted(ip) || f.threshold <= 0 {
		return false
	}
	now := f.now()
	list := f.failures[ip]
	cut := 0
	for cut < len(list) && now.Sub(list[cut]) > f.window {
		cut++
	}
	list = append(list[cut:], now)
	f.failures[ip] = list
	if len(list) >= 3 { // a mistyped password is not an attack; three in a row from one address are
		f.lastFail = now
	}
	if len(list) >= f.threshold {
		f.bans[ip] = ban{until: now.Add(f.duration), reason: reason}
		delete(f.failures, ip)
		return true
	}
	return false
}

// succeed clears failures after a good authentication.
func (f *FailBan) succeed(ip netip.Addr) {
	f.mu.Lock()
	delete(f.failures, ip)
	f.mu.Unlock()
}

func (f *FailBan) banned(ip netip.Addr) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bans[ip]
	if !ok {
		return false
	}
	if f.now().After(b.until) {
		delete(f.bans, ip)
		return false
	}
	return true
}

// allow is a token bucket: 40 requests per second sustained, bursts of 200, per address.
func (f *FailBan) allow(ip netip.Addr) bool {
	const rate, burst = 40.0, 200.0
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isTrusted(ip) {
		return true
	}
	now := f.now()
	b := f.buckets[ip]
	if b == nil {
		if len(f.buckets) > 100000 {
			f.buckets = map[netip.Addr]*bucket{} // bounded memory under a spoofed-source flood
		}
		b = &bucket{tokens: burst, last: now}
		f.buckets[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (f *FailBan) list() []BanInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	out := []BanInfo{}
	for ip, b := range f.bans {
		if now.Before(b.until) {
			out = append(out, BanInfo{IP: ip.String(), Until: b.until, Reason: b.reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

func (f *FailBan) unban(ip netip.Addr) {
	f.mu.Lock()
	delete(f.bans, ip)
	delete(f.failures, ip)
	f.mu.Unlock()
}

// attackedWithin reports repeated failed sign-ins from an untrusted address within d, or a ban in force.
func (f *FailBan) attackedWithin(d time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if !f.lastFail.IsZero() && now.Sub(f.lastFail) <= d {
		return true
	}
	for _, b := range f.bans {
		if b.until.After(now) {
			return true
		}
	}
	return false
}
