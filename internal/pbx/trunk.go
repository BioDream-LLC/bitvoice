package pbx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// trunkState is what BitVoice knows about a trunk right now.
type trunkState struct {
	cfg        config.Trunk
	addrs      []netip.Addr
	resolved   time.Time
	state      string // registering, registered, failed, reachable, unreachable, disabled
	lastError  string
	expires    time.Time
	nextCheck  time.Time
	callID     string
	cseq       uint32
	latencyMS  int
	lastChange time.Time
	owner      string // for a trunk held one server at a time by another server: its name
}

// TrunkStatus is a trunk's live state for the interface.
type TrunkStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Addresses []string  `json:"addresses"`
	Expires   time.Time `json:"expires,omitzero"`
	LatencyMS int       `json:"latencyMs"`
	Since     time.Time `json:"since,omitzero"`
	// HeldBy names the server registering a trunk shared one server at a time, when not this one.
	HeldBy string `json:"heldBy,omitempty"`
}

// TrunkStatus returns every trunk's state.
func (e *Engine) TrunkStatus() []TrunkStatus {
	cfg := e.Config()
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []TrunkStatus{}
	for _, t := range cfg.Trunks {
		ts := TrunkStatus{ID: t.ID, Name: t.Name, State: "disabled", Addresses: []string{}}
		if st := e.trunks[t.ID]; st != nil {
			ts.State, ts.Error, ts.Expires, ts.LatencyMS, ts.Since, ts.HeldBy = st.state, st.lastError, st.expires, st.latencyMS, st.lastChange, st.owner
			for _, a := range st.addrs {
				ts.Addresses = append(ts.Addresses, a.String())
			}
		}
		out = append(out, ts)
	}
	return out
}

func (e *Engine) syncTrunks(cfg config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[string]bool{}
	for _, t := range cfg.Trunks {
		seen[t.ID] = true
		st := e.trunks[t.ID]
		if !t.Enabled {
			delete(e.trunks, t.ID)
			continue
		}
		if st == nil || st.cfg.Host != t.Host || st.cfg.Port != t.Port || st.cfg.Username != t.Username ||
			st.cfg.Password != t.Password || st.cfg.Register != t.Register || st.cfg.Transport != t.Transport ||
			st.cfg.AuthUser != t.AuthUser {
			e.trunks[t.ID] = &trunkState{cfg: t, state: "starting", callID: sip.NewCallID(), lastChange: time.Now()}
			continue
		}
		st.cfg = t
	}
	for id := range e.trunks {
		if !seen[id] {
			delete(e.trunks, id)
		}
	}
}

