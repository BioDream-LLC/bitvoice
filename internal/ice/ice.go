// Package ice is a full ICE agent (RFC 8445) for one media stream of one component (RTP and RTCP
// multiplexed), for a BitVoice server that is itself behind NAT: it gathers host candidates, server
// reflexive ones through STUN (RFC 8489) and relayed ones through TURN (RFC 8656, UDP, long-term
// credentials), checks pairs with the far end in either role, nominates (controlling) or follows the
// far end's nomination (controlled), and keeps the chosen pair alive (consent, RFC 7675). Media on a
// relayed pair goes through the TURN server as ChannelData.
//
// The agent owns no socket: it sends with Send and is given every STUN and TURN datagram that arrives
// on the media socket through Handle, so candidates and media share one port.
package ice

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/stun"
)

// Type is a candidate's type.
type Type string

const (
	Host            Type = "host"
	ServerReflexive Type = "srflx"
	PeerReflexive   Type = "prflx"
	Relayed         Type = "relay"
)

// Candidate is one transport address (UDP, component 1).
type Candidate struct {
	Foundation string
	Priority   uint32
	Addr       netip.AddrPort
	Type       Type
	// Related is the base: the host address a server-reflexive candidate comes from, the mapped
	// address of a relayed one.
	Related netip.AddrPort
	// server is the TURN server of a relayed candidate.
	server netip.AddrPort
	// MDNS is the name of a remote host candidate a browser hid behind mDNS (Addr's address is then
	// unknown until it is resolved; see ResolveMDNS).
	MDNS string
}

// typePreference is RFC 8445 5.1.2.2's recommended values.
func typePreference(t Type) uint32 {
	switch t {
	case Host:
		return 126
	case PeerReflexive:
		return 110
	case ServerReflexive:
		return 100
	}
	return 0
}

// priority is RFC 8445 5.1.2.1's formula for component 1.
func priority(t Type, local uint32) uint32 {
	return typePreference(t)<<24 | (local&0xffff)<<8 | (256 - 1)
}

// SDP is the candidate as an SDP a=candidate value (RFC 8839).
func (c Candidate) SDP() string {
	s := fmt.Sprintf("%s 1 udp %d %s %d typ %s", c.Foundation, c.Priority, c.Addr.Addr(), c.Addr.Port(), c.Type)
	if c.Type != Host && c.Related.IsValid() {
		s += fmt.Sprintf(" raddr %s rport %d", c.Related.Addr(), c.Related.Port())
	}
	return s
}

// ParseCandidate reads an SDP a=candidate value. Only UDP candidates of component 1 are kept; others
// (TCP, RTCP's component 2 when rtcp-mux is used) are refused. A host candidate named by mDNS comes
// back with MDNS set and no address yet.
func ParseCandidate(v string) (Candidate, error) {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(v), "candidate:"))
	if len(f) < 8 || f[6] != "typ" {
		return Candidate{}, errors.New("ice: not a candidate")
	}
	if f[1] != "1" || !strings.EqualFold(f[2], "udp") {
		return Candidate{}, errors.New("ice: only UDP candidates of component 1 are used")
	}
	p, err := strconv.ParseUint(f[3], 10, 32)
	if err != nil {
		return Candidate{}, errors.New("ice: a bad priority")
	}
	port, err := strconv.Atoi(f[5])
	if err != nil || port <= 0 || port > 65535 {
		return Candidate{}, errors.New("ice: a bad port")
	}
	ip, err := netip.ParseAddr(f[4])
	mdns := ""
	if err != nil {
		if !IsMDNS(f[4]) {
			return Candidate{}, errors.New("ice: the address is not an IP address")
		}
		mdns, ip = f[4], netip.IPv4Unspecified()
	}
	c := Candidate{Foundation: f[0], Priority: uint32(p), Addr: netip.AddrPortFrom(ip.Unmap(), uint16(port)), Type: Type(f[7]), MDNS: mdns}
	switch c.Type {
	case Host, ServerReflexive, PeerReflexive, Relayed:
	default:
		return Candidate{}, errors.New("ice: an unknown candidate type")
	}
	return c, nil
}

// TURNServer is a TURN server to get a relayed candidate from (UDP).
type TURNServer struct {
	Addr               netip.AddrPort
	Username, Password string
}

// Config is what the agent is given.
type Config struct {
	// Hosts are the local addresses to offer as host candidates, with Port. Empty: none (only
	// server-reflexive and relayed ones, or a peer-reflexive one learnt from checks).
	Hosts []netip.Addr
	Port  int
	STUN  []netip.AddrPort
	TURN  []TURNServer
	// RelayOnly offers only relayed candidates (a policy, and for tests).
	RelayOnly bool
	// Controlling is the agent's role at the start (the offerer of two full agents is controlling).
	Controlling bool
	// LocalUfrag and LocalPwd are this side's ICE credentials, as in its SDP.
	LocalUfrag, LocalPwd string
	// Send sends a datagram from the media socket.
	Send func(pkt []byte, to netip.AddrPort)
	// GatherTimeout bounds how long STUN and TURN are waited for (default 1.5 s): those that have not
	// answered by then are left out.
	GatherTimeout time.Duration
}

