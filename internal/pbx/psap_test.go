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

const testPIDF = `<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gml="http://www.opengis.net/gml" xmlns:gs="http://www.opengis.net/pidflo/1.0"
 xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" entity="pres:caller@esinet.test">
 <tuple id="t"><status><gp:geopriv><gp:location-info>
 <ca:civicAddress><ca:country>US</ca:country><ca:A1>PA</ca:A1><ca:A3>Harrisburg</ca:A3><ca:HNO>123</ca:HNO><ca:RD>Front</ca:RD><ca:STS>St</ca:STS></ca:civicAddress>
 <gs:Circle><gml:pos>40.2633 -76.8823</gml:pos><gs:radius uom="urn:ogc:def:uom:EPSG::9001">25</gs:radius></gs:Circle>
 </gp:location-info><gp:method>GPS</gp:method></gp:geopriv></status></tuple></presence>`

// An NG911 call from the ESInet: a multipart INVITE with SDP and PIDF-LO. It rings the call-taker,
// carries the location on the call, and the answer is reported once, with the location.
func TestEmergencyCallFromESInet(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].ESInet = true
	cfg.Trunks[0].Register = false
	cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
	cfg.InboundRoutes[0].DID = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	answers := make(chan EmergencyAnswer, 2)
	e, _ := startEngine(t, cfg, func(e *Engine) { e.OnEmergencyAnswer = func(a EmergencyAnswer) { answers <- a } })
	taker := registered(t, e, "101")[0]

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sd := sdp.New("127.0.0.1", 40012, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + string(sd) + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag=sos1")
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Geolocation-Routing", "yes")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	tx := c.stack.Request(inv, pbx)
	ring := taker.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the 911 call did not reach the call-taker")
	}
	calls := e.Calls()
	if len(calls) != 1 || calls[0].Emergency == nil || calls[0].Emergency.Location == nil || !calls[0].Emergency.ESInet {
		t.Fatalf("calls %+v", calls)
	}
	if got := calls[0].Emergency.Location.Civic.String(); !strings.HasPrefix(got, "123 Front St") {
		t.Fatalf("address %q", got)
	}
	taker.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("ESInet got %v", r)
	}
	select {
	case a := <-answers:
		if a.Agent != "101" || len(a.PIDF) == 0 || a.Call.Emergency.Location.Radius != 25 {
			t.Fatalf("answer %+v", a)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the answer was not reported")
	}
	select {
	case <-answers:
		t.Fatal("reported twice")
	case <-time.After(300 * time.Millisecond):
	}
}
