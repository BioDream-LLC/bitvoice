package pbx

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// policyLab is an ESInet trunk in, two PSAPs out (East and West, told apart by the number the call
// is sent to), and an ECRF that always answers East.
func policyLab(t *testing.T, core func(*config.Core)) (*Engine, *carrier, *carrier) {
	esinet, psaps := newCarrier(t), newCarrier(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{
		{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: esinet.stack.Port(), ESInet: true, AllowFrom: []string{"127.0.0.1"}},
		{ID: "out", Name: "PSAPs", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port(), Username: "acct", Password: "trunkpass"},
	}
	cfg.System.Core = config.Core{PSAPs: []config.CorePSAP{
		{URI: "sip:east@east.example.gov", Name: "East", Trunk: "out"},
		{URI: "sip:west@west.example.gov", Name: "West", Trunk: "out"},
	}, Default: "sip:west@west.example.gov"}
	core(&cfg.System.Core)
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "esrp"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.FindService = func(string, *i3.Location) (string, string) { return "sip:east@east.example.gov", "" }
	})
	return e, esinet, psaps
}

// call911 sends an emergency call for a service from the ESInet; it returns the PSAP number it
// reached ("" when none) and the final answer the ESInet got.
func call911(t *testing.T, e *Engine, esinet, psaps *carrier, service string) (string, int) {
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: service}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag="+sip.NewTag())
	inv.Set("To", "<"+service+">")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
	sd := sdp.New("127.0.0.1", 40024, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte("--B\r\nContent-Type: application/sdp\r\n\r\n" + string(sd) + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n")
	tx := esinet.stack.Request(inv, pbx)
	got := ""
	select {
	case m := <-psaps.invites:
		got = userOf(m.RequestURI)
	case r := <-finalCh(tx):
		if r != nil {
			return "", r.StatusCode
		}
		return "", 0
	case <-time.After(5 * time.Second):
	}
	r := finalOf(tx)
	if r == nil {
		return got, 0
	}
	return got, r.StatusCode
}

func finalCh(tx *sip.ClientTx) chan *sip.Message {
	ch := make(chan *sip.Message, 1)
	go func() { ch <- finalOf(tx) }()
	return ch
}

func TestPolicyRuleByService(t *testing.T) {
	e, esinet, psaps := policyLab(t, func(c *config.Core) {
		c.Rules = []config.CoreRule{{Name: "Fire to West", Services: []string{"urn:service:sos.fire"}, Action: "route", To: "sip:west@west.example.gov"}}
	})
	if to, _ := call911(t, e, esinet, psaps, "urn:service:sos.police"); to != "east" {
		t.Fatalf("police call went to %q, want east (the ECRF's answer)", to)
	}
	if to, _ := call911(t, e, esinet, psaps, "urn:service:sos.fire"); to != "west" {
		t.Fatalf("fire call went to %q, want west (the rule)", to)
	}
}

// The BCF: a third call in a minute from one address is refused; with marking on, it is let
// through as suspicious and a rule for suspicious calls refuses it instead.
func TestBCFLimitsAndMarks(t *testing.T) {
	e, esinet, psaps := policyLab(t, func(c *config.Core) { c.BCF = config.BCF{Enabled: true, PerMinute: 2} })
	for i := 0; i < 2; i++ {
		if to, _ := call911(t, e, esinet, psaps, "urn:service:sos"); to != "east" {
			t.Fatalf("call %d went to %q", i+1, to)
		}
	}
	if to, code := call911(t, e, esinet, psaps, "urn:service:sos"); to != "" || code != 503 {
		t.Fatalf("third call: %q %d, want refused with 503", to, code)
	}
	if log := e.BCFLog(); len(log) != 1 || log[0].Action != "refused" {
		t.Fatalf("BCF log %+v", log)
	}
	e2, esinet2, psaps2 := policyLab(t, func(c *config.Core) {
		c.BCF = config.BCF{Enabled: true, PerMinute: 1, Mark: true}
		c.Rules = []config.CoreRule{{Name: "Suspicious to West", Suspicious: true, Action: "route", To: "sip:west@west.example.gov"}}
	})
	if to, _ := call911(t, e2, esinet2, psaps2, "urn:service:sos"); to != "east" {
		t.Fatalf("first call went to %q", to)
	}
	if to, _ := call911(t, e2, esinet2, psaps2, "urn:service:sos"); to != "west" {
		t.Fatalf("marked call went to %q, want west", to)
	}
}
