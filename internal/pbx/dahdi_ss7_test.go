package pbx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/ss7"
)

// An E1 run as SS7 on the card: the carrier's switch (a BitVoice ss7.SS7 with the point codes the
// other way round) calls the DID on a circuit, which reaches 101 with the caller ID; 101 answers and
// the switch hears ANM; 101 hangs up; then 101 calls out, and the switch gets an IAM with the
// number and 101's caller ID, answers and releases.
func TestTelephonyCardSS7(t *testing.T) {
	ss7.T4E, ss7.Fill = 200*time.Millisecond, 10*time.Millisecond
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25396, ALaw: true,
		Spans: []config.CardSpan{{Type: "e1", First: 1, Signalling: "ss7", OPC: "1-10-2", APC: "1-10-1", FirstCIC: 33}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "SS7", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	cfg.Extensions[1].CallerID = "2125550101"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	sp := cfg.System.Cards.Spans[0]
	if sp.Variant != "itu" || sp.NI != "national" {
		t.Fatalf("defaults %+v", sp)
	}
	opc, _ := config.PointCode(sp.OPC, false)
	apc, _ := config.PointCode(sp.APC, false)
	if opc != 1<<11|10<<3|2 {
		t.Fatalf("point code %d", opc)
	}
	bs, d := sp.Channels()
	e, _ := startEngine(t, cfg)
	bridgeEnd, farEnd := dahdi.NewFakeD()
	dahdi.OpenD = func(n int) (isdn.Link, error) {
		if n != d {
			return nil, errors.New("not the signalling link")
		}
		return bridgeEnd, nil
	}
	fakes := map[int]*dahdi.Fake{}
	var cics []int
	for i, ch := range bs {
		fakes[ch] = dahdi.NewFake()
		cics = append(cics, 33+i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25396, ALaw: true,
		Spans: []dahdi.SpanConf{{D: d, B: bs, SS7: &dahdi.SS7Conf{OPC: opc, APC: apc, NI: 2, FirstCIC: 33}}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sw := ss7.New(farEnd, ss7.Config{MTPConfig: ss7.MTPConfig{OPC: apc, APC: opc, NI: 2, Emergency: true}, CICs: cics})
	incoming := make(chan *ss7.Call, 2)
	sw.Incoming = func(c *ss7.Call) { incoming <- c }
	go sw.Run()
	t.Cleanup(sw.Close)
	waitFor(t, "the SS7 link set did not come up", func() bool { return strings.HasPrefix(b.Status()["span1"], "SS7 link set up") })

	callee := registered(t, e, "101")[0]
	in, err := sw.Dial(ss7.Setup{Called: "2125550100", Calling: "3125550199"})
	if err != nil {
		t.Fatal(err)
	}
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the DID did not reach 101 (%v)", b.Status())
	}
	if !strings.Contains(ring.Req.Get("From"), "3125550199") {
		t.Fatalf("caller ID lost: %s", ring.Req.Get("From"))
	}
	waitSS7(t, in, "alerting")
	cd := callee.answer(ring)
	waitSS7(t, in, "connected")
	ch := bs[in.CIC-33]
	before := fakes[ch].Wrote()
	cd.sendRTP(20)
	waitFor(t, "101's audio did not reach the circuit", func() bool { return fakes[ch].Wrote() > before })
	callee.send(cd.request("BYE"))
	waitSS7(t, in, "released")

	codes := make(chan int, 1)
	go func() { _, code := callee.dial("93125550199"); codes <- code }()
	var out *ss7.Call
	select {
	case out = <-incoming:
	case <-time.After(4 * time.Second):
		t.Fatalf("no IAM (%v)", b.Status())
	}
	if out.Called != "3125550199" || out.Calling != "2125550101" {
		t.Fatalf("IAM called %q calling %q", out.Called, out.Calling)
	}
	out.Alerting()
	time.Sleep(100 * time.Millisecond)
	out.Answer()
	if code := <-codes; code != 200 {
		t.Fatalf("the call out was answered with %d", code)
	}
	out.Hangup(ss7.CauseNormal)
	callee.waitBye(t, 3*time.Second)
	waitFor(t, "the circuits are not free", func() bool { return len(sw.Busy()) == 0 })
}

func waitSS7(t *testing.T, c *ss7.Call, kind string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events:
			if e.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("no %s on CIC %d", kind, c.CIC)
		}
	}
}
