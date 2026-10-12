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

// Screening refuses blocked and (when so set) withheld numbers, labels calls whose caller ID failed
// verification, never touches allowed numbers, and refuses nothing within an hour of an emergency call.
func TestScreening(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.System.Screening = config.Screening{Failed: "label", Anonymous: "block",
		Blocked: []config.ScreenNumber{{Number: "+1 900*"}, {Number: "3125559999"}},
		Allowed: []config.ScreenNumber{{Number: "19005550100"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	invite := func(from string) *sip.ClientTx {
		inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
		inv.Set("From", from+";tag=s"+sip.NewTag())
		inv.Set("To", "<sip:+12125550100@carrier.test>")
		inv.Set("Call-ID", sip.NewCallID())
		inv.Set("CSeq", "1 INVITE")
		inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
		inv.Set("Content-Type", "application/sdp")
		inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
		return c.stack.Request(inv, pbx)
	}
	refused := func(from string) {
		t.Helper()
		if r := finalOf(invite(from)); r == nil || r.StatusCode != 603 {
			t.Fatalf("%s: %v, want 603", from, r)
		}
	}
	refused("<sip:3125559999@carrier.test>")
	if r := rec.last(t); r.Disposition != "blocked" || r.Screened != "blocked" || !strings.Contains(r.Cause, "block list") {
		t.Fatalf("blocked record %+v", r)
	}
	refused("<sip:19005551234@carrier.test>")     // a prefix entry
	refused(`"Anonymous" <sip:anonymous@x.test>`) // withheld
	// An allowed number in a blocked range rings.
	invite("<sip:19005550100@carrier.test>")
	if ring := b.waitRing(2 * time.Second); ring == nil {
		t.Fatal("the allowed number did not ring")
	} else {
		b.send(sip.NewResponse(ring.Req, 486, "Busy Here"))
	}
	// A caller ID that failed verification is labelled, with its verdict on the call.
	invite(`"Prize Dept" <sip:+13125550142;verstat=TN-Validation-Failed@carrier.test>`)
	ring := b.waitRing(2 * time.Second)
	if ring == nil || !strings.HasPrefix(ring.Req.Get("From"), `"Spam? Prize Dept"`) {
		t.Fatalf("not labelled: %v", ring)
	}
	var got *CallInfo
	for _, ci := range e.Calls() {
		if ci.From == "+13125550142" {
			got = &ci
		}
	}
	if got == nil || got.Screen == nil || got.Screen.Verified != "failed" {
		t.Fatalf("no verdict on the call (or the number kept its parameters): %+v", got)
	}
	b.send(sip.NewResponse(ring.Req, 486, "Busy Here"))
	// Within an hour of an emergency call, a blocked number is labelled, not refused.
	e.lastEmergency.Store(time.Now().Unix())
	invite("<sip:3125559999@carrier.test>")
	if ring := b.waitRing(2 * time.Second); ring == nil {
		t.Fatal("a possible emergency call back was refused")
	}
}
