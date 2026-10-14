// Package turn is a TURN server (RFC 8656) for BitVoice's browser and WebRTC phones on networks that
// block UDP or hide behind strict NATs: UDP, TCP and TLS on one port each, long-term credentials made
// from a shared secret (the "TURN REST" usernames, expiry:name), and relaying only to this server's own
// addresses, so it cannot be used to reach anything else.
package turn

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/stun"
)

// STUN methods and attributes TURN adds (RFC 8656 section 18).
const (
	methodBinding    = 0x001
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

	attrUsername    = 0x0006
	attrIntegrity   = 0x0008
	attrErrorCode   = 0x0009
	attrChannel     = 0x000C
	attrLifetime    = 0x000D
	attrPeer        = 0x0012
	attrData        = 0x0013
	attrRealm       = 0x0014
	attrNonce       = 0x0015
	attrRelayed     = 0x0016
	attrTransport   = 0x0019
	attrMapped      = 0x0020
	attrSoftware    = 0x8022
	attrFingerprint = 0x8028

	defaultLifetime = 10 * time.Minute
	maxLifetime     = time.Hour
	permLifetime    = 5 * time.Minute
	chanLifetime    = 10 * time.Minute
)

func method(t uint16) uint16 { return t&0x000F | (t&0x00E0)>>1 | (t&0x3E00)>>2 }
func class(t uint16) uint16  { return t & 0x0110 }
func msgType(m, c uint16) uint16 {
	return m&0x000F | (m&0x0070)<<1 | (m&0x0F80)<<2 | c
}

// Server relays for authenticated clients.
type Server struct {
	Realm string
	// Secret is the shared secret credentials are made from (see Credentials).
	Secret func() []byte
	// Route says whether a peer may be relayed to (this server's own addresses only) and where to send
	// to reach it: the server's public address behind a NAT is reached at a local one, not through the router.
	Route func(netip.Addr) (netip.Addr, bool)
	// RelayIP is the address relay ports are opened on (empty: every address); Advertise the address
	// clients are told (empty: RelayIP).
	RelayIP   netip.Addr
	Advertise netip.Addr
	// PortMin and PortMax bound the relay ports (both 0: any free port).
	PortMin, PortMax int
	Log              *slog.Logger

	mu     sync.Mutex
	allocs map[string]*allocation // by client 5-tuple
	nonceK [16]byte
	once   sync.Once
	next   int
}

// Credentials makes a username and password valid for ttl: the username is the expiry time and a
// label, the password the base64 HMAC-SHA1 of the username with the secret (as coturn's use-auth-secret).
func Credentials(secret []byte, label string, ttl time.Duration) (user, pass string) {
	user = strconv.FormatInt(time.Now().Add(ttl).Unix(), 10) + ":" + label
	mac := hmac.New(sha1.New, secret)
	mac.Write([]byte(user))
	return user, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// client is one client connection's way back: a UDP address on the server's socket, or a stream.
type client struct {
	key    string
	addr   netip.AddrPort
	udp    net.PacketConn
	stream net.Conn
	wmu    sync.Mutex
	proto  string
}

func (c *client) send(b []byte) {
	if c.udp != nil {
		_, _ = c.udp.WriteTo(b, net.UDPAddrFromAddrPort(c.addr))
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.stream.Write(b)
}

type allocation struct {
	c       *client
	relay   *net.UDPConn
	key     []byte // the long-term key, for responses
	user    string
	expires time.Time
	perms   map[netip.Addr]time.Time
	chans   map[uint16]netip.AddrPort
	byPeer  map[netip.AddrPort]uint16
	chanExp map[uint16]time.Time
	alias   map[netip.Addr]netip.Addr // where a peer is reached -> the address the client named
	mu      sync.Mutex
	closed  bool
}

func (s *Server) init() {
	s.once.Do(func() {
		_, _ = rand.Read(s.nonceK[:])
		s.allocs = map[string]*allocation{}
		go s.sweep()
	})
}

// sweep closes allocations that were not refreshed.
func (s *Server) sweep() {
	for range time.Tick(30 * time.Second) {
		now := time.Now()
		s.mu.Lock()
		for k, a := range s.allocs {
			if now.After(a.expires) {
				delete(s.allocs, k)
				go a.close()
			}
		}
		s.mu.Unlock()
	}
}

func (a *allocation) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()
	a.relay.Close()
}

// Count is how many allocations are open, for the status page.
func (s *Server) Count() int {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.allocs)
}