// Pair is the chosen pair, given to the media path.
type Pair struct {
	Local  Candidate
	Remote netip.AddrPort
	// Relay is the TURN server and channel to send media through, when Local is a relayed candidate.
	Relay   netip.AddrPort
	Channel uint16
}

type pairState int

const (
	frozen pairState = iota
	waiting
	inProgress
	succeeded
	failed
)

type pair struct {
	local     Candidate
	remote    Candidate
	state     pairState
	nominated bool
	tries     int
	tx        [12]byte
	sentAt    time.Time
	useCand   bool
}

func (p *pair) priority(controlling bool) uint64 {
	g, d := uint64(p.local.Priority), uint64(p.remote.Priority)
	if !controlling {
		g, d = d, g
	}
	x := uint64(0)
	if g > d {
		x = 1
	}
	return min(g, d)<<32 + 2*max(g, d) + x
}

// turnAlloc is a relayed candidate's allocation.
type turnAlloc struct {
	server    TURNServer
	relayed   netip.AddrPort
	mapped    netip.AddrPort
	realm     string
	nonce     string
	key       []byte
	perms     map[netip.Addr]time.Time
	channels  map[netip.AddrPort]uint16
	nextChan  uint16
	refreshAt time.Time
}

// Agent is one stream's ICE agent.
type Agent struct {
	cfg Config

	mu          sync.Mutex
	controlling bool
	tiebreaker  uint64
	remoteUfrag string
	remotePwd   string
	local       []Candidate
	remote      []Candidate
	pairs       []*pair
	pending     map[[12]byte]func(*stun.Message, netip.AddrPort)
	allocs      []*turnAlloc
	selected    *pair
	selectedCh  chan Pair
	lastSeen    time.Time // the far end was last heard on the selected pair (consent)
	done        chan struct{}
	closeOnce   sync.Once
	started     bool
	nominating  bool
}

// New makes an agent.
func New(cfg Config) *Agent {
	if cfg.GatherTimeout <= 0 {
		cfg.GatherTimeout = 1500 * time.Millisecond
	}
	var tb [8]byte
	rand.Read(tb[:])
	return &Agent{cfg: cfg, controlling: cfg.Controlling, tiebreaker: binary.BigEndian.Uint64(tb[:]),
		pending: map[[12]byte]func(*stun.Message, netip.AddrPort){}, selectedCh: make(chan Pair, 1), done: make(chan struct{})}
}

// Close stops the agent's checks and refreshes, and frees TURN allocations.
func (a *Agent) Close() {
	a.closeOnce.Do(func() {
		close(a.done)
		a.mu.Lock()
		allocs := append([]*turnAlloc(nil), a.allocs...)
		a.mu.Unlock()
		for _, al := range allocs {
			a.sendTURN(al, methodRefresh, nil, func(b *stun.Builder) { b.Add(attrLifetime, []byte{0, 0, 0, 0}) })
		}
	})
}

// Selected is given the chosen pair once ICE has finished (and again if it changes).
func (a *Agent) Selected() <-chan Pair { return a.selectedCh }

// Controlling says the agent's role now (it may change on a role conflict).
func (a *Agent) Controlling() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.controlling
}

func txID() [12]byte {
	var t [12]byte
	rand.Read(t[:])
	return t
}

// TURN methods and attributes (RFC 8656 section 18).
const (
	methodAllocate   = 0x003
	methodRefresh    = 0x004
	methodSend       = 0x006
	methodData       = 0x007
	methodCreatePerm = 0x008
	methodChanBind   = 0x009

	classRequest    = 0x0000
	classIndication = 0x0010
	classSuccess    = 0x0100
	classError      = 0x0110

	attrUsername     = 0x0006
	attrErrorCode    = 0x0009
	attrChannel      = 0x000C
	attrLifetime     = 0x000D
	attrPeer         = 0x0012
	attrData         = 0x0013
	attrRealm        = 0x0014
	attrNonce        = 0x0015
	attrRelayed      = 0x0016
	attrTransport    = 0x0019
	attrMapped       = 0x0020
	attrPriority     = 0x0024
	attrUseCandidate = 0x0025
	attrControlled   = 0x8029
	attrControlling  = 0x802A
)

func msgType(method, class uint16) uint16 {
	return method&0x000F | (method&0x0070)<<1 | (method&0x0F80)<<2 | class
}

func methodOf(t uint16) uint16 { return t&0x000F | (t&0x00E0)>>1 | (t&0x3E00)>>2 }
func classOf(t uint16) uint16  { return t & 0x0110 }

func errorCode(m *stun.Message) int {
	v, ok := m.Get(attrErrorCode)
	if !ok || len(v) < 4 {
		return 0
	}
	return int(v[2])*100 + int(v[3])
}

