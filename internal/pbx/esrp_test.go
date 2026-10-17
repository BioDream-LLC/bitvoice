package pbx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The ESRP: a 911 call from the ESInet is routed by its location to the PSAP the ECRF names; that
// PSAP's trunk fails, so the call overflows to the next PSAP, with the location still on it.
func TestESRPRoutesByLocation(t *testing.T) {
	esinet := newCarrier(t)
	psaps := newCarrier(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{
		{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: esinet.stack.Port(), ESInet: true, AllowFrom: []string{"127.0.0.1"}},
		{ID: "east", Name: "East PSAP", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port(), Username: "acct", Password: "wrong"},
		{ID: "west", Name: "West PSAP", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port(), Username: "acct", Password: "trunkpass"},
	}
	cfg.System.Core = config.Core{PSAPs: []config.CorePSAP{
		{URI: "sip:sos@east.example.gov", Name: "East County", Trunk: "east", Overflow: "sip:sos@west.example.gov"},
		{URI: "sip:sos@west.example.gov", Name: "West County", Trunk: "west"},
	}, Default: "sip:sos@west.example.gov"}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "esrp"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	asked := make(chan string, 4)
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.FindService = func(service string, loc *i3.Location) (string, string) {
			asked <- service
			if loc != nil && loc.HasPoint() && *loc.Lon > -76.9 {
				return "sip:sos@east.example.gov", ""
			}
			return "", "notFound"
		}
	})
	routes := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(routes)

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sd := sdp.New("127.0.0.1", 40022, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + string(sd) + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos.fire"}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag=e1")
	inv.Set("To", "<urn:service:sos.fire>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	tx := esinet.stack.Request(inv, pbx)
	select {
	case m := <-psaps.invites:
		if !strings.Contains(m.Get("Content-Type"), "multipart/mixed") || !strings.Contains(string(m.Body), "Harrisburg") || m.Get("Geolocation") == "" {
			t.Fatalf("the location did not go on: %s\n%s", m.Get("Content-Type"), m.Body)
		}
		if userOf(m.RequestURI) != "sos" {
			t.Fatalf("sent to %s", m.RequestURI)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no PSAP got the call")
	}
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("ESInet got %v", r)
	}
	if got := <-asked; got != "urn:service:sos.fire" {
		t.Fatalf("asked the ECRF for %q", got)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-routes.C:
			if r, ok := ev.Data.(Route); ok {
				if r.PSAP != "sip:sos@east.example.gov" || r.Why != "by location" {
					t.Fatalf("route %+v", r)
				}
				return
			}
		case <-deadline:
			t.Fatal("no route event")
		}
	}
}