// ServeUDP answers TURN on a UDP socket until it is closed.
func (s *Server) ServeUDP(pc net.PacketConn) error {
	s.init()
	buf := make([]byte, 65536)
	clients := map[netip.AddrPort]*client{}
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		ap := ua.AddrPort()
		c := clients[ap]
		if c == nil {
			if len(clients) > 10000 {
				clients = map[netip.AddrPort]*client{}
			}
			c = &client{key: "udp/" + ap.String(), addr: ap, udp: pc, proto: "udp"}
			clients[ap] = c
		}
		s.handle(c, append([]byte(nil), buf[:n]...))
	}
}

// ServeStream answers TURN on TCP or TLS connections until the listener is closed.
func (s *Server) ServeStream(ln net.Listener, proto string) error {
	s.init()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(conn, proto)
	}
}

func (s *Server) serveConn(conn net.Conn, proto string) {
	defer conn.Close()
	ap, _ := netip.ParseAddrPort(conn.RemoteAddr().String())
	c := &client{key: proto + "/" + conn.RemoteAddr().String(), addr: ap, stream: conn, proto: proto}
	defer s.drop(c.key)
	r := bufio.NewReader(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(maxLifetime + time.Minute))
		head, err := r.Peek(4)
		if err != nil {
			return
		}
		var size int
		if head[0]&0xC0 == 0x40 { // ChannelData, padded to four bytes over a stream
			size = 4 + (int(binary.BigEndian.Uint16(head[2:]))+3)&^3
		} else {
			if _, err := r.Peek(20); err != nil {
				return
			}
			h, _ := r.Peek(20)
			size = stun.Length(h)
		}
		if size > 65536+4 {
			return
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(r, b); err != nil {
			return
		}
		s.handle(c, b)
	}
}

func (s *Server) drop(key string) {
	s.mu.Lock()
	a := s.allocs[key]
	delete(s.allocs, key)
	s.mu.Unlock()
	if a != nil {
		a.close()
	}
}

func (s *Server) alloc(key string) *allocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allocs[key]
	if a != nil && time.Now().After(a.expires) {
		delete(s.allocs, key)
		go a.close()
		return nil
	}
	return a
}

func (s *Server) handle(c *client, b []byte) {
	if len(b) >= 4 && b[0]&0xC0 == 0x40 {
		s.channelData(c, b)
		return
	}
	m, err := stun.Parse(b)
	if err != nil {
		return
	}
	meth, cls := method(m.Type), class(m.Type)
	if cls == classIndication {
		if meth == methodSend {
			s.sendIndication(c, m)
		}
		return
	}
	if cls != classRequest {
		return
	}
	if meth == methodBinding {
		w := stun.NewBuilder(msgType(methodBinding, classSuccess), m.TxID)
		w.AddXORAddress(attrMapped, c.addr)
		w.Fingerprint()
		c.send(w.Bytes())
		return
	}
	key, user, code := s.authenticate(m)
	if code != 0 {
		s.reject(c, m, code, nil)
		return
	}
	switch meth {
	case methodAllocate:
		s.allocate(c, m, key, user)
	case methodRefresh:
		s.refresh(c, m, key)
	case methodCreatePerm:
		s.createPermission(c, m, key)
	case methodChanBind:
		s.channelBind(c, m, key)
	default:
		s.reject(c, m, 400, key)
	}
}

// nonce is stateless: a time and its MAC, good for an hour.
func (s *Server) nonce() string {
	t := strconv.FormatInt(time.Now().Unix(), 16)
	mac := hmac.New(sha1.New, s.nonceK[:])
	mac.Write([]byte(t))
	return t + "-" + hex.EncodeToString(mac.Sum(nil)[:8])
}