// Gather finds the local candidates: hosts at once, server-reflexive and relayed ones as STUN and
// TURN answer, up to GatherTimeout.
func (a *Agent) Gather() []Candidate {
	var out []Candidate
	add := func(c Candidate) {
		for _, x := range out {
			if x.Addr == c.Addr && x.Type == c.Type {
				return
			}
		}
		out = append(out, c)
	}
	if !a.cfg.RelayOnly {
		for i, h := range a.cfg.Hosts {
			addr := netip.AddrPortFrom(h.Unmap(), uint16(a.cfg.Port))
			add(Candidate{Foundation: foundation(Host, h, netip.Addr{}), Priority: priority(Host, uint32(65535-i)), Addr: addr, Type: Host})
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	deadline := time.After(a.cfg.GatherTimeout)
	results := make(chan Candidate, 16)
	if !a.cfg.RelayOnly {
		for _, s := range a.cfg.STUN {
			wg.Add(1)
			go func(s netip.AddrPort) {
				defer wg.Done()
				if m, ok := a.binding(s); ok {
					results <- Candidate{Foundation: foundation(ServerReflexive, s.Addr(), s.Addr()), Priority: priority(ServerReflexive, 65535),
						Addr: m, Type: ServerReflexive, Related: netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(a.cfg.Port))}
				}
			}(s)
		}
	}
	for _, t := range a.cfg.TURN {
		wg.Add(1)
		go func(t TURNServer) {
			defer wg.Done()
			if al, err := a.allocate(t); err == nil {
				mu.Lock()
				a.mu.Lock()
				a.allocs = append(a.allocs, al)
				a.mu.Unlock()
				mu.Unlock()
				results <- Candidate{Foundation: foundation(Relayed, al.relayed.Addr(), t.Addr.Addr()), Priority: priority(Relayed, 65535),
					Addr: al.relayed, Type: Relayed, Related: al.mapped, server: t.Addr}
				if !a.cfg.RelayOnly && al.mapped.IsValid() {
					results <- Candidate{Foundation: foundation(ServerReflexive, t.Addr.Addr(), t.Addr.Addr()), Priority: priority(ServerReflexive, 65534),
						Addr: al.mapped, Type: ServerReflexive, Related: netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(a.cfg.Port))}
				}
			}
		}(t)
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	for {
		select {
		case c := <-results:
			add(c)
			continue
		case <-finished:
		case <-deadline:
		}
		break
	}
	// Drain what arrived at the last moment.
	for {
		select {
		case c := <-results:
			add(c)
			continue
		default:
		}
		break
	}
	// A server-reflexive candidate the same as a host one is redundant (RFC 8445 5.1.3).
	var kept []Candidate
	for _, c := range out {
		dup := false
		if c.Type == ServerReflexive {
			for _, h := range out {
				if h.Type == Host && h.Addr == c.Addr {
					dup = true
				}
			}
		}
		if !dup {
			kept = append(kept, c)
		}
	}
	a.mu.Lock()
	a.local = kept
	a.mu.Unlock()
	go a.keepAllocations()
	return kept
}

func foundation(t Type, base, server netip.Addr) string {
	h := md5.Sum([]byte(string(t) + base.String() + server.String()))
	return strconv.FormatUint(uint64(binary.BigEndian.Uint32(h[:])), 10)
}

// request sends a STUN request and waits for its answer (retransmitting as RFC 8489 6.2.1 says, at
// most a few times).
func (a *Agent) request(pkt []byte, tx [12]byte, to netip.AddrPort, tries int) (*stun.Message, bool) {
	got := make(chan *stun.Message, 1)
	a.mu.Lock()
	a.pending[tx] = func(m *stun.Message, _ netip.AddrPort) {
		select {
		case got <- m:
		default:
		}
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, tx)
		a.mu.Unlock()
	}()
	rto := 250 * time.Millisecond
	for range tries {
		a.cfg.Send(pkt, to)
		select {
		case m := <-got:
			return m, true
		case <-time.After(rto):
			rto *= 2
		case <-a.done:
			return nil, false
		}
	}
	return nil, false
}

// binding asks a STUN server for this socket's mapped address.
func (a *Agent) binding(server netip.AddrPort) (netip.AddrPort, bool) {
	tx := txID()
	b := stun.NewBuilder(stun.BindingRequest, tx)
	b.Fingerprint()
	m, ok := a.request(b.Bytes(), tx, server, 3)
	if !ok || m.Type != stun.BindingSuccess {
		return netip.AddrPort{}, false
	}
	addr, ok := m.MappedAddress()
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()), ok
}

func longTermKey(user, realm, pass string) []byte {
	h := md5.Sum([]byte(user + ":" + realm + ":" + pass))
	return h[:]
}

