package pbx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The BCF checks a signed call's STIR/SHAKEN signature at the ESInet edge: a bad one is let through
// marked suspicious (never refused), logged; an unsigned call is left alone.
func TestBCFVerifiesIdentity(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].ESInet = true
	cfg.Trunks[0].Register = false
	cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
	cfg.InboundRoutes[0].DID = ""
	cfg.System.Core.BCF.Enabled = true
	cfg.System.Core.BCF.VerifyIdentity = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	taker := registered(t, e, "101")[0]
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	call := func(identity string) *CallInfo {
		inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
		inv.Set("From", "<sip:+17175550100@esinet.test>;tag=b"+strconv.Itoa(len(identity)))
		inv.Set("To", "<urn:service:sos>")
		inv.Set("Call-ID", sip.NewCallID())
		inv.Set("CSeq", "1 INVITE")
		inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
		if identity != "" {
			inv.Set("Identity", identity)
		}
		inv.Set("Content-Type", "application/sdp")
		inv.Body = sdp.New("127.0.0.1", 40020, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
		tx := c.stack.Request(inv, pbx)
		ring := taker.waitRing(2 * time.Second)
		if ring == nil {
			t.Fatal("the 911 call did not ring: a bad signature must never stop one")
		}
		calls := e.Calls()
		go func() {
			for range tx.Responses {
			}
		}()
		// A 911 call is never refused, a busy call-taker's included: the stand-in ESInet gives up instead.
		hangupAll(e)
		waitNoCalls(t, e)
		for i := range calls {
			if calls[i].Emergency != nil {
				return &calls[i]
			}
		}
		t.Fatal("no 911 call")
		return nil
	}
	call("")
	if log := e.BCFLog(); len(log) != 0 {
		t.Fatalf("an unsigned call was logged: %+v", log)
	}
	call("eyJhbGciOiJFUzI1NiJ9.eyJ9.c2ln;info=<https://127.0.0.1:1/x.pem>;alg=ES256;ppt=shaken")
	log := e.BCFLog()
	if len(log) != 1 || log[0].Action != "marked" || !strings.Contains(log[0].Why, "signature failed") {
		t.Fatalf("log %+v", log)
	}
}