func (s *Server) nonceOK(n string) (valid, stale bool) {
	t, sig, ok := strings.Cut(n, "-")
	if !ok {
		return false, false
	}
	mac := hmac.New(sha1.New, s.nonceK[:])
	mac.Write([]byte(t))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)[:8]))) {
		return false, false
	}
	at, err := strconv.ParseInt(t, 16, 64)
	if err != nil {
		return false, false
	}
	return true, time.Since(time.Unix(at, 0)) > time.Hour
}

// authenticate checks the long-term credential: 401 asks for it, 438 for a fresh nonce.
func (s *Server) authenticate(m *stun.Message) (key []byte, user string, code int) {
	if !m.HasIntegrity() {
		return nil, "", 401
	}
	u, _ := m.Get(attrUsername)
	realm, _ := m.Get(attrRealm)
	n, _ := m.Get(attrNonce)
	user = string(u)
	if user == "" || string(realm) != s.Realm {
		return nil, "", 401
	}
	if ok, stale := s.nonceOK(string(n)); !ok {
		return nil, "", 401
	} else if stale {
		return nil, "", 438
	}
	exp, _, _ := strings.Cut(user, ":")
	if t, err := strconv.ParseInt(exp, 10, 64); err != nil || time.Now().Unix() > t {
		return nil, "", 401
	}
	mac := hmac.New(sha1.New, s.Secret())
	mac.Write(u)
	pass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	sum := md5.Sum([]byte(user + ":" + s.Realm + ":" + pass))
	if m.CheckKey(sum[:]) != nil {
		return nil, "", 401
	}
	return sum[:], user, 0
}

var reasons = map[int]string{400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 437: "Allocation Mismatch",
	438: "Stale Nonce", 442: "Unsupported Transport Protocol", 486: "Allocation Quota Reached", 508: "Insufficient Capacity"}

func (s *Server) reject(c *client, m *stun.Message, code int, key []byte) {
	w := stun.NewBuilder(msgType(method(m.Type), classError), m.TxID)
	ec := []byte{0, 0, byte(code / 100), byte(code % 100)}
	w.Add(attrErrorCode, append(ec, reasons[code]...))
	if code == 401 || code == 438 {
		w.Add(attrRealm, []byte(s.Realm))
		w.Add(attrNonce, []byte(s.nonce()))
	}
	if key != nil {
		w.Integrity(key)
	}
	w.Fingerprint()
	c.send(w.Bytes())
}

func (s *Server) success(c *client, m *stun.Message, key []byte, add func(*stun.Builder)) {
	w := stun.NewBuilder(msgType(method(m.Type), classSuccess), m.TxID)
	if add != nil {
		add(w)
	}
	w.Add(attrSoftware, []byte("BitVoice"))
	w.Integrity(key)
	w.Fingerprint()
	c.send(w.Bytes())
}

func lifetime(m *stun.Message, def time.Duration) time.Duration {
	v, ok := m.Get(attrLifetime)
	if !ok || len(v) != 4 {
		return def
	}
	d := time.Duration(binary.BigEndian.Uint32(v)) * time.Second
	if d > maxLifetime {
		d = maxLifetime
	}
	if d != 0 && d < defaultLifetime {
		d = defaultLifetime
	}
	return d
}

func secs(d time.Duration) []byte {
	return binary.BigEndian.AppendUint32(nil, uint32(d/time.Second))
}