// allocate makes a TURN allocation (UDP relay), answering the server's challenge for credentials.
func (a *Agent) allocate(t TURNServer) (*turnAlloc, error) {
	al := &turnAlloc{server: t, perms: map[netip.Addr]time.Time{}, channels: map[netip.AddrPort]uint16{}, nextChan: 0x4000}
	for attempt := 0; attempt < 3; attempt++ {
		tx := txID()
		b := stun.NewBuilder(msgType(methodAllocate, classRequest), tx)
		b.Add(attrTransport, []byte{17, 0, 0, 0})
		if al.nonce != "" {
			b.Add(attrUsername, []byte(t.Username))
			b.Add(attrRealm, []byte(al.realm))
			b.Add(attrNonce, []byte(al.nonce))
			b.Integrity(al.key)
		}
		b.Fingerprint()
		m, ok := a.request(b.Bytes(), tx, t.Addr, 3)
		if !ok {
			return nil, errors.New("ice: the TURN server did not answer")
		}
		switch classOf(m.Type) {
		case classSuccess:
			if al.key != nil && m.CheckKey(al.key) != nil {
				return nil, errors.New("ice: the TURN server's answer is not signed with the credentials")
			}
			r, ok := m.XORAddress(attrRelayed)
			if !ok {
				return nil, errors.New("ice: the TURN server gave no relayed address")
			}
			al.relayed = netip.AddrPortFrom(r.Addr().Unmap(), r.Port())
			if mapped, ok := m.XORAddress(attrMapped); ok {
				al.mapped = netip.AddrPortFrom(mapped.Addr().Unmap(), mapped.Port())
			}
			life := 600 * time.Second
			if v, ok := m.Get(attrLifetime); ok && len(v) == 4 {
				life = time.Duration(binary.BigEndian.Uint32(v)) * time.Second
			}
			al.refreshAt = time.Now().Add(life / 2)
			return al, nil
		case classError:
			code := errorCode(m)
			if code == 401 || code == 438 {
				realm, _ := m.Get(attrRealm)
				nonce, _ := m.Get(attrNonce)
				if len(nonce) == 0 || (code == 401 && al.nonce != "" && string(nonce) == al.nonce) {
					return nil, errors.New("ice: the TURN server refused the credentials")
				}
				if len(realm) > 0 {
					al.realm = string(realm)
				}
				al.nonce = string(nonce)
				al.key = longTermKey(t.Username, al.realm, t.Password)
				continue
			}
			return nil, fmt.Errorf("ice: the TURN server refused the allocation (%d)", code)
		}
	}
	return nil, errors.New("ice: the TURN server kept asking for credentials")
}

// sendTURN sends a signed TURN request about an allocation and waits for its answer, retrying once
// with a fresh nonce (438).
func (a *Agent) sendTURN(al *turnAlloc, method uint16, peer *netip.AddrPort, extra func(*stun.Builder)) bool {
	for attempt := 0; attempt < 2; attempt++ {
		tx := txID()
		b := stun.NewBuilder(msgType(method, classRequest), tx)
		if peer != nil {
			b.AddXORAddress(attrPeer, *peer)
		}
		if extra != nil {
			extra(b)
		}
		b.Add(attrUsername, []byte(al.server.Username))
		b.Add(attrRealm, []byte(al.realm))
		b.Add(attrNonce, []byte(al.nonce))
		b.Integrity(al.key)
		b.Fingerprint()
		m, ok := a.request(b.Bytes(), tx, al.server.Addr, 3)
		if !ok {
			return false
		}
		if classOf(m.Type) == classSuccess {
			return true
		}
		if errorCode(m) == 438 {
			if n, ok := m.Get(attrNonce); ok {
				al.nonce = string(n)
				continue
			}
		}
		return false
	}
	return false
}

// permit makes sure the allocation lets a peer's address in (CreatePermission, 5 minutes).
func (a *Agent) permit(al *turnAlloc, peer netip.AddrPort) bool {
	a.mu.Lock()
	at, ok := al.perms[peer.Addr()]
	a.mu.Unlock()
	if ok && time.Since(at) < 4*time.Minute {
		return true
	}
	if !a.sendTURN(al, methodCreatePerm, &peer, nil) {
		return false
	}
	a.mu.Lock()
	al.perms[peer.Addr()] = time.Now()
	a.mu.Unlock()
	return true
}

// bindChannel binds a channel to a peer, for media (ChannelData has 4 bytes of overhead, not 36).
func (a *Agent) bindChannel(al *turnAlloc, peer netip.AddrPort) (uint16, bool) {
	a.mu.Lock()
	ch, ok := al.channels[peer]
	if !ok {
		ch = al.nextChan
		al.nextChan++
		al.channels[peer] = ch
	}
	a.mu.Unlock()
	var cb [4]byte
	binary.BigEndian.PutUint16(cb[:], ch)
	if !a.sendTURN(al, methodChanBind, &peer, func(b *stun.Builder) { b.Add(attrChannel, cb[:]) }) {
		return 0, false
	}
	a.mu.Lock()
	al.perms[peer.Addr()] = time.Now() // a channel bind installs a permission too
	a.mu.Unlock()
	return ch, true
}

// keepAllocations refreshes allocations, permissions and channels until the agent closes.
func (a *Agent) keepAllocations() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-t.C:
		}
		a.mu.Lock()
		allocs := append([]*turnAlloc(nil), a.allocs...)
		sel := a.selected
		a.mu.Unlock()
		for _, al := range allocs {
			if time.Now().After(al.refreshAt) {
				if a.sendTURN(al, methodRefresh, nil, func(b *stun.Builder) { b.Add(attrLifetime, []byte{0, 0, 0x02, 0x58}) }) {
					al.refreshAt = time.Now().Add(5 * time.Minute)
				}
			}
			if sel != nil && sel.local.Type == Relayed && sel.local.server == al.server.Addr {
				a.mu.Lock()
				at := al.perms[sel.remote.Addr.Addr()]
				a.mu.Unlock()
				if time.Since(at) > 4*time.Minute {
					a.bindChannel(al, sel.remote.Addr) // refreshes both channel and permission
				}
			}
		}
	}
}