func (e *Engine) runTrunks(ctx context.Context) {
	e.syncTrunks(*e.Config())
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		e.mu.Lock()
		var due []*trunkState
		now := time.Now()
		for _, st := range e.trunks {
			if now.After(st.nextCheck) {
				st.nextCheck = now.Add(30 * time.Second) // until the check reschedules it
				due = append(due, st)
			}
		}
		e.mu.Unlock()
		for _, st := range due {
			go e.checkTrunk(ctx, st)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) setTrunk(st *trunkState, state, errText string, next time.Duration) {
	e.mu.Lock()
	changed := st.state != state || st.lastError != errText
	if st.state != state {
		st.lastChange = time.Now()
	}
	st.state, st.lastError = state, errText
	st.nextCheck = time.Now().Add(next)
	e.mu.Unlock()
	if changed {
		e.publish("trunks.changed", map[string]any{"id": st.cfg.ID, "state": state, "error": errText})
	}
}

// checkTrunk registers a trunk that needs it, or pings one that does not.
func (e *Engine) checkTrunk(ctx context.Context, st *trunkState) {
	if err := e.resolveTrunk(ctx, st); err != nil {
		e.setTrunk(st, "unreachable", err.Error(), 30*time.Second)
		return
	}
	peer, err := e.trunkPeer(&st.cfg)
	if err != nil {
		e.setTrunk(st, "unreachable", err.Error(), 30*time.Second)
		return
	}
	if e.Cluster != nil && st.cfg.Shared == "one" {
		node, mine := e.Cluster.TrunkOwner(st.cfg.ID)
		if !mine {
			// Another server holds it: stand by, and let go of any registration this server had.
			e.mu.Lock()
			was := st.state == "registered"
			st.owner = e.Cluster.NodeName(node)
			e.mu.Unlock()
			if was && st.cfg.Register {
				e.unregisterTrunk(st, peer)
			}
			why := "registered by " + e.Cluster.NodeName(node) + "; this server sends its calls through it"
			if node == "" {
				why = "waiting for a server to take it"
			}
			e.setTrunk(st, "standby", why, 3*time.Second)
			return
		}
		e.mu.Lock()
		st.owner = ""
		if st.state == "standby" {
			st.expires = time.Time{} // register now, it was another server's until a moment ago
		}
		e.mu.Unlock()
	}
	if st.cfg.Register {
		e.registerTrunk(st, peer)
		return
	}
	start := time.Now()
	m := e.trunkRequest(st, sip.OPTIONS, peer)
	resp := finalOf(e.stack.Request(m, peer))
	if resp == nil || resp.StatusCode == 408 || resp.StatusCode == 503 && resp.Get("Server") == "BitVoice" {
		e.setTrunk(st, "unreachable", "no answer to OPTIONS", 30*time.Second)
		return
	}
	e.mu.Lock()
	st.latencyMS = int(time.Since(start).Milliseconds())
	e.mu.Unlock()
	// Any response at all, even 403 or 404, proves the provider is there and answering.
	e.setTrunk(st, "reachable", "", 60*time.Second)
}

func finalOf(tx *sip.ClientTx) *sip.Message {
	var final *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	return final
}

func (e *Engine) trunkRequest(st *trunkState, method string, peer sip.Peer) *sip.Message {
	host := sip.HostPort(st.cfg.Host, st.cfg.Port)
	user := st.cfg.Username
	if user == "" {
		user = "bitvoice"
	}
	e.mu.Lock()
	st.cseq++
	seq := st.cseq
	e.mu.Unlock()
	ruri := "sip:" + host
	if st.cfg.Transport == "tcp" || st.cfg.Transport == "tls" {
		ruri += ";transport=" + st.cfg.Transport
	}
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: ruri}
	aor := "<sip:" + user + "@" + st.cfg.Host + ">"
	if st.cfg.Teams && st.cfg.SBCName != "" {
		aor = "<sip:" + user + "@" + st.cfg.SBCName + ">"
		teamsHeaders(m)
	}
	m.Set("From", aor+";tag="+sip.NewTag())
	m.Set("To", aor)
	if method == sip.OPTIONS {
		m.Set("To", "<"+ruri+">")
		m.Set("Call-ID", sip.NewCallID())
	} else {
		m.Set("Call-ID", st.callID)
	}
	m.Set("CSeq", strconv.FormatUint(uint64(seq), 10)+" "+method)
	m.Set("Contact", e.localContact(peer, user))
	m.Set("User-Agent", "BitVoice")
	return m
}

// unregisterTrunk removes this server's registration (Expires 0), when another server takes over.
func (e *Engine) unregisterTrunk(st *trunkState, peer sip.Peer) {
	m := e.trunkRequest(st, sip.REGISTER, peer)
	m.Set("Expires", "0")
	resp := finalOf(e.stack.Request(m, peer))
	if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 407) {
		h, header := resp.Get("WWW-Authenticate"), "Authorization"
		if resp.StatusCode == 407 {
			h, header = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
		}
		if ch, err := sip.ParseChallenge(h); err == nil {
			user := st.cfg.AuthUser
			if user == "" {
				user = st.cfg.Username
			}
			next := e.trunkRequest(st, sip.REGISTER, peer)
			next.Set("Expires", "0")
			if cr, err := sip.Answer(ch, user, st.cfg.Password, sip.REGISTER, next.RequestURI); err == nil {
				next.Set(header, cr.String())
				finalOf(e.stack.Request(next, peer))
			}
		}
	}
}

