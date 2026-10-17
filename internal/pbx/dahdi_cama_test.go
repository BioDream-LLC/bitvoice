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
	"github.com/biodream-llc/bitvoice/internal/i3"
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
		case <-time.After(15 * time.Second):
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

// A CAMA call whose location comes from the legacy ALI link (no HELD server): the record's address,
// the caller's name and the class of service.
func TestCAMAByALILink(t *testing.T) {
	dahdi.EMDigitWait, dahdi.WinkDelay = 600*time.Millisecond, 10*time.Millisecond
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25398}
	cfg.System.PSAP.ALILink = config.ALILink{Address: "127.0.0.1:9", Layout: []config.ALIField{{Name: "street", Line: 1, Col: 1}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "T1", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", DID: "911", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	asked := make(chan string, 2)
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.ALIQuery = func(_ context.Context, number, _ string) (i3.ALIRecord, error) {
			asked <- number
			return i3.ParseALI("\x02(717) 555-0100\r\nRESD\r\nJOHNSON PAT\r\n123 FRONT ST\r\nHARRISBURG          PA\r\n\x03",
				[]i3.ALIField{{Name: "class", Line: 2, Col: 1}, {Name: "name", Line: 3, Col: 1}, {Name: "street", Line: 4, Col: 1}, {Name: "city", Line: 5, Col: 1, Len: 20}, {Name: "state", Line: 5, Col: 21}})
		}
	})
	fake := dahdi.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25398, Channels: []dahdi.ChannelConf{{Channel: 1, Kind: "em", Start: "cama"}}},
		func(int, bool) (dahdi.Channel, error) { return fake, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	taker := registered(t, e, "101")[0]
	fake.Raise(dahdi.EventRingOffHook)
	waitFor(t, "no wink", func() bool { return slices.Contains(fake.Hooks(), dahdi.HookWink) })
	fake.Speak(ulawFrames(mf.R1Digits("K007175550100S")))
	if taker.waitRing(4*time.Second) == nil {
		t.Fatal("the call did not ring")
	}
	if n := <-asked; n != "7175550100" {
		t.Fatalf("asked for %q", n)
	}
	waitFor(t, "no location from the ALI link", func() bool {
		for _, c := range e.Calls() {
			if em := c.Emergency; em != nil && em.Location != nil && em.ALIClass == "RESD" && c.FromName == "JOHNSON PAT" && em.Location.Civic.Street == "FRONT" {
				return true
			}
		}
		return false
	})
}