func (a *Agent) allocFor(c Candidate) *turnAlloc {
	for _, al := range a.allocs {
		if al.relayed == c.Addr {
			return al
		}
	}
	return nil
}

// sendFrom sends a datagram from a local candidate: directly from the socket, or through the TURN
// server as a Send indication for a relayed one.
func (a *Agent) sendFrom(local Candidate, pkt []byte, to netip.AddrPort) {
	if local.Type != Relayed {
		a.cfg.Send(pkt, to)
		return
	}
	a.mu.Lock()
	al := a.allocFor(local)
	a.mu.Unlock()
	if al == nil {
		return
	}
	b := stun.NewBuilder(msgType(methodSend, classIndication), txID())
	b.AddXORAddress(attrPeer, to)
	b.Add(attrData, pkt)
	a.cfg.Send(b.Bytes(), al.server.Addr)
}

// Permit lets a peer's address through every relay before any of its candidates is known: the
// browser's own address as the web server saw it, for a browser that hid its addresses behind mDNS
// names. Its checks to the relayed candidates then get through and are answered.
func (a *Agent) Permit(ip netip.Addr) {
	a.mu.Lock()
	allocs := append([]*turnAlloc(nil), a.allocs...)
	a.mu.Unlock()
	for _, al := range allocs {
		go a.permit(al, netip.AddrPortFrom(ip.Unmap(), 1))
	}
}

// SetRemote gives the far end's credentials and candidates; Start begins checking once both sides
// are known. More candidates may come later with AddRemote (trickle).
func (a *Agent) SetRemote(ufrag, pwd string, cands []Candidate) {
	a.mu.Lock()
	a.remoteUfrag, a.remotePwd = ufrag, pwd
	a.mu.Unlock()
	for _, c := range cands {
		a.AddRemote(c)
	}
}

// AddRemote adds one remote candidate and pairs it with the local ones.
func (a *Agent) AddRemote(c Candidate) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.remote {
		if r.Addr == c.Addr {
			return
		}
	}
	a.remote = append(a.remote, c)
	for _, l := range a.bases() {
		if l.Addr.Addr().Is4() != c.Addr.Addr().Is4() {
			continue
		}
		if a.findPair(l.Addr, c.Addr) == nil && len(a.pairs) < 100 {
			a.pairs = append(a.pairs, &pair{local: l, remote: c, state: waiting})
		}
	}
	a.sortPairs()
}

// bases are the local candidates checks are sent from (a.mu held): server-reflexive candidates are
// not paired, as their base is the socket itself (RFC 8445 6.1.2.4); the socket is one host candidate
// per address family (every host address shares the one socket), and each relayed candidate is its own.
func (a *Agent) bases() []Candidate {
	var out []Candidate
	have4, have6 := false, false
	for _, l := range a.local {
		switch l.Type {
		case Host:
			if l.Addr.Addr().Is4() && !have4 {
				out, have4 = append(out, l), true
			} else if !l.Addr.Addr().Is4() && !have6 {
				out, have6 = append(out, l), true
			}
		case Relayed:
			out = append(out, l)
		}
	}
	if !have4 && !a.cfg.RelayOnly {
		// No host candidate offered (behind NAT with STUN only): the socket is still the base.
		out = append(out, socketBase(a.cfg.Port))
	}
	return out
}

func socketBase(port int) Candidate {
	return Candidate{Addr: netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)), Type: Host, Priority: priority(Host, 0)}
}

func (a *Agent) sortPairs() {
	ctl := a.controlling
	sort.SliceStable(a.pairs, func(i, j int) bool { return a.pairs[i].priority(ctl) > a.pairs[j].priority(ctl) })
}

func (a *Agent) findPair(local, remote netip.AddrPort) *pair {
	for _, p := range a.pairs {
		if p.local.Addr == local && p.remote.Addr == remote {
			return p
		}
	}
	return nil
}

// Start begins connectivity checks: one check every 50 ms (Ta, RFC 8445 14.2), retransmissions of
// checks in progress, and nomination when controlling.
func (a *Agent) Start() {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.mu.Unlock()
	go a.run()
}

func (a *Agent) run() {
	ta := time.NewTicker(50 * time.Millisecond)
	defer ta.Stop()
	consent := time.NewTicker(5 * time.Second)
	defer consent.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-consent.C:
			a.keepConsent()
		case <-ta.C:
			a.tick()
		}
	}
}

