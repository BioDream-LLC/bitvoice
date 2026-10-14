package turn

import (
	"bufio"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/stun"
)

// tclient is a minimal TURN client over a datagram or stream connection.
type tclient struct {
	t      *testing.T
	conn   net.Conn
	stream bool
	r      *bufio.Reader
	user   string
	key    []byte
	realm  string
	nonce  string
}

func (c *tclient) write(b []byte) {
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *tclient) read() []byte {
	c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !c.stream {
		b := make([]byte, 65536)
		n, err := c.conn.Read(b)
		if err != nil {
			c.t.Fatal(err)
		}
		return b[:n]
	}
	h, err := c.r.Peek(4)
	if err != nil {
		c.t.Fatal(err)
	}
	size := 0
	if h[0]&0xC0 == 0x40 {
		size = 4 + (int(binary.BigEndian.Uint16(h[2:]))+3)&^3
	} else {
		hh, _ := c.r.Peek(20)
		size = stun.Length(hh)
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(c.r, b); err != nil {
		c.t.Fatal(err)
	}
	return b
}

func tx() [12]byte {
	var t [12]byte
	rand.Read(t[:])
	return t
}

// request sends a signed request (after the first 401 has given realm and nonce) and returns the answer.
func (c *tclient) request(meth uint16, add func(*stun.Builder)) *stun.Message {
	for range 2 {
		w := stun.NewBuilder(msgType(meth, classRequest), tx())
		if add != nil {
			add(w)
		}
		if c.nonce != "" {
			w.Add(attrUsername, []byte(c.user))
			w.Add(attrRealm, []byte(c.realm))
			w.Add(attrNonce, []byte(c.nonce))
			w.Integrity(c.key)
		}
		w.Fingerprint()
		c.write(w.Bytes())
		m, err := stun.Parse(c.read())
		if err != nil {
			c.t.Fatal(err)
		}
		if class(m.Type) == classError {
			v, _ := m.Get(attrErrorCode)
			if code := int(v[2])*100 + int(v[3]); code == 401 && c.nonce == "" {
				r, _ := m.Get(attrRealm)
				n, _ := m.Get(attrNonce)
				c.realm, c.nonce = string(r), string(n)
				continue
			}
		}
		return m
	}
	c.t.Fatal("never authenticated")
	return nil
}

func errCode(m *stun.Message) int {
	if class(m.Type) != classError {
		return 0
	}
	v, _ := m.Get(attrErrorCode)
	return int(v[2])*100 + int(v[3])
}

func newClient(t *testing.T, conn net.Conn, stream bool, user, pass, realm string) *tclient {
	sum := md5.Sum([]byte(user + ":" + realm + ":" + pass))
	return &tclient{t: t, conn: conn, stream: stream, r: bufio.NewReader(conn), user: user, key: sum[:]}
}

func startServer(t *testing.T) (*Server, net.PacketConn, net.Listener) {
	s := &Server{Realm: "bitvoice", Secret: func() []byte { return []byte("s3cret") },
		Route: func(a netip.Addr) (netip.Addr, bool) { return a, a.IsLoopback() }, RelayIP: netip.MustParseAddr("127.0.0.1")}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.ServeUDP(pc)
	go s.ServeStream(ln, "tcp")
	t.Cleanup(func() { pc.Close(); ln.Close() })
	return s, pc, ln
}

func transport(w *stun.Builder) { w.Add(attrTransport, []byte{17, 0, 0, 0}) }

// A client allocates (after a 401), is refused a peer that is not this server, gets a permission and
// talks to a peer by Send and Data indications and then over a bound channel, on UDP and on TCP.
func TestRelay(t *testing.T) {
	_, pc, ln := startServer(t)
	user, pass := Credentials([]byte("s3cret"), "ext-100", time.Hour)
	for _, kind := range []string{"udp", "tcp"} {
		t.Run(kind, func(t *testing.T) {
			var conn net.Conn
			var err error
			if kind == "udp" {
				conn, err = net.Dial("udp", pc.LocalAddr().String())
			} else {
				conn, err = net.Dial("tcp", ln.Addr().String())
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			c := newClient(t, conn, kind == "tcp", user, pass, "bitvoice")
			m := c.request(methodAllocate, transport)
			if class(m.Type) != classSuccess || m.CheckKey(c.key) != nil {
				t.Fatalf("allocate: %x code %d", m.Type, errCode(m))
			}
			relay, ok := m.XORAddress(attrRelayed)
			if !ok || relay.Addr().String() != "127.0.0.1" {
				t.Fatalf("relayed %v", relay)
			}
			peer, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			defer peer.Close()
			pa := peer.LocalAddr().(*net.UDPAddr).AddrPort()
			// Not this server: refused.
			if m := c.request(methodCreatePerm, func(w *stun.Builder) {
				w.AddXORAddress(attrPeer, netip.MustParseAddrPort("192.0.2.9:5000"))
			}); errCode(m) != 403 {
				t.Fatalf("outside peer allowed: %d", errCode(m))
			}
			// Before a permission, the peer is not heard.
			if m := c.request(methodCreatePerm, func(w *stun.Builder) { w.AddXORAddress(attrPeer, pa) }); errCode(m) != 0 {
				t.Fatalf("permission: %d", errCode(m))
			}
			w := stun.NewBuilder(msgType(methodSend, classIndication), tx())
			w.AddXORAddress(attrPeer, pa)
			w.Add(attrData, []byte("hello peer"))
			c.write(w.Bytes())
			buf := make([]byte, 1500)
			peer.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, from, err := peer.ReadFromUDPAddrPort(buf)
			if err != nil || string(buf[:n]) != "hello peer" || from.Port() != relay.Port() {
				t.Fatalf("peer got %q from %v (%v), relay %v", buf[:n], from, err, relay)
			}
			peer.WriteToUDPAddrPort([]byte("hello client"), from)
			d, err := stun.Parse(c.read())
			if err != nil || method(d.Type) != methodData {
				t.Fatalf("data indication %v", err)
			}
			if v, _ := d.Get(attrData); string(v) != "hello client" {
				t.Fatalf("data %q", v)
			}
			// A channel.
			if m := c.request(methodChanBind, func(w *stun.Builder) {
				w.Add(attrChannel, []byte{0x40, 0x01, 0, 0})
				w.AddXORAddress(attrPeer, pa)
			}); errCode(m) != 0 {
				t.Fatalf("channel bind %d", errCode(m))
			}
			cd := []byte{0x40, 0x01, 0, 3, 'a', 'b', 'c', 0}
			if kind == "udp" {
				cd = cd[:7]
			}
			c.write(cd)
			n, _, _ = peer.ReadFromUDPAddrPort(buf)
			if string(buf[:n]) != "abc" {
				t.Fatalf("channel to peer %q", buf[:n])
			}
			peer.WriteToUDPAddrPort([]byte("xyz12"), from)
			got := c.read()
			if got[0] != 0x40 || got[1] != 0x01 || string(got[4:9]) != "xyz12" {
				t.Fatalf("channel from peer % x", got)
			}
			// Refresh with lifetime 0 ends it.
			if m := c.request(methodRefresh, func(w *stun.Builder) { w.Add(attrLifetime, []byte{0, 0, 0, 0}) }); errCode(m) != 0 {
				t.Fatalf("refresh %d", errCode(m))
			}
		})
	}
}

// Wrong or expired credentials get 401; a second allocation on one 5-tuple 437; TCP relays 442.
func TestAuthAndErrors(t *testing.T) {
	_, pc, _ := startServer(t)
	conn, _ := net.Dial("udp", pc.LocalAddr().String())
	defer conn.Close()
	user, _ := Credentials([]byte("s3cret"), "x", time.Hour)
	c := newClient(t, conn, false, user, "wrong", "bitvoice")
	if m := c.request(methodAllocate, transport); errCode(m) != 401 {
		t.Fatalf("wrong password: %d", errCode(m))
	}
	old, pass := Credentials([]byte("s3cret"), "x", -time.Minute)
	c = newClient(t, conn, false, old, pass, "bitvoice")
	if m := c.request(methodAllocate, transport); errCode(m) != 401 {
		t.Fatalf("expired: %d", errCode(m))
	}
	user, pass = Credentials([]byte("s3cret"), "x", time.Hour)
	c = newClient(t, conn, false, user, pass, "bitvoice")
	if m := c.request(methodAllocate, func(w *stun.Builder) { w.Add(attrTransport, []byte{6, 0, 0, 0}) }); errCode(m) != 442 {
		t.Fatalf("TCP relay: %d", errCode(m))
	}
	if m := c.request(methodAllocate, transport); errCode(m) != 0 {
		t.Fatalf("allocate %d", errCode(m))
	}
	if m := c.request(methodAllocate, transport); errCode(m) != 437 {
		t.Fatalf("second allocation %d", errCode(m))
	}
	// Binding works without credentials, as plain STUN.
	w := stun.NewBuilder(msgType(methodBinding, classRequest), tx())
	c.write(w.Bytes())
	m, _ := stun.Parse(c.read())
	if a, ok := m.XORAddress(attrMapped); !ok || a.Addr().String() != "127.0.0.1" {
		t.Fatal("binding")
	}
}

func FuzzHandle(f *testing.F) {
	f.Add([]byte{0, 3, 0, 0, 0x21, 0x12, 0xA4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add([]byte{0x40, 0x01, 0, 3, 1, 2, 3})
	s := &Server{Realm: "r", Secret: func() []byte { return []byte("k") }, Route: func(a netip.Addr) (netip.Addr, bool) { return a, false }}
	s.init()
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	c := &client{key: "f", addr: netip.MustParseAddrPort("127.0.0.1:9"), udp: pc}
	f.Fuzz(func(t *testing.T, b []byte) { s.handle(c, b) })
}

// The server's public address (behind a NAT) is reached at a local one, and the client still sees
// the address it named.
func TestPublicAddressReachedLocally(t *testing.T) {
	public := netip.MustParseAddr("192.0.2.1")
	s := &Server{Realm: "bitvoice", Secret: func() []byte { return []byte("s3cret") }, RelayIP: netip.MustParseAddr("127.0.0.1"),
		Route: func(a netip.Addr) (netip.Addr, bool) {
			if a == public {
				return netip.MustParseAddr("127.0.0.1"), true
			}
			return a, false
		}}
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	go s.ServeUDP(pc)
	conn, _ := net.Dial("udp", pc.LocalAddr().String())
	defer conn.Close()
	user, pass := Credentials([]byte("s3cret"), "x", time.Hour)
	c := newClient(t, conn, false, user, pass, "bitvoice")
	if m := c.request(methodAllocate, transport); errCode(m) != 0 {
		t.Fatal(errCode(m))
	}
	peer, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer peer.Close()
	named := netip.AddrPortFrom(public, peer.LocalAddr().(*net.UDPAddr).AddrPort().Port())
	if m := c.request(methodCreatePerm, func(w *stun.Builder) { w.AddXORAddress(attrPeer, named) }); errCode(m) != 0 {
		t.Fatal(errCode(m))
	}
	w := stun.NewBuilder(msgType(methodSend, classIndication), tx())
	w.AddXORAddress(attrPeer, named)
	w.Add(attrData, []byte("ping"))
	c.write(w.Bytes())
	buf := make([]byte, 100)
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := peer.ReadFromUDPAddrPort(buf)
	if err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("%q %v", buf[:n], err)
	}
	peer.WriteToUDPAddrPort([]byte("pong"), from)
	d, _ := stun.Parse(c.read())
	if p, _ := d.XORAddress(attrPeer); p != named {
		t.Fatalf("client sees %v, named %v", p, named)
	}
}
