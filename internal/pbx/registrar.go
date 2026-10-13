package pbx

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Binding is one registered contact for an extension.
type Binding struct {
	Extension string    `json:"extension"`
	Name      string    `json:"name"`
	Contact   string    `json:"contact"`
	Source    string    `json:"source"`
	Transport string    `json:"transport"`
	UserAgent string    `json:"userAgent"`
	Expires   time.Time `json:"expires"`
	Since     time.Time `json:"since"`

	peer   sip.Peer
	uri    *sip.URI
	callID string
	cseq   uint32
	node   string // another server holds this phone: calls go through it
}

// Registrar keeps where each extension's phones can be reached.
type Registrar struct {
	e        *Engine
	mu       sync.Mutex
	bindings map[string][]*Binding // by extension number
}

const (
	minExpires     = 60
	maxExpires     = 3600
	defaultExpires = 300
	// keepAlive is how often UDP registrations get a CRLF to hold NAT bindings open. Many home routers
	// forget a UDP mapping after 30 seconds of silence.
	keepAlive = 25 * time.Second
)

func newRegistrar(e *Engine) *Registrar {
	return &Registrar{e: e, bindings: map[string][]*Binding{}}
}

func (r *Registrar) handle(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	cfg := r.e.Config()
	to, err := sip.ParseAddress(m.Get("To"))
	if err != nil {
		tx.Respond(sip.NewResponse(m, 400, "Bad To"))
		return
	}
	number := r.e.localUser(to.URI.User)
	ext := cfg.FindExtension(number)
	lookup := func(user string) (string, bool) {
		x := cfg.FindExtension(r.e.localUser(user))
		if x == nil || !x.Enabled {
			return "", false
		}
		return x.Secret, true
	}
	result, user := r.e.auth.Verify(m.Get("Authorization"), sip.REGISTER, lookup)
	user = r.e.localUser(user)
	ip := in.From.Addr.Addr()
	switch result {
	case sip.AuthOK:
		if user != number || ext == nil {
			// Authenticated as one extension but registering another: refuse.
			r.e.bans.fail(ip, "registration for another extension")
			tx.Respond(sip.NewResponse(m, 403, ""))
			return
		}
		r.e.bans.succeed(ip)
	case sip.AuthBad:
		if r.e.bans.fail(ip, "failed registrations as "+user) {
			r.e.Log.Warn("banned address after repeated registration failures", "ip", ip, "user", user)
			r.e.publish("security.banned", map[string]any{"ip": ip.String(), "reason": "failed registrations as " + user})
		}
		r.challenge(tx, m, false)
		return
	case sip.AuthStale:
		r.challenge(tx, m, true)
		return
	default:
		r.challenge(tx, m, false)
		return
	}

	expiresHeader := defaultExpires
	if v := m.Get("Expires"); v != "" {
		if n, ok := sip.ParseExpires(v); ok {
			expiresHeader = n
		}
	}
	callID := m.CallID()
	cseq, _ := m.CSeq()
	now := time.Now()

	r.mu.Lock()
	list := r.bindings[number]
	for _, c := range m.GetAll("Contact") {
		a, err := sip.ParseAddress(c)
		if err != nil {
			continue
		}
		if a.Star {
			if expiresHeader != 0 {
				r.mu.Unlock()
				tx.Respond(sip.NewResponse(m, 400, "Wildcard Contact Needs Expires 0"))
				return
			}
			list = nil
			continue
		}
		exp := expiresHeader
		if v, ok := a.Params.Get("expires"); ok {
			if n, ok := sip.ParseExpires(v); ok {
				exp = n
			}
		}
		if exp != 0 && exp < minExpires {
			r.mu.Unlock()
			resp := sip.NewResponse(m, 423, "")
			resp.Set("Min-Expires", strconv.Itoa(minExpires))
			tx.Respond(resp)
			return
		}
		if exp > maxExpires {
			exp = maxExpires
		}
		uri := a.URI.String()
		// Drop the old binding for the same contact (or the same phone re-registering on a new address).
		kept := list[:0]
		var since time.Time
		for _, b := range list {
			// The same contact, or the same phone (same Call-ID) re-registering from a new address, replaces
			// the old binding and keeps its "online since" time.
			if b.Contact == uri || b.callID == callID {
				if since.IsZero() {
					since = b.Since
				}
				continue
			}
			kept = append(kept, b)
		}
		list = kept
		if exp == 0 {
			continue
		}
		if since.IsZero() {
			since = now
		}
		list = append(list, &Binding{Extension: number, Name: ext.Name, Contact: uri, Source: in.From.Addr.String(),
			Transport: in.From.Transport, UserAgent: m.Get("User-Agent"), Expires: now.Add(time.Duration(exp) * time.Second),
			Since: since, peer: in.From, uri: a.URI, callID: callID, cseq: cseq})
	}
	// A phone may register many contacts; keep the ten newest so a misbehaving one cannot grow without bound.
	if len(list) > 10 {
		list = list[len(list)-10:]
	}
	if len(list) == 0 {
		delete(r.bindings, number)
	} else {
		r.bindings[number] = list
	}
	resp := sip.NewResponse(m, 200, "")
	resp.Set("To", withTag(m.Get("To")))
	for _, b := range list {
		resp.Add("Contact", "<"+b.Contact+">;expires="+strconv.Itoa(int(time.Until(b.Expires).Seconds())))
	}
	resp.Set("Date", now.UTC().Format(time.RFC1123))
	var fresh *Binding
	for _, b := range list {
		if b.Since.Equal(now) {
			fresh = b // a phone that was not registered before
		}
	}
	r.mu.Unlock()
	tx.Respond(resp)
	r.e.publish("registrations.changed", map[string]any{"extension": number})
	if fresh != nil {
		go r.e.registered(number, fresh)
	}
	if r.e.SeenDevice != nil && m.Get("User-Agent") != "" && len(list) > 0 {
		go r.e.SeenDevice(m.Get("User-Agent"), number)
	}
}

