package pbx

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Switch shares one SIP port among the engines of hosted tenants. Each request goes to the engine it
// belongs to: an existing dialog to the engine holding it, a phone by its tenant's domain or user name
// prefix, a call from a provider to the tenant whose trunk and number it matches, and anything else to
// the main engine.
type Switch struct {
	Default *Engine

	mu      sync.RWMutex
	tenants map[string]*Engine // by tenant ID
	domains map[string]*Engine // by SIP domain
}

// NewSwitch makes a switch whose main engine is def.
func NewSwitch(def *Engine) *Switch {
	s := &Switch{Default: def, tenants: map[string]*Engine{}, domains: map[string]*Engine{}}
	def.Dispatch = s.Handle
	return s
}

// Add puts a tenant's engine on the switch, reached by domain (when set) and by user names id.<ext>.
func (s *Switch) Add(id, domain string, e *Engine) {
	e.UserPrefix = id + "."
	s.mu.Lock()
	for d, x := range s.domains {
		if x == s.tenants[id] {
			delete(s.domains, d)
		}
	}
	s.tenants[id] = e
	if domain != "" {
		s.domains[strings.ToLower(domain)] = e
	}
	s.mu.Unlock()
}

// Remove takes a tenant's engine off the switch.
func (s *Switch) Remove(id string) {
	s.mu.Lock()
	e := s.tenants[id]
	delete(s.tenants, id)
	for d, x := range s.domains {
		if x == e {
			delete(s.domains, d)
		}
	}
	s.mu.Unlock()
}

// Engine is a tenant's engine, or nil.
func (s *Switch) Engine(id string) *Engine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tenants[id]
}

func (s *Switch) all() []*Engine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Engine, 0, len(s.tenants)+1)
	out = append(out, s.Default)
	for _, e := range s.tenants {
		out = append(out, e)
	}
	return out
}

// Handle is the SIP stack's handler.
func (s *Switch) Handle(tx *sip.ServerTx, in *sip.Incoming) {
	s.pick(in).handle(tx, in)
}

func (s *Switch) pick(in *sip.Incoming) *Engine {
	m := in.Msg
	s.mu.RLock()
	n := len(s.tenants)
	s.mu.RUnlock()
	if n == 0 {
		return s.Default
	}
	callID := m.CallID()
	if to, _ := sip.ParseAddress(m.Get("To")); to != nil && to.Tag() != "" {
		for _, e := range s.all() {
			if e.ownsDialog(callID, to.Tag()) {
				return e
			}
		}
		return s.Default
	}
	if m.Method == sip.CANCEL {
		for _, e := range s.all() {
			if e.hasPending(m) {
				return e
			}
		}
		return s.Default
	}
	// A tenant's own domain, in the request address, To or From.
	s.mu.RLock()
	for _, h := range []string{hostOf(m.RequestURI), headerHost(m.Get("To")), headerHost(m.Get("From"))} {
		if e := s.domains[h]; e != nil && h != "" {
			s.mu.RUnlock()
			return e
		}
	}
	// A user name with a tenant's prefix: acme.100.
	for _, u := range []string{headerUser(m.Get("From")), headerUser(m.Get("To")), authUser(m.Get("Authorization")), authUser(m.Get("Proxy-Authorization"))} {
		if i := strings.IndexByte(u, '.'); i > 0 {
			if e := s.tenants[strings.ToLower(u[:i])]; e != nil {
				s.mu.RUnlock()
				return e
			}
		}
	}
	s.mu.RUnlock()
	// A call from a provider: the engine whose trunk it comes from, the one with a route for the number
	// first (several tenants often share one provider).
	if m.Method == sip.INVITE {
		var best *Engine
		bestScore := 0
		for _, e := range s.all() {
			if sc := e.claimScore(in); sc > bestScore {
				best, bestScore = e, sc
			}
		}
		if best != nil {
			return best
		}
	}
	return s.Default
}

// ownsDialog says whether this engine holds a dialog (a call leg or a subscription).
func (e *Engine) ownsDialog(callID, tag string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.legs[legKey(callID, tag)]; ok {
		return true
	}
	for _, sub := range e.subs {
		if sub.callID == callID {
			return true
		}
	}
	return false
}

// hasPending says whether a CANCEL is for an INVITE this engine is handling.
func (e *Engine) hasPending(m *sip.Message) bool {
	inv := e.stack.FindInvite(m)
	if inv == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.pending[inv.Branch()]
	return ok
}

// claimScore rates how surely a new INVITE from a provider belongs here: 2 when a trunk matches its
// source and an incoming route its number, 1 for the trunk alone, 0 when it is not from a trunk here.
func (e *Engine) claimScore(in *sip.Incoming) int {
	cfg := e.Config()
	t := e.trunkFor(cfg, in.From.Addr)
	if t == nil {
		return 0
	}
	did := cleanNumber(userOf(in.Msg.Get("To")))
	if r := inboundRoute(cfg, t.ID, did); r != nil && r.DID != "" {
		return 2
	}
	return 1
}

// StartShared runs this engine on another engine's SIP port and media ports (a hosted tenant).
func (e *Engine) StartShared(ctx context.Context, base *Engine) {
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.stack, e.pool, e.shared = base.stack, base.pool, true
	e.started = time.Now()
	go e.reg.run(e.ctx)
	go e.runTrunks(e.ctx)
	go e.watchdog(e.ctx)
	e.applyRadio(e.Config())
}

// localUser drops the tenant prefix from a SIP user name: acme.100 is 100.
func (e *Engine) localUser(u string) string {
	if p := e.UserPrefix; p != "" && len(u) > len(p) && strings.EqualFold(u[:len(p)], p) {
		return u[len(p):]
	}
	return u
}

// overLimit says why a new call is refused (a suspended tenant, or one at its call allowance), or "".
// Emergency calls, and calls coming in within an hour of one (the call back), are never refused.
func (e *Engine) overLimit(direction string) string {
	if direction == "emergency" {
		return ""
	}
	if direction == "inbound" && time.Since(time.Unix(e.lastEmergency.Load(), 0)) < time.Hour {
		return ""
	}
	if e.Suspended.Load() {
		return "Service Suspended"
	}
	if max := int(e.MaxCalls.Load()); max > 0 && len(e.allCalls()) >= max {
		return "Call Limit Reached"
	}
	return ""
}

func hostOf(uri string) string {
	u, err := sip.ParseURI(uri)
	if err != nil || u == nil {
		return ""
	}
	h := u.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return strings.ToLower(h)
}

func headerHost(v string) string {
	a, err := sip.ParseAddress(v)
	if err != nil || a == nil || a.URI == nil {
		return ""
	}
	return hostOf(a.URI.String())
}

func headerUser(v string) string {
	a, err := sip.ParseAddress(v)
	if err != nil || a == nil || a.URI == nil {
		return ""
	}
	return a.URI.User
}

// authUser is the username parameter of a digest Authorization header.
func authUser(v string) string {
	i := strings.Index(strings.ToLower(v), "username=")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(v[i+len("username="):], " ")
	if strings.HasPrefix(rest, `"`) {
		if j := strings.IndexByte(rest[1:], '"'); j >= 0 {
			return rest[1 : j+1]
		}
		return ""
	}
	if j := strings.IndexAny(rest, ", "); j >= 0 {
		return rest[:j]
	}
	return rest
}
