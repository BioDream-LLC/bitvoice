package mgcp

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

func TestParse(t *testing.T) {
	in := "NTFY 2001 aaln/1@gw1.example.net MGCP 1.0\r\nX: 0123\r\nO: L/hd\r\n"
	ms, err := Parse([]byte(in))
	if err != nil || len(ms) != 1 || ms[0].Verb != "NTFY" || ms[0].Endpoint != "aaln/1@gw1.example.net" || ms[0].Get("o") != "L/hd" {
		t.Fatalf("%+v %v", ms, err)
	}
	r := "200 1203 OK\r\nI: FDE234C8\r\n\r\nv=0\r\nc=IN IP4 10.0.0.9\r\nm=audio 3456 RTP/AVP 0\r\n.\r\n200 1204 OK\r\n"
	ms, err = Parse([]byte(r))
	if err != nil || len(ms) != 2 || ms[0].Code != 200 || ms[0].Get("I") != "FDE234C8" || !strings.Contains(string(ms[0].SDP), "m=audio 3456") || ms[1].TxID != "1204" {
		t.Fatalf("piggybacked responses %+v %v", ms, err)
	}
	if got := strings.Join(Events("L/hd, D/1,D/2 ,3, L/oc(N), D/T"), " "); got != "hd 1 2 3 oc t" {
		t.Fatal(got)
	}
	for _, junk := range []string{"", "hello", "NTFY x aaln MGCP", "999999 1 x"} {
		if _, err := Parse([]byte(junk)); err == nil {
			t.Errorf("%q parsed", junk)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("NTFY 2001 aaln/1@gw MGCP 1.0\r\nO: L/hd\r\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if ms, err := Parse(b); err == nil {
			for _, m := range ms {
				m.Bytes()
			}
		}
	})
}

type nopRecorder struct{}

func (nopRecorder) AddCDR(store.CDR) error { return nil }

// fakeGateway is a residential gateway with two analogue ports: it answers every command, keeps
// what it was asked, and reports events the test makes up.
type fakeGateway struct {
	t     *testing.T
	conn  *net.UDPConn
	agent netip.AddrPort
	got   chan *Message
	seen  []*Message
	tx    int
	rtp   map[string]*net.UDPConn // a socket per port, standing for its audio
}

func newFakeGateway(t *testing.T) *fakeGateway {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGateway{t: t, conn: c, got: make(chan *Message, 64), rtp: map[string]*net.UDPConn{}}
	for _, ep := range []string{"aaln/1", "aaln/2"} {
		r, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		g.rtp[ep] = r
		t.Cleanup(func() { r.Close() })
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 8192)
		for {
			n, from, err := c.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			ms, err := Parse(buf[:n])
			if err != nil {
				continue
			}
			for _, m := range ms {
				if m.Verb == "" {
					continue
				}
				g.agent = from
				r := &Message{Code: 200, TxID: m.TxID}
				if m.Verb == "CRCX" {
					local, _, _ := strings.Cut(m.Endpoint, "@")
					r.Set("I", "C"+m.TxID)
					s := sdp.New("127.0.0.1", g.rtp[local].LocalAddr().(*net.UDPAddr).Port, 1, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101)
					r.SDP = s.Bytes()
				}
				if m.Verb == "DLCX" {
					r.Code = 250
				}
				_, _ = c.WriteToUDPAddrPort(r.Bytes(), from)
				g.got <- m
			}
		}
	}()
	return g
}

func (g *fakeGateway) send(agentPort int, verb, ep string, params ...string) {
	g.tx++
	m := &Message{Verb: verb, TxID: strconv.Itoa(90000 + g.tx), Endpoint: ep + "@gw1.test"}
	for i := 0; i+1 < len(params); i += 2 {
		m.Set(params[i], params[i+1])
	}
	_, _ = g.conn.WriteToUDPAddrPort(m.Bytes(), netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(agentPort)))
}

// wait reads commands until one matches.
func (g *fakeGateway) wait(what string, match func(*Message) bool) *Message {
	g.t.Helper()
	for i, m := range g.seen {
		if match(m) {
			g.seen = append(g.seen[:i], g.seen[i+1:]...)
			return m
		}
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case m := <-g.got:
			if match(m) {
				return m
			}
			g.seen = append(g.seen, m)
		case <-deadline:
			g.t.Fatalf("the gateway was never sent %s", what)
			return nil
		}
	}
}

func isRQNT(ep, contains string) func(*Message) bool {
	return func(m *Message) bool {
		return m.Verb == "RQNT" && strings.HasPrefix(m.Endpoint, ep+"@") && strings.Contains(m.Get("R")+" S:"+m.Get("S"), contains)
	}
}

