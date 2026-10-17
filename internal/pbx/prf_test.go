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

func prfEngine(t *testing.T, policies map[string]string, setup func(*config.Config)) (*Engine, *carrier, *carrier) {
	esinet, psaps := newCarrier(t), newCarrier(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{
		{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: esinet.stack.Port(), ESInet: true, AllowFrom: []string{"127.0.0.1"}},
		{ID: "east", Name: "East PSAP", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port(), Username: "acct", Password: "trunkpass"},
		{ID: "west", Name: "West PSAP", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port(), Username: "acct", Password: "trunkpass"},
	}
	cfg.System.Core = config.Core{PSAPs: []config.CorePSAP{
		{URI: "sip:sos@east.example.gov", Name: "East County", Trunk: "east"},
		{URI: "sip:sos@west.example.gov", Name: "West County", Trunk: "west"},
	}, Default: "sip:sos@west.example.gov", PolicyStore: true, QueueURI: "sip:esrp@esrp.example.gov", AllowFrom: []string{"127.0.0.1"},
		Source: "bcf.example.gov", BCF: config.BCF{Enabled: true, BadActors: true}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "esrp"}}}
	if setup != nil {
		setup(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed := map[string]i3.Policy{}
	for k, s := range policies {
		p, err := i3.ParsePolicy([]byte(s), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		parsed[k] = p
	}
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.FindService = func(string, *i3.Location) (string, string) { return "sip:sos@east.example.gov", "" }
		e.Policies = func(pt, q, id string) (i3.Policy, bool) { p, ok := parsed[pt+" "+q+id]; return p, ok }
	})
	return e, esinet, psaps
}

func sendSOS(t *testing.T, e *Engine, esinet *carrier, extra map[string]string) *sip.ClientTx {
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sd := sdp.New("127.0.0.1", 40022, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + string(sd) + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF +
		"\r\n--B\r\nContent-Type: application/EmergencyCallData.ServiceInfo+xml\r\n\r\n<svcInfo:EmergencyCallData.ServiceInfo xmlns:svcInfo=\"urn:ietf:params:xml:ns:EmergencyCallData:ServiceInfo\"><svcInfo:ServiceEnvironment>Business</svcInfo:ServiceEnvironment><svcInfo:ServiceType>relay</svcInfo:ServiceType></svcInfo:EmergencyCallData.ServiceInfo>\r\n--B--\r\n"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550100@carrier.example.net>;tag=e1")
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	for k, v := range extra {
		inv.Set(k, v)
	}
	inv.Body = []byte(body)
	return esinet.stack.Request(inv, pbx)
}

func gotAt(t *testing.T, psaps *carrier) *sip.Message {
	t.Helper()
	select {
	case m := <-psaps.invites:
		return m
	case <-time.After(15 * time.Second):
		t.Fatal("no PSAP got the call")
	}
	return nil
}

// A stored OriginationRoutePolicy decides: relay calls (Additional Data) go west, others by the
// ECRF to east, whose own next-hop policy is then followed; a suspicious score sends to west too.
func TestESRPRunsStoredPolicies(t *testing.T) {
	origin := `{"policyOwner":"esrp.example.gov","policyType":"OriginationRoutePolicy","policyQueueName":"sip:esrp@esrp.example.gov","policyRules":[
	  {"id":"relay","priority":9,"conditions":[{"conditionType":"AdditionalDataCondition","type":"EmergencyCallData.ServiceInfo","operator":"EQ","element":"ServiceType","content":"relay"}],
	   "actions":[{"actionType":"RouteAction","recipientUri":"sip:sos@west.example.gov"}]},
	  {"id":"normal","priority":1,"conditions":[{"conditionType":"LostServiceUrnCondition","urn":"urn:emergency:service:sos.psap"}],
	   "actions":[{"actionType":"InvokePolicyAction","policyType":"NormalNextHopRoutePolicy"}]}]}`
	east := `{"policyOwner":"east.example.gov","policyType":"NormalNextHopRoutePolicy","policyQueueName":"sip:sos@east.example.gov","policyRules":[
	  {"id":"spam","priority":5,"conditions":[{"conditionType":"CallSuspicionCondition","scoreFrom":60,"scoreTo":100}],"actions":[{"actionType":"BusyAction"}]},
	  {"id":"take","priority":1,"actions":[{"actionType":"RouteAction","recipientUri":"sip:sos@east.example.gov"}]}]}`
	pols := map[string]string{"OriginationRoutePolicy sip:esrp@esrp.example.gov": origin, "NormalNextHopRoutePolicy sip:sos@east.example.gov": east}
	e, esinet, psaps := prfEngine(t, pols, nil)
	routes := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(routes)

	tx := sendSOS(t, e, esinet, nil)
	m := gotAt(t, psaps)
	if m.Get("Call-Info") == "" || !strings.Contains(m.Get("Call-Info"), "@bcf.example.gov>;purpose=emergency-source") {
		t.Fatalf("no source ID went on: %q", m.Get("Call-Info"))
	}
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("got %v", r)
	}
	why := ""
	for why == "" {
		select {
		case ev := <-routes.C:
			if r, ok := ev.Data.(Route); ok {
				why = r.Why
				if r.PSAP != "sip:sos@west.example.gov" || !strings.Contains(why, "OriginationRoutePolicy") || !strings.Contains(why, "rule relay") {
					t.Fatalf("relay call: %+v", r)
				}
			}
		case <-time.After(15 * time.Second):
			t.Fatal("no route event")
		}
	}

	// Without the relay block, and with a suspicion score: east's policy says busy.
	origin2 := strings.Replace(origin, `"content":"relay"`, `"content":"nothing"`, 1)
	e2, esinet2, _ := prfEngine(t, map[string]string{"OriginationRoutePolicy sip:esrp@esrp.example.gov": origin2, "NormalNextHopRoutePolicy sip:sos@east.example.gov": east}, nil)
	tx = sendSOS(t, e2, esinet2, map[string]string{"Call-Info": "<urn:emergency:uid:suspicion:80>;purpose=emergency-CallSuspicion"})
	if r := finalOf(tx); r == nil || r.StatusCode != 600 {
		t.Fatalf("suspicious call got %v, want 600 Busy Everywhere", r)
	}
}

