package pbx

import (
	"context"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
)

// Two MFC-R2 circuits of the card looped back to back: 100 calls out on the first (forward: seize,
// the number and its own number in compelled MF tones), the second hears the call (backward), calls
// the engine for the number dialled, which rings 101 with 100's number; 101 answers, both circuits
// answer, and 100 hanging up clears both back to idle.
func TestTelephonyCardR2(t *testing.T) {
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25395}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "E1", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "DID", DID: "5550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	a, b := dahdi.NewFake(), dahdi.NewFake()
	b.SetRxBits(0x9)
	a.SetRxBits(0x9)
	dahdi.Patch(a, b)
	fakes := map[int]*dahdi.Fake{1: a, 2: b}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	br, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25395, Channels: []dahdi.ChannelConf{
		{Channel: 1, Kind: "r2", Variant: "itu"}, {Channel: 2, Kind: "r2", Variant: "itu"}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(br.Close)
	caller := registered(t, e, "100")[0]
	callee := registered(t, e, "101")[0]
	waitFor(t, "the R2 circuits are not idle", func() bool { return br.Status()["1"] == "idle" && br.Status()["2"] == "idle" })
	done := make(chan *dialog, 1)
	go func() { d, _ := caller.dial("95550100"); done <- d }()
	ring := callee.waitRing(15 * time.Second)
	if ring == nil {
		t.Fatalf("the R2 call did not reach 101 (status %v)", br.Status())
	}
	if from := userOf(ring.Req.Get("From")); from != "100" {
		t.Fatalf("caller shown as %q", from)
	}
	callee.answer(ring)
	var d *dialog
	select {
	case d = <-done:
	case <-time.After(10 * time.Second):
	}
	if d == nil {
		t.Fatalf("the call out was not answered (status %v)", br.Status())
	}
	waitFor(t, "both circuits are not in the call", func() bool {
		s := br.Status()
		return s["1"] == "in a call" && s["2"] == "in a call"
	})
	caller.send(d.request("BYE"))
	callee.waitBye(t, 5*time.Second)
	waitFor(t, "the circuits did not go idle", func() bool {
		s := br.Status()
		return s["1"] == "idle" && s["2"] == "idle"
	})
}