func (a *Agent) tick() {
	a.mu.Lock()
	if a.remotePwd == "" {
		a.mu.Unlock()
		return
	}
	now := time.Now()
	var next *pair
	for _, p := range a.pairs {
		if p.state == inProgress && now.Sub(p.sentAt) > rtoFor(p.tries) {
			if p.tries >= 7 {
				p.state = failed
				continue
			}
			next = p // retransmit
			break
		}
	}
	if next == nil {
		for _, p := range a.pairs {
			if p.state == waiting {
				next = p
				break
			}
		}
	}
	if next == nil && a.controlling && a.selected == nil && !a.nominating {
		// Nothing left to check: nominate the best pair that worked (regular nomination).
		var best *pair
		for _, p := range a.pairs {
			if p.state == succeeded && (best == nil || p.priority(true) > best.priority(true)) {
				best = p
			}
		}
		if best != nil {
			a.nominating = true
			best.useCand, best.state, best.tries = true, waiting, 0
			next = best
		}
	}
	if next != nil {
		a.check(next)
	}
	a.mu.Unlock()
}

func rtoFor(tries int) time.Duration {
	d := 100 * time.Millisecond
	for i := 1; i < tries && d < 1600*time.Millisecond; i++ {
		d *= 2
	}
	return d
}

// check sends a connectivity check on a pair (a.mu held).
func (a *Agent) check(p *pair) {
	if p.tries == 0 || p.state != inProgress {
		p.tx = txID()
	}
	p.state, p.sentAt = inProgress, time.Now()
	p.tries++
	b := stun.NewBuilder(stun.BindingRequest, p.tx)
	b.Add(attrUsername, []byte(a.remoteUfrag+":"+a.cfg.LocalUfrag))
	var pr [4]byte
	binary.BigEndian.PutUint32(pr[:], priority(PeerReflexive, 65535))
	b.Add(attrPriority, pr[:])
	var tb [8]byte
	binary.BigEndian.PutUint64(tb[:], a.tiebreaker)
	if a.controlling {
		b.Add(attrControlling, tb[:])
		if p.useCand {
			b.Add(attrUseCandidate, nil)
		}
	} else {
		b.Add(attrControlled, tb[:])
	}
	b.Integrity([]byte(a.remotePwd))
	b.Fingerprint()
	pkt := b.Bytes()
	local, remote, tx, useCand := p.local, p.remote.Addr, p.tx, p.useCand
	a.pending[tx] = func(m *stun.Message, from netip.AddrPort) { a.checkAnswer(local, remote, useCand, m, from) }
	go func() {
		if local.Type == Relayed {
			// The TURN server relays only to and from peers it has a permission for.
			a.mu.Lock()
			al := a.allocFor(local)
			a.mu.Unlock()
			if al == nil || !a.permit(al, remote) {
				return
			}
		}
		a.sendFrom(local, pkt, remote)
	}()
}

// checkAnswer handles the answer to a check.
func (a *Agent) checkAnswer(local Candidate, remote netip.AddrPort, useCand bool, m *stun.Message, from netip.AddrPort) {
	a.mu.Lock()
	p := a.findPair(local.Addr, remote)
	if p == nil {
		a.mu.Unlock()
		return
	}
	delete(a.pending, p.tx)
	if from != remote || m.CheckKey([]byte(a.remotePwd)) != nil {
		p.state = failed // a response from elsewhere, or not signed by the far end (RFC 8445 7.2.5.2)
		a.mu.Unlock()
		return
	}
	if m.Type == stun.BindingError {
		if errorCode(m) == 487 { // role conflict: switch and check again
			a.controlling = !a.controlling
			p.state, p.tries = waiting, 0
			a.sortPairs()
		} else {
			p.state = failed
		}
		a.mu.Unlock()
		return
	}
	p.state = succeeded
	// A mapped address unknown to the agent is a peer-reflexive local candidate (RFC 8445 7.2.5.3.1).
	if mapped, ok := m.MappedAddress(); ok && local.Type != Relayed {
		mapped = netip.AddrPortFrom(mapped.Addr().Unmap(), mapped.Port())
		known := false
		for _, l := range a.local {
			if l.Addr == mapped {
				known = true
			}
		}
		if !known && len(a.local) < 20 {
			a.local = append(a.local, Candidate{Foundation: foundation(PeerReflexive, mapped.Addr(), netip.Addr{}), Priority: priority(PeerReflexive, 65535), Addr: mapped, Type: PeerReflexive})
		}
	}
	nominate := useCand && a.controlling
	if !a.controlling && p.nominated {
		nominate = true // the far end nominated this pair before our check of it succeeded
	}
	a.mu.Unlock()
	if nominate {
		go a.choose(p) // off the media socket's reader: a relayed pair's channel bind waits for an answer on it
	}
}

// choose makes a pair the selected one and gives it to the media path.
func (a *Agent) choose(p *pair) {
	a.mu.Lock()
	if a.selected == p {
		a.mu.Unlock()
		return
	}
	if a.selected != nil && a.selected.priority(a.controlling) >= p.priority(a.controlling) {
		a.mu.Unlock()
		return // keep the better pair already chosen
	}
	a.selected, a.lastSeen = p, time.Now()
	a.mu.Unlock()
	out := Pair{Local: p.local, Remote: p.remote.Addr}
	if p.local.Type == Relayed {
		a.mu.Lock()
		al := a.allocFor(p.local)
		a.mu.Unlock()
		if al == nil {
			return
		}
		ch, ok := a.bindChannel(al, p.remote.Addr)
		if !ok {
			return
		}
		out.Relay, out.Channel = al.server.Addr, ch
	}
	select {
	case <-a.selectedCh:
	default:
	}
	a.selectedCh <- out
}

