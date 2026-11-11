package pbx

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// peerInvite is a call another site's phone system sends this engine on a peer trunk, for a number.
func peerInvite(c *carrier, e *Engine, number, maxForwards string) *sip.ClientTx {
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + number + "@127.0.0.1"}
	inv.Set("From", `"Jane Smith" <sip:2101@texas.example>;tag=p1`)
	inv.Set("To", "<sip:"+number+"@127.0.0.1>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	if maxForwards != "" {
		inv.Set("Max-Forwards", maxForwards)
	}
	inv.Set("Contact", "<sip:texas@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40004, 5, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	return c.stack.Request(inv, pbx)
}

// peerConfig adds a trunk to another site (no registration, the address names it) to trunkConfig.
func peerConfig(t *testing.T, carrierPort, sitePort int, mode string) config.Config {
	t.Helper()
	cfg := trunkConfig(carrierPort)
	cfg.Trunks = append(cfg.Trunks, config.Trunk{ID: "tx", Name: "Texas", Enabled: true, Host: "127.0.0.1", Port: sitePort, Peer: mode})
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func waitTrunks(t *testing.T, e *Engine, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(patient(5 * time.Second))
	for {
		ok := true
		for _, s := range e.TrunkStatus() {
			if w, there := want[s.ID]; there && s.State != w {
				ok = false
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("trunks never %v: %+v", want, e.TrunkStatus())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPeerTrunkDialsExtensions(t *testing.T) {
	carrier, texas := newCarrier(t), newCarrier(t)
	e, rec := startEngine(t, peerConfig(t, carrier.stack.Port(), texas.stack.Port(), "internal"))
	waitTrunks(t, e, map[string]string{"t1": "registered", "tx": "reachable"})
	b := registered(t, e, "101")[0]

	tx := peerInvite(texas, e, "101", "")
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the other site's call for 101 did not ring it")
	}
	if from := userOf(ring.Req.Get("From")); from != "2101" || !strings.HasPrefix(ring.Req.Get("From"), `"Jane Smith"`) {
		t.Fatalf("caller shown as %s", ring.Req.Get("From"))
	}
	b.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("the other site got %v", r)
	}
	e.Hangup(e.Calls()[0].ID, "test")
	if r := rec.last(t); r.Direction != "inbound" || r.Trunk != "Texas" || !strings.Contains(strings.Join(r.Why, " "), "another of your phone systems") {
		t.Fatalf("call record %+v", r)
	}

	// Not a hub: an outside number is not passed on, and no incoming route takes it.
	if r := finalOf(peerInvite(texas, e, "2125551212", "")); r == nil || r.StatusCode != 404 {
		t.Fatalf("an outside number through an internal peer: %v", r)
	}
	select {
	case n := <-carrier.answered:
		t.Fatalf("the provider was called for %s", n)
	default:
	}
}

func TestPeerTrunkHub(t *testing.T) {
	carrier, texas := newCarrier(t), newCarrier(t)
	e, _ := startEngine(t, peerConfig(t, carrier.stack.Port(), texas.stack.Port(), "onward"))
	waitTrunks(t, e, map[string]string{"t1": "registered", "tx": "reachable"})

	tx := peerInvite(texas, e, "2125551212", "5")
	var inv *sip.Message
	select {
	case inv = <-carrier.invites:
	case <-time.After(patient(5 * time.Second)):
		t.Fatal("the hub did not pass the call on")
	}
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("the other site got %v", r)
	}
	if mf := inv.Get("Max-Forwards"); mf != "4" {
		t.Fatalf("Max-Forwards passed on as %q, want 4", mf)
	}
	if !strings.Contains(inv.Get("From"), "2101") {
		t.Fatalf("the caller's number was lost: %s", inv.Get("From"))
	}
	for _, c := range e.Calls() {
		e.Hangup(c.ID, "test")
	}

	// A loop ends: no hops left.
	if r := finalOf(peerInvite(texas, e, "2125551213", "1")); r == nil || r.StatusCode != 483 {
		t.Fatalf("a call with no hops left: %v", r)
	}
	// An emergency call is passed on even then.
	tx = peerInvite(texas, e, "911", "1")
	select {
	case inv = <-carrier.invites:
	case <-time.After(patient(5 * time.Second)):
		t.Fatal("the hub did not pass on the emergency call")
	}
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("emergency call through the hub: %v", r)
	}
}

func TestTrunkForSomeServers(t *testing.T) {
	carrier := newCarrier(t)
	cfg := trunkConfig(carrier.stack.Port())
	cfg.Trunks[0].Servers = []string{"a"}
	for _, n := range []string{"101", "102"} {
		cfg.FindExtension(n).Outbound = true
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	f := &fakeCluster{engines: map[string]*Engine{}}
	a, _ := startEngine(t, cfg, func(e *Engine) { e.Cluster = fakeLink{f, "a"} })
	b, _ := startEngine(t, cfg, func(e *Engine) { e.Cluster = fakeLink{f, "b"} })
	f.set("a", a)
	f.set("b", b)
	waitTrunk(t, a, "registered")
	waitTrunk(t, b, "standby")
	if ts := b.TrunkStatus(); !strings.Contains(ts[0].Error, "Server A only") {
		t.Fatalf("b's trunk %+v", ts)
	}

	// b's phone: the line is a's, so an ordinary call out has no way out here.
	p101 := registered(t, b, "101")[0]
	if _, code := p101.dial("2125550123"); code != 503 {
		t.Fatalf("call out on another server's line: %d", code)
	}
	// An emergency call does go out, through a.
	done := make(chan int, 1)
	go func() { _, code := p101.dial("911"); done <- code }()
	select {
	case n := <-carrier.answered:
		if n != "1911" {
			t.Fatalf("provider got %q", n)
		}
	case <-time.After(patient(5 * time.Second)):
		t.Fatal("the emergency call did not reach the provider through the other server")
	}
	if code := <-done; code != 200 {
		t.Fatalf("emergency call: %d", code)
	}
	for _, c := range b.Calls() {
		b.Hangup(c.ID, "test")
	}

	// a's phone uses its own line.
	p102 := registered(t, a, "102")[0]
	if _, code := p102.dial("2125550124"); code != 200 {
		t.Fatalf("call out on the server's own line: %d", code)
	}
}

// Two sites with lines from the same provider (one account each, one provider address): a call in
// counts as on this server's own line.
func TestSameProviderLinePerServer(t *testing.T) {
	carrier := newCarrier(t)
	cfg := trunkConfig(carrier.stack.Port())
	cfg.Trunks = []config.Trunk{
		{ID: "la", Name: "Line A", Enabled: true, Host: "127.0.0.1", Port: carrier.stack.Port(), Servers: []string{"a"}},
		{ID: "lb", Name: "Line B", Enabled: true, Host: "127.0.0.1", Port: carrier.stack.Port(), Servers: []string{"b"}}}
	cfg.OutboundRoutes[0].Trunks = []string{"la", "lb"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	f := &fakeCluster{engines: map[string]*Engine{}}
	b, recB := startEngine(t, cfg, func(e *Engine) { e.Cluster = fakeLink{f, "b"} })
	f.set("b", b)
	waitTrunks(t, b, map[string]string{"la": "standby", "lb": "reachable"})
	p101 := registered(t, b, "101")[0]

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(b.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:+12125550100@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(carrier.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40006, 7, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	tx := carrier.stack.Request(inv, pbx)
	ring := p101.waitRing(patient(2 * time.Second))
	if ring == nil {
		t.Fatalf("the call in never rang 101: %v", finalOf(tx))
	}
	p101.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("carrier got %v", r)
	}
	b.Hangup(b.Calls()[0].ID, "test")
	if r := recB.last(t); r.Trunk != "Line B" {
		t.Fatalf("the call in was put on %q, not this server's own line", r.Trunk)
	}
}

// The far end of a call moves (another server of its cluster took the call over): its re-INVITE comes
// from another address, and BitVoice's requests in that dialog (the hang-up) go there from then on.
func TestFarEndMovesByReinvite(t *testing.T) {
	c := newCarrier(t)
	e, _ := startEngine(t, trunkConfig(c.stack.Port()))
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40008, 9, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	tx := c.stack.Request(inv, pbx)
	ring := b.waitRing(patient(2 * time.Second))
	if ring == nil {
		t.Fatal("the call in never rang 101")
	}
	d := b.answer(ring)
	ok := finalOf(tx)
	if ok == nil || ok.StatusCode != 200 {
		t.Fatalf("carrier got %v", ok)
	}

	// The server that took the call over: another address.
	byes := make(chan *sip.Message, 2)
	taker := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		if tx == nil {
			return
		}
		if in.Msg.Method == sip.BYE {
			byes <- in.Msg
		}
		tx.Respond(sip.NewResponse(in.Msg, 200, ""))
	})
	taker.Host = "127.0.0.1"
	if err := taker.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	taker.Serve(ctx)
	t.Cleanup(func() { cancel(); taker.Close() })
	ct, _ := sip.ParseAddress(ok.Get("Contact"))
	re := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: ct.URI.String()}
	re.Set("From", inv.Get("From"))
	re.Set("To", ok.Get("To"))
	re.Set("Call-ID", inv.CallID())
	re.Set("CSeq", "1002 INVITE")
	re.Set("Contact", "<sip:taker@127.0.0.1:"+strconv.Itoa(taker.Port())+">")
	re.Set("Content-Type", "application/sdp")
	re.Body = sdp.New("127.0.0.1", 40010, 10, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	if r := finalOf(taker.Request(re, pbx)); r == nil || r.StatusCode != 200 || len(r.Body) == 0 {
		t.Fatalf("re-INVITE from the new address: %v", r)
	}

	b.send(d.request(sip.BYE))
	select {
	case <-byes:
	case <-time.After(patient(2 * time.Second)):
		t.Fatal("the hang-up went to the old address, not to the server that took the call over")
	}
}
