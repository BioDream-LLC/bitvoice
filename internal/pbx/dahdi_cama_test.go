package pbx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/mf"
)

// CAMA trunks from a 911 selective router on a T1's E&M channels: the seizure is winked at, the ANI
// spill (enhanced: II + 10 digits; traditional: the numbering plan digit + 7) is the callback number,
// the call is a 911 call, and its location is looked up by that number from the location server.
func TestTelephonyCardCAMA(t *testing.T) {
	dahdi.EMDigitWait, dahdi.WinkDelay = 600*time.Millisecond, 10*time.Millisecond
	asked := make(chan string, 4)
	lis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		asked <- string(b)
		io.WriteString(w, `<locationResponse xmlns="urn:ietf:params:xml:ns:geopriv:held">`+testPIDF+`</locationResponse>`)
	}))
	defer lis.Close()
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25397}
	cfg.System.PSAP.LocationServer = lis.URL
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "T1", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", DID: "911", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	fakes := map[int]*dahdi.Fake{1: dahdi.NewFake(), 2: dahdi.NewFake()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25397, Channels: []dahdi.ChannelConf{
		{Channel: 1, Kind: "em", Start: "cama"},
		{Channel: 2, Kind: "em", Start: "cama", NPD: []string{"570", "717"}}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	taker := registered(t, e, "101")[0]
	for _, tc := range []struct {
		ch         int
		spill, ani string
		info       string
	}{{1, "K007175550100S", "7175550100", "00"}, {2, "K15550100A", "7175550100", "1"}} {
		ch := fakes[tc.ch]
		ch.Raise(dahdi.EventRingOffHook)
		waitFor(t, "no wink", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookWink) })
		ch.Speak(ulawFrames(mf.R1Digits(tc.spill)))
		ring := taker.waitRing(4 * time.Second)
		if ring == nil {
			t.Fatalf("the CAMA call did not reach the call-taker (status %v)", b.Status())
		}
		if from := userOf(ring.Req.Get("From")); from != tc.ani {
			t.Fatalf("callback number %q", from)
		}
		select {
		case req := <-asked:
			if !strings.Contains(req, "<tel>"+tc.ani+"</tel>") {
				t.Fatalf("ALI asked %s", req)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no ALI lookup")
		}
		var em *Emergency
		waitFor(t, "no location from the ALI lookup", func() bool {
			for _, c := range e.Calls() {
				if c.Emergency != nil && c.Emergency.Location != nil {
					em = c.Emergency
					return true
				}
			}
			return false
		})
		if em.CAMA != tc.info || em.ALILookup != "found" || em.ESInet || !strings.HasPrefix(em.Location.Civic.String(), "123 Front St") {
			t.Fatalf("emergency %+v", em)
		}
		d := taker.answer(ring)
		waitFor(t, "no answer supervision", func() bool { return slices.Contains(ch.Hooks(), dahdi.HookOff) })
		taker.send(d.request("BYE"))
		waitFor(t, "the channel was not released", func() bool { return b.Status()[itoaInt(tc.ch)] == "idle" })
	}
}