// keepConsent checks the chosen pair every few seconds; the answers also keep NAT bindings open.
func (a *Agent) keepConsent() {
	a.mu.Lock()
	p := a.selected
	if p == nil || a.remotePwd == "" {
		a.mu.Unlock()
		return
	}
	tx := txID()
	b := stun.NewBuilder(stun.BindingRequest, tx)
	b.Add(attrUsername, []byte(a.remoteUfrag+":"+a.cfg.LocalUfrag))
	var pr [4]byte
	binary.BigEndian.PutUint32(pr[:], p.local.Priority)
	b.Add(attrPriority, pr[:])
	var tb [8]byte
	binary.BigEndian.PutUint64(tb[:], a.tiebreaker)
	if a.controlling {
		b.Add(attrControlling, tb[:])
	} else {
		b.Add(attrControlled, tb[:])
	}
	b.Integrity([]byte(a.remotePwd))
	b.Fingerprint()
	local, remote := p.local, p.remote.Addr
	a.pending[tx] = func(m *stun.Message, from netip.AddrPort) {
		a.mu.Lock()
		delete(a.pending, tx)
		if from == remote && m.Type == stun.BindingSuccess {
			a.lastSeen = time.Now()
		}
		a.mu.Unlock()
	}
	a.mu.Unlock()
	if p.local.Type == Relayed {
		a.sendRelayedToSelected(local, b.Bytes(), remote)
		return
	}
	a.sendFrom(local, b.Bytes(), remote)
}

// sendRelayedToSelected sends through the channel once bound, else as a Send indication.
func (a *Agent) sendRelayedToSelected(local Candidate, pkt []byte, to netip.AddrPort) {
	a.mu.Lock()
	al := a.allocFor(local)
	var ch uint16
	if al != nil {
		ch = al.channels[to]
	}
	a.mu.Unlock()
	if al == nil {
		return
	}
	if ch != 0 {
		a.cfg.Send(ChannelData(ch, pkt), al.server.Addr)
		return
	}
	a.sendFrom(local, pkt, to)
}

// Valid says whether a check with the far end at addr has succeeded (DTLS may come from there).
func (a *Agent) Valid(addr netip.AddrPort) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.pairs {
		if p.remote.Addr == addr && p.state == succeeded {
			return true
		}
	}
	return false
}

// LastHeard is when the far end last answered a consent check on the chosen pair.
func (a *Agent) LastHeard() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastSeen
}

// ChannelData wraps a datagram for a TURN channel (RFC 8656 12.4).
func ChannelData(ch uint16, pkt []byte) []byte {
	out := make([]byte, 4+len(pkt), 4+len(pkt)+3)
	binary.BigEndian.PutUint16(out, ch)
	binary.BigEndian.PutUint16(out[2:], uint16(len(pkt)))
	copy(out[4:], pkt)
	return out
}

// UnwrapChannelData returns a ChannelData message's channel and datagram.
func UnwrapChannelData(b []byte) (uint16, []byte, bool) {
	if len(b) < 4 || b[0] < 0x40 || b[0] > 0x4F {
		return 0, nil, false
	}
	n := int(binary.BigEndian.Uint16(b[2:]))
	if 4+n > len(b) {
		return 0, nil, false
	}
	return binary.BigEndian.Uint16(b), b[4 : 4+n], true
}

// Handle takes a STUN or TURN datagram that arrived on the media socket. It answers checks, takes
// answers to the agent's requests, and unwraps what the TURN server relays from the far end
// (returned as inner with the far end's address, for the caller to handle as if it came directly).
// consumed is false for datagrams that are not the agent's.
func (a *Agent) Handle(pkt []byte, from netip.AddrPort) (consumed bool, inner []byte, innerFrom netip.AddrPort) {
	from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
	// Relayed through a TURN server: a Data indication, or ChannelData on a channel we bound.
	if a.isTURNServer(from) {
		if ch, data, ok := UnwrapChannelData(pkt); ok {
			if peer, ok := a.peerOfChannel(from, ch); ok {
				return true, data, peer
			}
			return true, nil, netip.AddrPort{}
		}
	}
	if !stun.IsSTUN(pkt) {
		return false, nil, netip.AddrPort{}
	}
	m, err := stun.Parse(pkt)
	if err != nil {
		return true, nil, netip.AddrPort{}
	}
	if m.Type == msgType(methodData, classIndication) && a.isTURNServer(from) {
		peer, ok1 := m.XORAddress(attrPeer)
		data, ok2 := m.Get(attrData)
		if ok1 && ok2 {
			peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
			if relayed := a.relayedOf(from); relayed.IsValid() && stun.IsSTUN(data) {
				a.handleSTUN(data, peer, relayed)
				return true, nil, netip.AddrPort{}
			}
			return true, data, peer
		}
		return true, nil, netip.AddrPort{}
	}
	a.handleSTUN(pkt, from, netip.AddrPort{})
	return true, nil, netip.AddrPort{}
}

