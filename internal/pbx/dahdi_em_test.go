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
	"github.com/biodream-llc/bitvoice/internal/mf"
)

func ulawFrames(pcm []int16) [][]byte {
	var out [][]byte
	for i := 0; i < len(pcm); i += 160 {
		f := make([]byte, 160)
		for j := range f {
			v := int16(0)
			if i+j < len(pcm) {
				v = pcm[i+j]
			}
			f[j] = media.LinearToUlaw(v)
		}
		out = append(out, f)
	}
	return out
}

func heardMF(b []byte) string {
	rx := mf.NewR1Receiver()
	pcm := make([]int16, len(b))
	for i, v := range b {
		pcm[i] = media.UlawToLinear(v)
	}
	return rx.Feed(pcm)
}

// E&M channels of a T1 on the card: a wink-start DTMF channel, a wink-start MF channel and a
// Feature Group D channel. Seizures are winked at, the number dialled reaches the inbound routes (with
// the ANI as caller ID on Feature Group D), answering goes off hook; a call out seizes, waits for the
// wink, sends the number in MF, and is answered when the far end goes off hook.
func TestTelephonyCardEM(t *testing.T) {
	dahdi.EMDigitWait, dahdi.WinkDelay = 600*time.Millisecond, 10*time.Millisecond
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25394}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "T1", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "DID", DID: "5550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	fakes := map[int]*dahdi.Fake{1: dahdi.NewFake(), 2: dahdi.NewFake(), 3: dahdi.NewFake()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25394, Channels: []dahdi.ChannelConf{
		{Channel: 1, Kind: "em", Start: "wink", Address: "dtmf"},
		{Channel: 2, Kind: "em", Start: "wink", Address: "mf"},
		{Channel: 3, Kind: "em", Start: "fgd"}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	callee := registered(t, e, "101")[0]

	// Wink start, DTMF.
	ch := fakes[1]
	ch.Raise(dahdi.EventRingOffHook)
	waitFor(t, "no wink", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookWink) })
	ch.Speak(dahdi.DTMF("5550100"))
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the E&M call did not reach 101 (status %v)", b.Status())
	}
	d := callee.answer(ring)
	waitFor(t, "no answer supervision", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookOff) && b.Status()["1"] == "in a call" })
	callee.send(d.request("BYE"))
	waitFor(t, "the channel was not released", func() bool { return b.Status()["1"] == "idle" && ch.Hooks()[len(ch.Hooks())-1] == dahdi.HookOn })

	// Feature Group D: the ANI is the caller ID.
	ch = fakes[3]
	ch.Raise(dahdi.EventRingOffHook)
	waitFor(t, "no wink on the Feature Group D channel", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookWink) })
	ch.Speak(ulawFrames(mf.R1Digits("K003125559999SK5550100S")))
	ring = callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the Feature Group D call did not reach 101 (status %v)", b.Status())
	}
	if from := userOf(ring.Req.Get("From")); from != "3125559999" {
		t.Fatalf("ANI shown as %q", from)
	}
	ch.Raise(dahdi.EventOnHook) // the caller gives up
	waitFor(t, "the Feature Group D channel is not idle", func() bool { return b.Status()["3"] == "idle" })

	// A call out: channel 1 is idle and taken first; make it busy so channel 2 (MF) is used.
	ch1 := fakes[1]
	ch1.Raise(dahdi.EventRingOffHook)
	waitFor(t, "channel 1 not taken", func() bool { return b.Status()["1"] == "dialling" })
	ch = fakes[2]
	done := make(chan *dialog, 1)
	go func() { d, _ := callee.dial("92125550199"); done <- d }()
	waitFor(t, "the MF channel was not seized", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookOff) })
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(heardMF(ch.Sent()), "2") {
		t.Fatal("digits sent before the wink")
	}
	ch.Raise(dahdi.EventWinkFlash)
	waitFor(t, "the number was not sent in MF", func() bool { return heardMF(ch.Sent()) == "K2125550199S" })
	ch.Raise(dahdi.EventRingOffHook) // answered
	var ad *dialog
	select {
	case ad = <-done:
	case <-time.After(15 * time.Second):
	}
	if ad == nil {
		t.Fatalf("the call out was not answered (status %v)", b.Status())
	}
	ch.Raise(dahdi.EventOnHook) // the far end hangs up
	callee.waitBye(t, 3*time.Second)
	waitFor(t, "the MF channel is not idle", func() bool { return b.Status()["2"] == "idle" })
}