// Port 1 (extension 100) lifts its handset, gets dial tone, dials 101; port 2 (extension 101) rings
// with caller ID and answers; audio flows from port 1 to port 2; port 1 hangs up and port 2 is
// released.
func TestCallBetweenTwoPorts(t *testing.T) {
	cfg := config.Default()
	cfg.System.PublicAddress = "127.0.0.1"
	cfg.System.RTPMin, cfg.System.RTPMax = 34000, 35000
	for _, n := range []string{"100", "101"} {
		cfg.Extensions = append(cfg.Extensions, config.Extension{Number: n, Name: "Port " + n, Secret: "a long enough secret 1", Enabled: true, Outbound: true, RingSeconds: 10})
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := pbx.New(cfg, events.New(), nopRecorder{}, nil)
	if err := e.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wp, err := webphone.New(ctx, e.SIPPort(), e)
	if err != nil {
		t.Fatal(err)
	}
	gw := newFakeGateway(t)
	a := New(ctx, wp, e, func(string) string { return "a long enough secret 1" }, nil)
	a.Retry = 100 * time.Millisecond
	t.Cleanup(a.Close)
	gwPort := gw.conn.LocalAddr().(*net.UDPAddr).Port
	a.Apply(config.MGCP{Enabled: true, Port: 0, Gateways: []config.MGCPGateway{{Name: "gw1.test", Host: "127.0.0.1", Port: gwPort,
		Lines: []config.MGCPLine{{Endpoint: "aaln/1", Extension: "100"}, {Endpoint: "aaln/2", Extension: "101"}}}}})
	_ = a.Port()
	gw.wait("an off-hook watch on port 1", isRQNT("aaln/1", "L/hd"))
	gw.wait("an off-hook watch on port 2", isRQNT("aaln/2", "L/hd"))

	gw.send(a.Port(), "NTFY", "aaln/1", "X", "1", "O", "L/hd")
	m := gw.wait("dial tone", isRQNT("aaln/1", "S:L/dl"))
	if m.Get("D") == "" || !strings.Contains(m.Get("R"), "D/") {
		t.Fatalf("no digit map: %+v", m.Params)
	}
	gw.send(a.Port(), "NTFY", "aaln/1", "X", m.Get("X"), "O", "D/1,D/0,D/1,D/T")
	gw.wait("a connection for port 1", func(m *Message) bool {
		return m.Verb == "CRCX" && strings.HasPrefix(m.Endpoint, "aaln/1@") && len(m.SDP) > 0
	})
	ring := gw.wait("ringing on port 2", isRQNT("aaln/2", "S:L/rg"))
	if !strings.Contains(ring.Get("S"), `"100"`) {
		t.Errorf("no caller ID in %q", ring.Get("S"))
	}
	gw.send(a.Port(), "NTFY", "aaln/2", "X", ring.Get("X"), "O", "L/hd")
	crcx := gw.wait("a connection for port 2", func(m *Message) bool { return m.Verb == "CRCX" && strings.HasPrefix(m.Endpoint, "aaln/2@") })
	ours, err := sdp.Parse(crcx.SDP)
	if err != nil || ours.Audio() == nil {
		t.Fatal("CRCX without our SDP")
	}
	// Audio from port 1 reaches port 2.
	var ep1 *media.Endpoint
	for end := time.Now().Add(15 * time.Second); ep1 == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("port 1 has no audio endpoint")
		}
		a.mu.Lock()
		l1 := a.gateways["gw1.test"].lines["aaln/1"]
		a.mu.Unlock()
		l1.mu.Lock()
		ep1 = l1.rtp
		l1.mu.Unlock()
	}
	pkt := make([]byte, 172)
	pkt[0], pkt[1] = 0x80, 0
	got := false
	buf := make([]byte, 2048)
	to := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(ep1.Port()))
	for i := 0; i < 50 && !got; i++ {
		pkt[3] = byte(i)
		pkt[7] = byte(i * 160)
		for j := 12; j < len(pkt); j++ {
			pkt[j] = byte(0x10 + i)
		}
		_, _ = gw.rtp["aaln/1"].WriteToUDPAddrPort(pkt, to)
		_ = gw.rtp["aaln/2"].SetReadDeadline(time.Now().Add(40 * time.Millisecond))
		if n, _, err := gw.rtp["aaln/2"].ReadFromUDPAddrPort(buf); err == nil && n > 12 {
			got = true
		}
	}
	if !got {
		t.Fatal("no audio from port 1 at port 2")
	}
	gw.send(a.Port(), "NTFY", "aaln/1", "X", "9", "O", "L/hu")
	gw.wait("the connection of port 1 deleted", func(m *Message) bool { return m.Verb == "DLCX" && strings.HasPrefix(m.Endpoint, "aaln/1@") })
	gw.wait("the connection of port 2 deleted", func(m *Message) bool { return m.Verb == "DLCX" && strings.HasPrefix(m.Endpoint, "aaln/2@") })
	gw.wait("reorder tone on port 2", isRQNT("aaln/2", "S:L/ro"))
	if s := a.Status(); s["aaln/1@gw1.test"] != "ready" {
		t.Errorf("status %v", s)
	}
}
