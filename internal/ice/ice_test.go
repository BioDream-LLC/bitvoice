package ice

import (
	"bytes"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/turn"
)

// side is one agent on its own UDP socket, with a reader feeding it and collecting media.
type side struct {
	conn   *net.UDPConn
	agent  *Agent
	media  chan []byte
	relay  *Pair // set once a relayed pair is chosen: media goes as ChannelData
	ufrag  string
	pwd    string
	closed chan struct{}
}

func newSide(t *testing.T, controlling bool, stunSrv []netip.AddrPort, turnSrv []TURNServer, relayOnly bool, hosts bool) *side {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	s := &side{conn: conn, media: make(chan []byte, 16), ufrag: "u" + strconv.Itoa(port), pwd: "a-long-ice-password-" + strconv.Itoa(port), closed: make(chan struct{})}
	cfg := Config{Port: port, STUN: stunSrv, TURN: turnSrv, RelayOnly: relayOnly, Controlling: controlling,
		LocalUfrag: s.ufrag, LocalPwd: s.pwd, GatherTimeout: 2 * time.Second,
		Send: func(pkt []byte, to netip.AddrPort) { conn.WriteToUDPAddrPort(pkt, to) }}
	if hosts {
		cfg.Hosts = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	}
	s.agent = New(cfg)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				close(s.closed)
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
			consumed, inner, innerFrom := s.agent.Handle(pkt, from)
			if inner != nil {
				if bytes.HasPrefix(inner, []byte("media:")) {
					s.media <- inner
				} else {
					s.agent.HandleRelayed(inner, innerFrom, from)
				}
				continue
			}
			if !consumed && bytes.HasPrefix(pkt, []byte("media:")) {
				s.media <- pkt
			}
		}
	}()
	t.Cleanup(func() { s.agent.Close(); conn.Close() })
	return s
}

// send sends media to the far end on the chosen pair.
func (s *side) send(p Pair, payload []byte) {
	if p.Channel != 0 {
		s.conn.WriteToUDPAddrPort(ChannelData(p.Channel, payload), p.Relay)
		return
	}
	s.conn.WriteToUDPAddrPort(payload, p.Remote)
}

func connect(t *testing.T, a, b *side) (Pair, Pair) {
	t.Helper()
	ca, cb := a.agent.Gather(), b.agent.Gather()
	a.agent.SetRemote(b.ufrag, b.pwd, cb)
	b.agent.SetRemote(a.ufrag, a.pwd, ca)
	a.agent.Start()
	b.agent.Start()
	var pa, pb Pair
	for pa.Remote == (netip.AddrPort{}) || pb.Remote == (netip.AddrPort{}) {
		select {
		case pa = <-a.agent.Selected():
		case pb = <-b.agent.Selected():
		case <-time.After(10 * time.Second):
			t.Fatalf("no pair chosen: a %+v, b %+v; candidates %v / %v", pa, pb, ca, cb)
		}
	}
	return pa, pb
}

func exchange(t *testing.T, a, b *side, pa, pb Pair) {
	t.Helper()
	a.send(pa, []byte("media:a"))
	b.send(pb, []byte("media:b"))
	for _, s := range []*side{a, b} {
		select {
		case <-s.media:
		case <-time.After(5 * time.Second):
			t.Fatal("media did not cross the chosen pair")
		}
	}
}

func TestCandidateSDP(t *testing.T) {
	c := Candidate{Foundation: "1", Priority: priority(ServerReflexive, 65535), Addr: netip.MustParseAddrPort("203.0.113.5:40000"), Type: ServerReflexive,
		Related: netip.MustParseAddrPort("10.0.0.2:40000")}
	got, err := ParseCandidate("candidate:" + c.SDP())
	if err != nil || got.Addr != c.Addr || got.Type != c.Type || got.Priority != c.Priority {
		t.Fatalf("%+v %v (%s)", got, err, c.SDP())
	}
	// Chrome's candidates: host, an mDNS name (kept, to be resolved), TCP (refused), component 2 (refused).
	for v, ok := range map[string]bool{
		"candidate:842163049 1 udp 1677729535 198.51.100.7 61221 typ srflx raddr 0.0.0.0 rport 0 generation 0 ufrag x network-cost 999": true,
		"candidate:1 1 udp 2122260223 4f3a5b9c-1111-2222-3333-444455556666.local 54321 typ host":                                        true,
		"candidate:2 1 tcp 1518280447 192.0.2.1 9 typ host tcptype active":                                                              false,
		"candidate:3 2 udp 2122260222 192.0.2.1 54322 typ host":                                                                         false,
	} {
		if _, err := ParseCandidate(v); (err == nil) != ok {
			t.Errorf("%q: %v", v, err)
		}
	}
	if priority(Host, 65535) <= priority(ServerReflexive, 65535) || priority(ServerReflexive, 65535) <= priority(Relayed, 65535) {
		t.Fatal("type preferences out of order")
	}
}

// Two full agents with host candidates: checks both ways, the controlling one nominates, both choose.
func TestDirect(t *testing.T) {
	a := newSide(t, true, nil, nil, false, true)
	b := newSide(t, false, nil, nil, false, true)
	pa, pb := connect(t, a, b)
	if pa.Local.Type != Host || pb.Local.Type != Host || pa.Remote.Port() != uint16(b.conn.LocalAddr().(*net.UDPAddr).Port) {
		t.Fatalf("pairs %+v %+v", pa, pb)
	}
	exchange(t, a, b, pa, pb)
}

