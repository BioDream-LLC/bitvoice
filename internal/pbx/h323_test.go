package pbx

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/h323"
)

func h323Service(t *testing.T, e *Engine) *h323.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := h323.NewService(ctx, "127.0.0.1:0", e.LocalSIP, h323.NewToken(), nil)
	t.Cleanup(func() { s.Close(); cancel() })
	e.SetIAX(s)
	return s
}

func toH323(c config.Config) []h323.Trunk {
	var out []h323.Trunk
	for _, t := range c.Trunks {
		if t.Protocol == "h323" {
			out = append(out, h323.Trunk{ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, Codecs: t.Codecs})
		}
	}
	return out
}

// Two BitVoice servers joined by an H.323 trunk (Fast Connect, G.711): a call from one rings a phone
// on the other, audio flows both ways, a busy callee is reported as busy, and hanging up ends both.
func TestH323TrunkBetweenServers(t *testing.T) {
	bcfg := testConfig()
	bcfg.Trunks = []config.Trunk{{ID: "a", Name: "Site A", Enabled: true, Protocol: "h323", Host: "127.0.0.1"}}
	bcfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}},
		{Name: "Busy", DID: "2125550199", Destination: config.Destination{Type: "hangup"}}}
	if err := bcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := startEngine(t, bcfg)
	sb := h323Service(t, b)
	sb.Apply(toH323(bcfg))
	if sb.Port() == 0 {
		t.Fatal("H.323 is not listening")
	}

	acfg := testConfig()
	acfg.Trunks = []config.Trunk{{ID: "b", Name: "Site B", Enabled: true, Protocol: "h323", Host: "127.0.0.1", Port: sb.Port(), Codecs: []string{"PCMA"}}}
	acfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"b"}}}
	if err := acfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a, _ := startEngine(t, acfg)
	sa := h323Service(t, a)
	sa.Apply(toH323(acfg))

	caller := registered(t, a, "100")[0]
	callee := registered(t, b, "101")[0]
	done := make(chan *dialog, 1)
	go func() { d, _ := caller.dial("2125550100"); done <- d }()
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the call over H.323 did not ring 101 on the other server (trunks: %+v)", a.TrunkStatus())
	}
	cd := callee.answer(ring)
	var ad *dialog
	select {
	case ad = <-done:
	case <-time.After(15 * time.Second):
	}
	if ad == nil {
		t.Fatal("the caller was not answered")
	}
	callee.drainRTP()
	ad.sendRTP(20)
	if !callee.gotRTP(2 * time.Second) {
		t.Fatal("no audio across the H.323 trunk to the callee")
	}
	caller.drainRTP()
	cd.sendRTP(20)
	if !caller.gotRTP(2 * time.Second) {
		t.Fatal("no audio across the H.323 trunk back to the caller")
	}
	caller.send(ad.request("BYE"))
	waitFor(t, "the far server's call did not end", func() bool { return len(b.Calls()) == 0 })

	// A number the far server refuses: the caller is told, not left ringing.
	codes := make(chan int, 1)
	go func() { _, code := caller.dial("2125550199"); codes <- code }()
	select {
	case code := <-codes:
		if code < 400 {
			t.Fatalf("a refused call got %d", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("a refused call was never answered")
	}
	if st := a.TrunkStatus(); st[0].State != "ready" {
		t.Errorf("trunk status %+v", st)
	}
}

// Another vendor's Setup (encoded by an independent ASN.1 encoder, proposing G.729, G.723.1 and both
// G.711 laws) rings the extension it routes to, and BitVoice answers in G.711 by Fast Connect.
func TestH323SetupFromAnotherEncoder(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "g", Name: "Old gateway", Enabled: true, Protocol: "h323", Host: "127.0.0.1"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "All", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	s := h323Service(t, e)
	s.Apply(toH323(cfg))
	callee := registered(t, e, "101")[0]
	raw, err := os.ReadFile("../h323/testdata/setup.hex")
	if err != nil {
		t.Fatal(err)
	}
	uu, _ := hex.DecodeString(strings.TrimSpace(string(raw)))
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(s.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write(h323.RawSetup(0x55, uu))
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if typ, _, err := h323.ReadMessage(conn); err != nil || typ != h323.MsgCallProceeding {
		t.Fatalf("first answer %x %v", typ, err)
	}
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatal("the H.323 call did not ring 101")
	}
	if c := e.Calls()[0]; c.From != "4105551234" || c.FromName != "Front Desk" || c.Legs[0].Codec != "PCMA" {
		t.Errorf("caller %q %q, codec %s", c.From, c.FromName, c.Legs[0].Codec)
	}
	callee.answer(ring)
	for {
		typ, fast, err := h323.ReadMessage(conn)
		if err != nil {
			t.Fatal("no Connect:", err)
		}
		if typ == h323.MsgConnect || typ == h323.MsgAlerting && fast > 0 {
			if fast != 2 {
				t.Fatalf("Fast Connect answer has %d channels", fast)
			}
			break
		}
	}
}