func (s *Server) allocate(c *client, m *stun.Message, key []byte, user string) {
	if a := s.alloc(c.key); a != nil {
		s.reject(c, m, 437, key)
		return
	}
	if v, ok := m.Get(attrTransport); !ok || len(v) != 4 || v[0] != 17 {
		s.reject(c, m, 442, key) // UDP relays only, as WebRTC uses
		return
	}
	s.mu.Lock()
	perUser := 0
	for _, a := range s.allocs {
		if a.user == user {
			perUser++
		}
	}
	s.mu.Unlock()
	if perUser >= 20 {
		s.reject(c, m, 486, key)
		return
	}
	relay, err := s.openRelay()
	if err != nil {
		s.reject(c, m, 508, key)
		return
	}
	life := lifetime(m, defaultLifetime)
	a := &allocation{c: c, relay: relay, key: key, user: user, expires: time.Now().Add(life),
		perms: map[netip.Addr]time.Time{}, chans: map[uint16]netip.AddrPort{}, byPeer: map[netip.AddrPort]uint16{}, chanExp: map[uint16]time.Time{},
		alias: map[netip.Addr]netip.Addr{}}
	s.mu.Lock()
	s.allocs[c.key] = a
	s.mu.Unlock()
	go s.relayLoop(a)
	rp := relay.LocalAddr().(*net.UDPAddr).AddrPort()
	adv := netip.AddrPortFrom(s.Advertise, rp.Port())
	if !s.Advertise.IsValid() {
		adv = netip.AddrPortFrom(s.RelayIP, rp.Port())
	}
	if s.Log != nil {
		s.Log.Debug("TURN allocation", "client", c.key, "relay", adv.String(), "user", user)
	}
	s.success(c, m, key, func(w *stun.Builder) {
		w.AddXORAddress(attrRelayed, adv)
		w.AddXORAddress(attrMapped, c.addr)
		w.Add(attrLifetime, secs(life))
	})
}

// openRelay opens a UDP port in the range for an allocation.
func (s *Server) openRelay() (*net.UDPConn, error) {
	ip := s.RelayIP
	if !ip.IsValid() {
		ip = netip.IPv4Unspecified()
	}
	if s.PortMin == 0 || s.PortMax < s.PortMin {
		return net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 0)))
	}
	span := s.PortMax - s.PortMin + 1
	s.mu.Lock()
	start := s.next
	s.next = (s.next + 1) % span
	s.mu.Unlock()
	for i := 0; i < span; i++ {
		p := s.PortMin + (start+i)%span
		if c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, uint16(p)))); err == nil {
			return c, nil
		}
	}
	return nil, errors.New("no relay port free")
}

// relayLoop passes what peers send to the relay port back to the client: by channel when one is
// bound, else as a Data indication. Only peers with a permission get through.
func (s *Server) relayLoop(a *allocation) {
	buf := make([]byte, 65536)
	for {
		n, from, err := a.relay.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		peer := netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		now := time.Now()
		a.mu.Lock()
		if named, ok := a.alias[peer.Addr()]; ok {
			peer = netip.AddrPortFrom(named, peer.Port())
		}
		exp, ok := a.perms[peer.Addr()]
		ch := a.byPeer[peer]
		if ch != 0 && now.After(a.chanExp[ch]) {
			ch = 0
		}
		a.mu.Unlock()
		if !ok || now.After(exp) {
			continue
		}
		if ch != 0 {
			out := make([]byte, 4, 4+n+3)
			binary.BigEndian.PutUint16(out, ch)
			binary.BigEndian.PutUint16(out[2:], uint16(n))
			out = append(out, buf[:n]...)
			if a.c.stream != nil {
				for len(out)%4 != 0 {
					out = append(out, 0)
				}
			}
			a.c.send(out)
			continue
		}
		var tx [12]byte
		_, _ = rand.Read(tx[:])
		w := stun.NewBuilder(msgType(methodData, classIndication), tx)
		w.AddXORAddress(attrPeer, peer)
		w.Add(attrData, buf[:n])
		a.c.send(w.Bytes())
	}
}

func (s *Server) refresh(c *client, m *stun.Message, key []byte) {
	a := s.alloc(c.key)
	if a == nil {
		s.reject(c, m, 437, key)
		return
	}
	life := lifetime(m, defaultLifetime)
	if life == 0 {
		s.drop(c.key)
	} else {
		s.mu.Lock()
		a.expires = time.Now().Add(life)
		s.mu.Unlock()
	}
	s.success(c, m, key, func(w *stun.Builder) { w.Add(attrLifetime, secs(life)) })
}

