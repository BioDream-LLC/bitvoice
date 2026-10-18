package pbx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Every call record says how the call was routed: the incoming route, the hours, and why it reached who it did.
func TestCallRecordSaysWhy(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	open := config.Destination{Type: "extension", Target: "102"}
	cfg.Extensions[2].NoAnswer = &config.Destination{Type: "extension", Target: "101"}
	cfg.Schedules = []config.Schedule{{ID: "s1", Name: "Office hours",
		Hours: []config.OpenHours{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "23:59"}}, Open: open, Closed: config.Destination{Type: "hangup"}}}
	cfg.InboundRoutes[0].Destination = config.Destination{Type: "schedule", Target: "s1"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0] // 102 has no phone: its no-answer destination, 101, rings

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	tx := c.stack.Request(inv, pbx)
	ring := b.waitRing(patient(2 * time.Second))
	if ring == nil {
		t.Fatal("the call never reached 101")
	}
	// Nobody answers 101 either: the call ends, and its record is written.
	if r := finalOf(tx); r == nil || r.StatusCode != 480 {
		t.Fatalf("carrier got %v", r)
	}
	r := rec.last(t)
	why := strings.Join(r.Why, " | ")
	for _, want := range []string{"The incoming route Main on Carrier took +12125550100.", "Office hours was open",
		"Ext 102 had no phone connected, so its no-answer destination took the call."} {
		if !strings.Contains(why, want) {
			t.Errorf("why %q lacks %q", why, want)
		}
	}
}

// A call that cannot be put through says why in its record.
func TestRefusedCallSaysWhy(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	if _, code := a.dial("600"); code != 480 {
		t.Fatalf("code %d", code)
	}
	r := rec.last(t)
	if !strings.Contains(strings.Join(r.Why, " "), "Nobody in Sales had a phone connected") {
		t.Fatalf("why %q", r.Why)
	}
}
