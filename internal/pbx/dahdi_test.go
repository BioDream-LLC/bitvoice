package pbx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A card with a phone on channel 1 (extension 100) and a phone company line on channel 2, joined by
// the bridge: the phone dials 101, which rings and is answered; the line rings in and reaches 101;
// 101 calls out on the line, which goes off hook and dials the number.
func TestTelephonyCard(t *testing.T) {
	dahdi.DigitTimeout = 500 * time.Millisecond
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25391,
		Channels: []config.CardChannel{{Channel: 1, Kind: "phone", Extension: "100"}, {Channel: 2, Kind: "line", Number: "2125550100"}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "Analogue lines", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Line 1", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	phonePort, linePort := dahdi.NewFake(), dahdi.NewFake()
	fakes := map[int]*dahdi.Fake{1: phonePort, 2: linePort}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25391, Channels: []dahdi.ChannelConf{
		{Channel: 1, Kind: "phone", Extension: "100", Secret: testSecret}, {Channel: 2, Kind: "line", Number: "2125550100"}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	waitFor(t, "the card's phone never registered", func() bool {
		for _, r := range e.Registrations() {
			if r.Extension == "100" && strings.Contains(r.UserAgent, "telephony card") {
				return b.Status()["1"] == "idle"
			}
		}
		return false
	})

	callee := registered(t, e, "101")[0]
	phonePort.Raise(dahdi.EventRingOffHook)
	waitFor(t, "no dial tone", func() bool { return slices.Contains(phonePort.Tones(), dahdi.ToneDial) })
	phonePort.Speak(dahdi.DTMF("101#"))
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the card's phone did not reach 101 (status %v)", b.Status())
	}
	cd := callee.answer(ring)
	waitFor(t, "the card's phone is not in the call", func() bool { return b.Status()["1"] == "in a call" })
	before := phonePort.Wrote()
	cd.sendRTP(20)
	waitFor(t, "101's audio did not reach the card's phone", func() bool { return phonePort.Wrote() > before })
	phonePort.Raise(dahdi.EventOnHook)
	callee.waitBye(t, 3*time.Second)
	waitFor(t, "the phone port is not idle", func() bool { return b.Status()["1"] == "idle" })

	// The line rings: 101 rings; when it answers, the line goes off hook.
	linePort.Raise(dahdi.EventRingBegin)
	ring = callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("a ringing line did not reach 101 (status %v)", b.Status())
	}
	cd = callee.answer(ring)
	waitFor(t, "the line did not go off hook", func() bool { return slices.Contains(linePort.Hooks(), dahdi.HookOff) && b.Status()["2"] == "in a call" })
	callee.send(cd.request("BYE"))
	waitFor(t, "the line did not hang up", func() bool { return b.Status()["2"] == "idle" && slices.Contains(linePort.Hooks(), dahdi.HookOn) })

	// 101 dials out on the line: off hook, the number dialled as tones, then answered.
	done := make(chan *dialog, 1)
	go func() { d, _ := callee.dial("92125550199"); done <- d }()
	var ad *dialog
	select {
	case ad = <-done:
	case <-time.After(8 * time.Second):
	}
	if ad == nil {
		t.Fatalf("the call out on the line was not answered (status %v, trunks %+v)", b.Status(), e.TrunkStatus())
	}
	var heard []byte
	var det dahdi.Detector
	for _, frame := range chunks(linePort.Sent(), 160) {
		pcm := make([]int16, len(frame))
		for i, v := range frame {
			pcm[i] = media.UlawToLinear(v)
		}
		heard = append(heard, det.Feed(pcm)...)
	}
	if !strings.Contains(string(heard), "2125550199") {
		t.Fatalf("the line was dialled %q", heard)
	}
	callee.send(ad.request("BYE"))
	waitFor(t, "the line did not hang up after the call out", func() bool { return b.Status()["2"] == "idle" })
}