// A downstream PSAP reports the source by the ID its call carried: the next call from that source
// has its suspicion raised (or is refused when the BCF blocks bad actors).
func TestBadActors(t *testing.T) {
	for _, block := range []bool{false, true} {
		e, esinet, psaps := prfEngine(t, nil, func(c *config.Config) {
			c.System.Core.PolicyStore = false
			c.System.Core.BCF.BadActorBlock = block
			c.System.Core.Rules = []config.CoreRule{{ID: "s", Name: "suspects west", Suspicious: true, Action: "route", To: "sip:sos@west.example.gov"}}
		})
		tx := sendSOS(t, e, esinet, nil)
		m := gotAt(t, psaps)
		finalOf(tx)
		ci := m.Get("Call-Info")
		id := ci[strings.Index(ci, "<sip:")+5 : strings.Index(ci, ">")]
		if _, err := e.ReportBadActor("nobody@bcf.example.gov", "test"); err != ErrUnknownSource {
			t.Fatalf("an unknown ID: %v", err)
		}
		if _, err := e.ReportBadActor("<sip:"+id+">", "PSAP east"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ReportBadActor(id, "PSAP east"); err != ErrAlreadyReported {
			t.Fatalf("twice: %v", err)
		}
		if len(e.BadActors()) != 1 {
			t.Fatal("not listed")
		}
		tx = sendSOS(t, e, esinet, nil)
		if block {
			if r := finalOf(tx); r == nil || r.StatusCode != 503 {
				t.Fatalf("a blocked bad actor got %v", r)
			}
		} else {
			m = gotAt(t, psaps)
			if !strings.Contains(m.Get("To"), "") || finalOf(tx).StatusCode != 200 {
				t.Fatal("not let through")
			}
			found := false
			for _, ev := range e.BCFLog() {
				found = found || ev.Action == "marked" && ev.Why == "a reported bad actor"
			}
			if !found {
				t.Fatal("not marked")
			}
		}
		if !e.ClearBadActor(id) || len(e.BadActors()) != 0 {
			t.Fatal("not cleared")
		}
	}
}