// HandleRelayed takes a STUN datagram the media path unwrapped from a TURN channel (from the far end).
func (a *Agent) HandleRelayed(pkt []byte, peer netip.AddrPort, relayServer netip.AddrPort) {
	if relayed := a.relayedOf(relayServer); relayed.IsValid() {
		a.handleSTUN(pkt, peer, relayed)
	}
}

func (a *Agent) isTURNServer(addr netip.AddrPort) bool {
	for _, t := range a.cfg.TURN {
		if t.Addr == addr {
			return true
		}
	}
	return false
}

func (a *Agent) relayedOf(server netip.AddrPort) netip.AddrPort {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, al := range a.allocs {
		if al.server.Addr == server {
			return al.relayed
		}
	}
	return netip.AddrPort{}
}

func (a *Agent) peerOfChannel(server netip.AddrPort, ch uint16) (netip.AddrPort, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, al := range a.allocs {
		if al.server.Addr != server {
			continue
		}
		for peer, c := range al.channels {
			if c == ch {
				return peer, true
			}
		}
	}
	return netip.AddrPort{}, false
}

// handleSTUN takes a STUN message from the far end (or a server); arrivedOn is the relayed candidate
// it came through, invalid when it came to the socket directly.
func (a *Agent) handleSTUN(pkt []byte, from, arrivedOn netip.AddrPort) {
	m, err := stun.Parse(pkt)
	if err != nil {
		return
	}
	if m.Type != stun.BindingRequest {
		a.mu.Lock()
		f := a.pending[m.TxID]
		a.mu.Unlock()
		if f != nil {
			f(m, from)
		}
		return
	}
	// A check from the far end.
	a.mu.Lock()
	lu, lp := a.cfg.LocalUfrag, a.cfg.LocalPwd
	a.mu.Unlock()
	if !strings.HasPrefix(m.Username(), lu+":") || m.Check(lp) != nil {
		return
	}
	local := a.localFor(arrivedOn, from)
	if a.cfg.RelayOnly && !arrivedOn.IsValid() {
		return // relay only: nothing is answered on the socket directly
	}
	// Role conflict (RFC 8445 7.3.1.1).
	_, theyCtl := m.Get(attrControlling)
	_, theyCtd := m.Get(attrControlled)
	a.mu.Lock()
	conflict := false
	if a.controlling && theyCtl {
		v, _ := m.Get(attrControlling)
		if len(v) == 8 && a.tiebreaker >= binary.BigEndian.Uint64(v) {
			conflict = true
		} else {
			a.controlling = false
			a.sortPairs()
		}
	} else if !a.controlling && theyCtd {
		v, _ := m.Get(attrControlled)
		if len(v) == 8 && a.tiebreaker >= binary.BigEndian.Uint64(v) {
			a.controlling = true
			a.sortPairs()
		} else {
			conflict = true
		}
	}
	a.mu.Unlock()
	if conflict {
		b := stun.NewBuilder(stun.BindingError, m.TxID)
		b.Add(attrErrorCode, []byte{0, 0, 4, 87})
		b.Integrity([]byte(lp))
		b.Fingerprint()
		a.sendFrom(local, b.Bytes(), from)
		return
	}
	a.sendFrom(local, stun.BindingResponse(m, from, lp), from)
	// The far end's address may be new: a peer-reflexive remote candidate (RFC 8445 7.3.1.3).
	var pr uint32
	if v, ok := m.Get(attrPriority); ok && len(v) == 4 {
		pr = binary.BigEndian.Uint32(v)
	}
	a.mu.Lock()
	known := false
	for _, r := range a.remote {
		if r.Addr == from {
			known = true
		}
	}
	if !known && len(a.remote) < 50 {
		a.remote = append(a.remote, Candidate{Foundation: "prflx" + strconv.Itoa(len(a.remote)), Priority: pr, Addr: from, Type: PeerReflexive})
	}
	p := a.findPair(local.Addr, from)
	if p == nil && local.Addr.IsValid() {
		p = &pair{local: local, remote: Candidate{Priority: pr, Addr: from, Type: PeerReflexive}, state: waiting}
		a.pairs = append(a.pairs, p)
		a.sortPairs()
	}
	nominated := m.UseCandidate() && !a.controlling
	var choose bool
	if p != nil {
		if nominated {
			p.nominated = true
		}
		switch p.state {
		case succeeded:
			choose = nominated
		case failed, frozen, waiting:
			// A triggered check (RFC 8445 7.3.1.4), at once.
			p.state, p.tries = waiting, 0
			a.check(p)
		}
	}
	if a.selected != nil && a.selected == p {
		a.lastSeen = time.Now()
	}
	a.mu.Unlock()
	if choose {
		go a.choose(p)
	}
}

// localFor is the base a datagram from from arrived on: the relayed candidate it came through, or
// the socket's base of from's address family.
func (a *Agent) localFor(arrivedOn, from netip.AddrPort) Candidate {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, l := range a.bases() {
		if arrivedOn.IsValid() {
			if l.Type == Relayed && l.Addr == arrivedOn {
				return l
			}
			continue
		}
		if l.Type == Host && l.Addr.Addr().Is4() == from.Addr().Is4() {
			return l
		}
	}
	return socketBase(a.cfg.Port)
}