func chunks(b []byte, n int) [][]byte {
	var out [][]byte
	for len(b) >= n {
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

// A T1 PRI on the card: the phone company (a network-side isdn.PRI) calls the DID 2125550100 from
// 3125550199, which reaches 101 with the caller ID; 101 answers and the network hears CONNECT; then
// 101 calls out, the network sees SETUP with the number and 101's caller ID, answers, and clears.
func TestTelephonyCardPRI(t *testing.T) {
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25392,
		Spans: []config.CardSpan{{Type: "t1", First: 1}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "PRI", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	cfg.Extensions[1].CallerID = "2125550101"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	bs, d := cfg.System.Cards.Spans[0].Channels()
	if len(bs) != 23 || d != 24 || bs[0] != 1 {
		t.Fatalf("T1 channels %v %d", bs, d)
	}
	e, _ := startEngine(t, cfg)
	bridgeEnd, farEnd := dahdi.NewFakeD()
	dahdi.OpenD = func(n int) (isdn.Link, error) {
		if n != 24 {
			return nil, errors.New("not the D-channel")
		}
		return bridgeEnd, nil
	}
	fakes := map[int]*dahdi.Fake{}
	var q []int
	for _, ch := range bs {
		fakes[ch] = dahdi.NewFake()
		q = append(q, ch)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25392,
		Spans: []dahdi.SpanConf{{D: d, B: bs, Q931: q}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	network := isdn.New(farEnd, isdn.Config{Network: true, Channels: q})
	incoming := make(chan *isdn.Call, 2)
	network.Incoming = func(c *isdn.Call) { incoming <- c }
	go network.Run()
	t.Cleanup(network.Close)
	waitFor(t, "the PRI did not come up", func() bool { return strings.HasPrefix(b.Status()["span1"], "D-channel up") })

	callee := registered(t, e, "101")[0]
	in, err := network.Dial("2125550100", "3125550199")
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
	waitEvent(t, in, "alerting")
	cd := callee.answer(ring)
	waitEvent(t, in, "connected")
	before := fakes[in.Channel].Wrote()
	cd.sendRTP(20)
	waitFor(t, "101's audio did not reach the B-channel", func() bool { return fakes[in.Channel].Wrote() > before })
	callee.send(cd.request("BYE"))
	waitEvent(t, in, "disconnected")
	waitEvent(t, in, "released")
	waitFor(t, "the B-channel is not free", func() bool { return len(network.Busy()) == 0 })

	// 101 calls out on the PRI.
	done := make(chan *dialog, 1)
	codes := make(chan int, 1)
	go func() { d, code := callee.dial("93125550199"); codes <- code; done <- d }()
	var out *isdn.Call
	select {
	case out = <-incoming:
	case <-time.After(4 * time.Second):
		t.Fatalf("no SETUP on the PRI (%v)", b.Status())
	}
	if out.Called != "3125550199" || out.Calling != "2125550101" {
		t.Fatalf("SETUP called %q calling %q", out.Called, out.Calling)
	}
	out.Alerting()
	time.Sleep(200 * time.Millisecond)
	out.Answer()
	if code := <-codes; code != 200 {
		t.Fatalf("the call out was answered with %d", code)
	}
	od := <-done
	out.Hangup(isdn.CauseNormal)
	callee.waitBye(t, 3*time.Second)
	_ = od
	// Busy at the far end: the caller hears busy (486).
	go func() { _, code := callee.dial("93125550100"); codes <- code }()
	out = <-incoming
	out.Hangup(isdn.CauseBusy)
	if code := <-codes; code != 486 {
		t.Fatalf("busy came back as %d", code)
	}
}

func waitEvent(t *testing.T, c *isdn.Call, kind string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.Events:
			if ev.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("no %s on the PRI", kind)
		}
	}
}

// PRI supplementary services through the engine: a caller's name and redirecting number arrive as
// the From display name and Diversion; 101's name and its forwarding go out in SETUP; and when 101
// transfers a PRI caller to an outside number on the same span, the call is handed to the phone
// company (Two B-Channel Transfer), which joins the two and clears both channels.
func TestTelephonyCardPRIServices(t *testing.T) {
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25393,
		Spans: []config.CardSpan{{Type: "t1", First: 1, NetworkTransfer: true}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "PRI", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	cfg.Extensions[1].CallerID = "2125550101"
	cfg.Extensions[1].Outbound = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.System.Cards.Spans[0].Switch != "ni2" {
		t.Fatal(cfg.System.Cards.Spans[0].Switch)
	}
	bs, d := cfg.System.Cards.Spans[0].Channels()
	e, _ := startEngine(t, cfg)
	bridgeEnd, farEnd := dahdi.NewFakeD()
	dahdi.OpenD = func(n int) (isdn.Link, error) { return bridgeEnd, nil }
	fakes := map[int]*dahdi.Fake{}
	for _, ch := range bs {
		fakes[ch] = dahdi.NewFake()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25393,
		Spans: []dahdi.SpanConf{{D: d, B: bs, Q931: bs, Switch: "ni2", NetworkTransfer: true}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	network := isdn.New(farEnd, isdn.Config{Network: true, Channels: bs})
	incoming := make(chan *isdn.Call, 4)
	network.Incoming = func(c *isdn.Call) { incoming <- c }
	joined := make(chan [2]*isdn.Call, 1)
	network.OnTransfer = func(a, c *isdn.Call) bool {
		joined <- [2]*isdn.Call{a, c}
		go func() { a.Hangup(isdn.CauseNormal); c.Hangup(isdn.CauseNormal) }()
		return true
	}
	go network.Run()
	t.Cleanup(network.Close)
	waitFor(t, "the PRI did not come up", func() bool { return strings.HasPrefix(b.Status()["span1"], "D-channel up") })

	callee := registered(t, e, "101")[0]
	in, err := network.DialWith(isdn.Setup{Called: "2125550100", Calling: "3125550199", Name: "Ann Smith", Redirecting: "3125550111", RedirectReason: isdn.RedirectBusy})
	if err != nil {
		t.Fatal(err)
	}
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatal("the DID did not reach 101")
	}
	if f := ring.Req.Get("From"); !strings.Contains(f, `"Ann Smith"`) || !strings.Contains(f, "3125550199") {
		t.Fatalf("From %s", f)
	}
	cd := callee.answer(ring)
	waitEvent(t, in, "connected")

	// 101 transfers the caller to an outside number on the same PRI.
	refer := cd.request(sip.REFER)
	refer.Set("Refer-To", "<sip:93125550177@127.0.0.1>")
	refer.Set("Referred-By", "<sip:101@127.0.0.1>")
	if r := callee.send(refer); r == nil || r.StatusCode != 202 {
		t.Fatalf("REFER: %v", r)
	}
	var out *isdn.Call
	select {
	case out = <-incoming:
	case <-time.After(4 * time.Second):
		t.Fatal("no SETUP for the transfer")
	}
	if out.Called != "3125550177" {
		t.Fatalf("called %q", out.Called)
	}
	out.Answer()
	select {
	case j := <-joined:
		if !(j[0] == in && j[1] == out || j[0] == out && j[1] == in) {
			t.Fatalf("the network joined %v and %v", j[0].Called, j[1].Called)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the transfer was not handed to the phone company")
	}
	waitFor(t, "the B-channels are not free", func() bool { return len(network.Busy()) == 0 })
	waitFor(t, "the engine's call did not end", func() bool { return len(e.Calls()) == 0 })

	// 101's own call out carries its name.
	go callee.dial("93125550199")
	out = <-incoming
	if out.Calling != "2125550101" || out.Name == "" {
		t.Fatalf("SETUP calling %q name %q", out.Calling, out.Name)
	}
	out.Hangup(isdn.CauseNormal)
	// 101 forwards every call outside: the PRI caller's call goes back out with 101 as the
	// redirecting number, reason unconditional.
	if err := e.SetDivert(Divert{Ext: "101", All: "93125550188"}); err != nil {
		t.Fatal(err)
	}
	if _, err := network.Dial("2125550100", "3125550199"); err != nil {
		t.Fatal(err)
	}
	select {
	case out = <-incoming:
	case <-time.After(4 * time.Second):
		t.Fatal("the forward did not go out")
	}
	if out.Called != "3125550188" || out.Redirecting != "2125550101" || out.RedirectReason != isdn.RedirectUnconditional {
		t.Fatalf("forward: called %q redirecting %q reason %d", out.Called, out.Redirecting, out.RedirectReason)
	}
	out.Hangup(isdn.CauseNormal)
}