func (r *Registrar) challenge(tx *sip.ServerTx, m *sip.Message, stale bool) {
	resp := sip.NewResponse(m, 401, "")
	resp.Set("To", withTag(m.Get("To")))
	resp.Set("WWW-Authenticate", r.e.auth.Challenge(stale))
	tx.Respond(resp)
}

// contacts returns where to reach an extension now.
// registeredFrom says whether a phone is registered from exactly this address and port: its requests
// are a phone's even when a trunk shares the address (a provider allowed in from the same network).
func (r *Registrar) registeredFrom(src netip.AddrPort) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, bs := range r.bindings {
		for _, b := range bs {
			if now.Before(b.Expires) && b.peer.Addr == src {
				return true
			}
		}
	}
	return false
}

// contacts lists where an extension's phones are: on this server, and on the others in its cluster.
func (r *Registrar) contacts(number string) []*Binding {
	numbers := append([]string{number}, r.e.desksOf(number)...) // and any hot desk they are signed in at
	// The same phone listed by two servers has moved: its newest registration is where it is.
	best := map[string]*Binding{}
	var order []string
	for _, b := range append(r.local(numbers), r.e.remoteContacts(numbers)...) {
		cur, ok := best[b.Contact]
		if !ok {
			order = append(order, b.Contact)
		}
		if !ok || b.Since.After(cur.Since) {
			best[b.Contact] = b
		}
	}
	out := make([]*Binding, 0, len(order))
	for _, c := range order {
		out = append(out, best[c])
	}
	return out
}

// localContacts lists only the phones registered with this server.
func (r *Registrar) localContacts(number string) []*Binding {
	return r.local(append([]string{number}, r.e.desksOf(number)...))
}

func (r *Registrar) local(numbers []string) []*Binding {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var out []*Binding
	for _, n := range numbers {
		for _, b := range r.bindings[n] {
			if now.Before(b.Expires) {
				c := *b
				out = append(out, &c)
			}
		}
	}
	return out
}

func (r *Registrar) list() []Binding {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := []Binding{}
	for _, list := range r.bindings {
		for _, b := range list {
			if now.Before(b.Expires) {
				out = append(out, *b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Extension != out[j].Extension {
			return out[i].Extension < out[j].Extension
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// prune drops bindings of extensions that were deleted or disabled.
func (r *Registrar) prune(cfg config.Config) {
	r.mu.Lock()
	changed := false
	for number := range r.bindings {
		x := cfg.FindExtension(number)
		if x == nil || !x.Enabled {
			delete(r.bindings, number)
			changed = true
		}
	}
	r.mu.Unlock()
	if changed {
		r.e.publish("registrations.changed", map[string]any{})
	}
}

func (r *Registrar) run(ctx context.Context) {
	t := time.NewTicker(keepAlive)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		r.e.signOutNightly(now)
		var alive []sip.Peer
		expired := false
		r.mu.Lock()
		for number, list := range r.bindings {
			kept := list[:0]
			for _, b := range list {
				if now.Before(b.Expires) {
					kept = append(kept, b)
					if b.peer.Transport == "UDP" {
						alive = append(alive, b.peer)
					}
				} else {
					expired = true
				}
			}
			if len(kept) == 0 {
				delete(r.bindings, number)
			} else {
				r.bindings[number] = kept
			}
		}
		r.mu.Unlock()
		for _, p := range alive {
			_ = r.e.stack.SendRaw([]byte("\r\n\r\n"), p)
		}
		if expired {
			r.e.publish("registrations.changed", map[string]any{})
		}
	}
}

// requestURIFor is where to address a request to a binding: its registered contact.
func (b *Binding) requestURI() string {
	return b.uri.String()
}

func (b *Binding) shortAgent() string {
	if i := strings.IndexByte(b.UserAgent, ' '); i > 0 {
		return b.UserAgent[:i]
	}
	return b.UserAgent
}