// peers reads every XOR-PEER-ADDRESS and refuses any that is not this server's; it notes where each
// is really reached.
func (s *Server) peers(a *allocation, m *stun.Message) ([]netip.AddrPort, bool) {
	var out []netip.AddrPort
	for _, at := range m.Attrs {
		if at.Type != attrPeer {
			continue
		}
		one := &stun.Message{TxID: m.TxID, Attrs: []stun.Attr{at}}
		p, ok := one.XORAddress(attrPeer)
		if !ok {
			return nil, false
		}
		p = netip.AddrPortFrom(p.Addr().Unmap(), p.Port())
		if s.Route == nil {
			return nil, false
		}
		via, ok := s.Route(p.Addr())
		if !ok {
			return nil, false
		}
		if via != p.Addr() {
			a.mu.Lock()
			a.alias[via] = p.Addr()
			a.mu.Unlock()
		}
		out = append(out, p)
	}
	return out, len(out) > 0
}

func (s *Server) createPermission(c *client, m *stun.Message, key []byte) {
	a := s.alloc(c.key)
	if a == nil {
		s.reject(c, m, 437, key)
		return
	}
	ps, ok := s.peers(a, m)
	if !ok {
		s.reject(c, m, 403, key)
		return
	}
	a.mu.Lock()
	for _, p := range ps {
		a.perms[p.Addr()] = time.Now().Add(permLifetime)
	}
	a.mu.Unlock()
	s.success(c, m, key, nil)
}

func (s *Server) channelBind(c *client, m *stun.Message, key []byte) {
	a := s.alloc(c.key)
	if a == nil {
		s.reject(c, m, 437, key)
		return
	}
	v, ok := m.Get(attrChannel)
	ps, pok := s.peers(a, m)
	if !ok || len(v) != 4 || len(ps) != 1 {
		if !pok && ok {
			s.reject(c, m, 403, key)
			return
		}
		s.reject(c, m, 400, key)
		return
	}
	ch := binary.BigEndian.Uint16(v)
	if ch < 0x4000 || ch > 0x4FFF {
		s.reject(c, m, 400, key)
		return
	}
	p := ps[0]
	a.mu.Lock()
	if old, has := a.chans[ch]; has && old != p {
		a.mu.Unlock()
		s.reject(c, m, 400, key)
		return
	}
	if old := a.byPeer[p]; old != 0 && old != ch {
		a.mu.Unlock()
		s.reject(c, m, 400, key)
		return
	}
	a.chans[ch], a.byPeer[p], a.chanExp[ch] = p, ch, time.Now().Add(chanLifetime)
	a.perms[p.Addr()] = time.Now().Add(permLifetime)
	a.mu.Unlock()
	s.success(c, m, key, nil)
}

func (s *Server) sendIndication(c *client, m *stun.Message) {
	a := s.alloc(c.key)
	if a == nil {
		return
	}
	peer, ok := m.XORAddress(attrPeer)
	data, dok := m.Get(attrData)
	if !ok || !dok {
		return
	}
	peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
	a.mu.Lock()
	exp, has := a.perms[peer.Addr()]
	a.mu.Unlock()
	if !has || time.Now().After(exp) {
		return
	}
	_, _ = a.relay.WriteToUDPAddrPort(data, s.real(a, peer))
}

// real is where a peer the client named is reached.
func (s *Server) real(a *allocation, p netip.AddrPort) netip.AddrPort {
	a.mu.Lock()
	defer a.mu.Unlock()
	for via, named := range a.alias {
		if named == p.Addr() {
			return netip.AddrPortFrom(via, p.Port())
		}
	}
	return p
}

func (s *Server) channelData(c *client, b []byte) {
	a := s.alloc(c.key)
	if a == nil {
		return
	}
	ch := binary.BigEndian.Uint16(b)
	n := int(binary.BigEndian.Uint16(b[2:]))
	if 4+n > len(b) {
		return
	}
	a.mu.Lock()
	p, ok := a.chans[ch]
	live := ok && time.Now().Before(a.chanExp[ch])
	a.mu.Unlock()
	if !live {
		return
	}
	_, _ = a.relay.WriteToUDPAddrPort(b[4:4+n], s.real(a, p))
}
