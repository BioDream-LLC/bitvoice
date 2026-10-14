package pbx

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/media"
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