func (e *Engine) registerTrunk(st *trunkState, peer sip.Peer) {
	const expires = 300
	start := time.Now()
	m := e.trunkRequest(st, sip.REGISTER, peer)
	m.Set("Expires", strconv.Itoa(expires))
	resp := finalOf(e.stack.Request(m, peer))
	for i := 0; i < 2 && resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 407); i++ {
		h, header := resp.Get("WWW-Authenticate"), "Authorization"
		if resp.StatusCode == 407 {
			h, header = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
		}
		ch, err := sip.ParseChallenge(h)
		if err != nil {
			break
		}
		user := st.cfg.AuthUser
		if user == "" {
			user = st.cfg.Username
		}
		next := e.trunkRequest(st, sip.REGISTER, peer)
		next.Set("Expires", strconv.Itoa(expires))
		cr, err := sip.Answer(ch, user, st.cfg.Password, sip.REGISTER, next.RequestURI)
		if err != nil {
			break
		}
		next.Set(header, cr.String())
		resp = finalOf(e.stack.Request(next, peer))
	}
	switch {
	case resp == nil || resp.StatusCode == 408:
		e.setTrunk(st, "failed", "the provider did not answer", 30*time.Second)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		granted := expires
		if v := resp.Get("Expires"); v != "" {
			if n, ok := sip.ParseExpires(v); ok && n > 0 {
				granted = n
			}
		}
		for _, c := range resp.GetAll("Contact") {
			if a, err := sip.ParseAddress(c); err == nil {
				if v, ok := a.Params.Get("expires"); ok {
					if n, ok := sip.ParseExpires(v); ok && n > 0 && n < granted {
						granted = n
					}
				}
			}
		}
		e.mu.Lock()
		st.expires = time.Now().Add(time.Duration(granted) * time.Second)
		st.latencyMS = int(time.Since(start).Milliseconds())
		e.mu.Unlock()
		// Refresh at half the granted time, never less than 20 seconds.
		refresh := time.Duration(granted) * time.Second / 2
		if refresh < 20*time.Second {
			refresh = 20 * time.Second
		}
		e.setTrunk(st, "registered", "", refresh)
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 407:
		e.setTrunk(st, "failed", "the provider refused the username or password", 60*time.Second)
	default:
		e.setTrunk(st, "failed", "the provider answered "+strconv.Itoa(resp.StatusCode)+" "+resp.Reason, 60*time.Second)
	}
}

func (e *Engine) resolveTrunk(ctx context.Context, st *trunkState) error {
	e.mu.Lock()
	fresh := time.Since(st.resolved) < 5*time.Minute && len(st.addrs) > 0
	host := st.cfg.Host
	e.mu.Unlock()
	if fresh {
		return nil
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a.Unmap()}
	} else {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
		if err != nil {
			return errors.New("cannot find " + host + " in DNS")
		}
		for _, ip := range ips {
			addrs = append(addrs, ip.Unmap())
		}
	}
	e.mu.Lock()
	st.addrs, st.resolved = addrs, time.Now()
	e.mu.Unlock()
	return nil
}

// trunkPeer returns where to send a trunk's requests, preferring IPv4 addresses.
func (e *Engine) trunkPeer(t *config.Trunk) (sip.Peer, error) {
	transport := strings.ToUpper(t.Transport)
	if transport == "" {
		transport = "UDP"
	}
	port := t.Port
	if port == 0 {
		port = 5060
		if transport == "TLS" {
			port = 5061
		}
	}
	e.mu.Lock()
	st := e.trunks[t.ID]
	var addrs []netip.Addr
	if st != nil && strings.EqualFold(st.cfg.Host, t.Host) { // a Teams backup proxy is looked up itself
		addrs = append(addrs, st.addrs...)
	}
	e.mu.Unlock()
	if len(addrs) == 0 {
		if a, err := netip.ParseAddr(t.Host); err == nil {
			addrs = []netip.Addr{a}
		} else {
			ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", t.Host)
			if err != nil || len(ips) == 0 {
				return sip.Peer{}, errors.New("cannot find " + t.Host + " in DNS")
			}
			addrs = ips
		}
	}
	best := addrs[0]
	for _, a := range addrs {
		if a.Unmap().Is4() {
			best = a.Unmap()
			break
		}
	}
	return sip.Peer{Transport: transport, Addr: netip.AddrPortFrom(best, uint16(port)), Host: t.Host}, nil
}