// Both claim to be controlling: the role conflict is resolved (487) and they still connect.
func TestRoleConflict(t *testing.T) {
	a := newSide(t, true, nil, nil, false, true)
	b := newSide(t, true, nil, nil, false, true)
	pa, pb := connect(t, a, b)
	if a.agent.Controlling() == b.agent.Controlling() {
		t.Fatal("the conflict was not resolved")
	}
	exchange(t, a, b, pa, pb)
}

// A side with no host candidate (a server behind NAT that only knows its STUN mapping): the far end
// learns it as peer-reflexive from its checks, and they connect.
func TestPeerReflexive(t *testing.T) {
	a := newSide(t, true, nil, nil, false, true)
	b := newSide(t, false, nil, nil, false, false)
	pa, pb := connect(t, a, b)
	exchange(t, a, b, pa, pb)
}

// STUN gives a server-reflexive candidate (BitVoice's own TURN server answers Binding).
func TestServerReflexive(t *testing.T) {
	srv := startTURN(t)
	a := newSide(t, true, []netip.AddrPort{srv}, nil, false, false)
	cands := a.agent.Gather()
	found := false
	for _, c := range cands {
		found = found || c.Type == ServerReflexive && c.Addr.Port() == uint16(a.conn.LocalAddr().(*net.UDPAddr).Port)
	}
	if !found {
		t.Fatalf("no server-reflexive candidate: %v", cands)
	}
}

func startTURN(t *testing.T) netip.AddrPort {
	t.Helper()
	s := &turn.Server{Realm: "bitvoice", Secret: func() []byte { return []byte("s3cret") },
		Route: func(a netip.Addr) (netip.Addr, bool) { return a, a.IsLoopback() }, RelayIP: netip.MustParseAddr("127.0.0.1")}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.ServeUDP(pc)
	t.Cleanup(func() { pc.Close() })
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

// Relay only, through BitVoice's own TURN server: checks go as Send and Data indications, the chosen
// pair gets a channel, and media goes as ChannelData.
func TestRelayOnly(t *testing.T) {
	srv := startTURN(t)
	user, pass := turn.Credentials([]byte("s3cret"), "ice-test", time.Hour)
	a := newSide(t, true, nil, []TURNServer{{Addr: srv, Username: user, Password: pass}}, true, false)
	b := newSide(t, false, nil, nil, false, true)
	pa, pb := connect(t, a, b)
	if pa.Local.Type != Relayed || pa.Channel == 0 || pa.Relay != srv {
		t.Fatalf("a did not choose its relay: %+v", pa)
	}
	exchange(t, a, b, pa, pb)
	// Wrong credentials: no relayed candidate, and gathering still ends.
	c := newSide(t, true, nil, []TURNServer{{Addr: srv, Username: user, Password: "wrong"}}, true, false)
	if cands := c.agent.Gather(); len(cands) != 0 {
		t.Fatalf("a relay with wrong credentials: %v", cands)
	}
}

// Against coturn, the reference TURN server (BITVOICE_TEST_COTURN: the path of turnserver).
func TestRelayWithCoturn(t *testing.T) {
	bin := os.Getenv("BITVOICE_TEST_COTURN")
	if bin == "" {
		t.Skip("set BITVOICE_TEST_COTURN to coturn's turnserver")
	}
	l, _ := net.ListenPacket("udp", "127.0.0.1:0")
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "-n", "--listening-ip=127.0.0.1", "--relay-ip=127.0.0.1", "--listening-port="+strconv.Itoa(port),
		"--min-port=49160", "--max-port=49200", "--lt-cred-mech", "--user=bv:a-long-turn-password", "--realm=example.test",
		"--no-tls", "--no-tcp", "--allow-loopback-peers", "--no-cli", "--simple-log", "--log-file=stdout")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	time.Sleep(500 * time.Millisecond)
	srv := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(port))
	a := newSide(t, true, []netip.AddrPort{srv}, []TURNServer{{Addr: srv, Username: "bv", Password: "a-long-turn-password"}}, true, false)
	b := newSide(t, false, nil, nil, false, true)
	pa, pb := connect(t, a, b)
	if pa.Local.Type != Relayed || pa.Channel == 0 {
		t.Fatalf("a did not choose its coturn relay: %+v", pa)
	}
	exchange(t, a, b, pa, pb)
}

// A DNS response carrying an A record (compressed name) is read; others are not.
func TestMDNSAnswer(t *testing.T) {
	name := "4f3a5b9c-1111-2222-3333-444455556666.local"
	// Header: ID 0, response+AA, 0 questions, 1 answer; the answer's name in full, then a second
	// record that points back to it (compression).
	b := []byte{0, 0, 0x84, 0, 0, 0, 0, 2, 0, 0, 0, 0}
	nameAt := len(b)
	for _, l := range []string{"4f3a5b9c-1111-2222-3333-444455556666", "local"} {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 16, 0, 1, 0, 0, 0, 120, 0, 1, 'x')                              // a TXT record first
	b = append(b, 0xC0, byte(nameAt), 0, 1, 0x80, 1, 0, 0, 0, 120, 0, 4, 192, 0, 2, 33) // then the A record
	if ip, ok := answerA(b, 0x1234, name); !ok || ip != netip.MustParseAddr("192.0.2.33") {
		t.Fatalf("%v %v", ip, ok)
	}
	if _, ok := answerA(b, 0x1234, "other.local"); ok {
		t.Fatal("an answer for another name was taken")
	}
	if _, ok := answerA(b[:20], 0x1234, name); ok {
		t.Fatal("a truncated answer was taken")
	}
}
