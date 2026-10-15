package pbx

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A trunk's advice of charge (3GPP TS 24.647 XML, as the card bridge sends a PRI's): a figure in an
// INFO during the call and the whole charge in the BYE; the call's record keeps the last, and its cost
// is that figure when it is in the system's currency.
func TestTrunkAdviceOfCharge(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.System.Costs.Currency = "EUR"
	e, rec := startEngine(t, cfg)
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
	inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	tx := c.stack.Request(inv, pbx)
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the call did not ring")
	}
	b.answer(ring)
	fin := finalOf(tx)
	if fin == nil || fin.StatusCode != 200 {
		t.Fatalf("carrier got %v", fin)
	}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: "sip:" + pbx.Addr.String()}
	ack.Set("From", inv.Get("From"))
	ack.Set("To", fin.Get("To"))
	ack.Set("Call-ID", inv.CallID())
	ack.Set("CSeq", "1 ACK")
	_ = c.stack.Stateless(ack, pbx)
	send := func(method string, seq int, ch isdn.Charge) {
		m := &sip.Message{IsRequest: true, Method: method, RequestURI: "sip:" + pbx.Addr.String()}
		m.Set("From", inv.Get("From"))
		m.Set("To", fin.Get("To"))
		m.Set("Call-ID", inv.CallID())
		m.Set("CSeq", strconv.Itoa(seq)+" "+method)
		m.Set("Content-Type", isdn.ChargeContentType)
		m.Body = isdn.ChargeXML(ch)
		if r := finalOf(c.stack.Request(m, pbx)); r == nil || r.StatusCode != 200 {
			t.Fatalf("%s: %v", method, r)
		}
	}
	send(sip.INFO, 2, isdn.Charge{Kind: "currency", Currency: "EUR", Amount: 40, Multiplier: 1})
	send(sip.BYE, 3, isdn.Charge{Final: true, Kind: "currency", Currency: "EUR", Amount: 125, Multiplier: 1})
	r := rec.last(t)
	if r.Charged != "EUR 1.25" || r.Cost != 1.25 || r.Rate != "Charged by the phone company" {
		t.Fatalf("record %+v", r)
	}
}
