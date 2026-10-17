package pbx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A toggle sends calls to its normal destination, *28 then its code switches it over (rising tone),
// calls then go to the override, the switch is kept, and *281 again puts it back.
func TestToggleSwitchesCallFlow(t *testing.T) {
	cfg := testConfig()
	cfg.Toggles = []config.Toggle{{ID: "night", Name: "Night service", Code: "1",
		Normal:   config.Destination{Type: "extension", Target: "101"},
		Override: config.Destination{Type: "extension", Target: "102"}, Allowed: []string{"100"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var saved atomic.Value
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.SaveDiverts = func(s DivertState) error { saved.Store(s); return nil }
	})
	ps := registered(t, e, "100", "101", "102")
	if p := e.resolve(&cfg, config.Destination{Type: "toggle", Target: "night"}, 0); len(p.stages) == 0 || p.stages[0].targets[0].ext != "101" {
		t.Fatalf("normal: %+v", p)
	}
	// 101 may not switch it.
	if _, code := ps[1].dial("*281"); code != 403 {
		t.Fatalf("101 switching it: %d", code)
	}
	d, code := ps[0].dial("*281")
	if code != 200 {
		t.Fatalf("*281 answered %d", code)
	}
	ps[0].gotRTP(time.Second)
	ps[0].waitBye(t, 4*time.Second)
	_ = d
	if !e.ToggleOn("night") {
		t.Fatal("*281 did not switch the toggle over")
	}
	if s, _ := saved.Load().(DivertState); len(s.Toggles) != 1 || s.Toggles[0] != "night" {
		t.Fatalf("not saved: %+v", s)
	}
	if p := e.resolve(&cfg, config.Destination{Type: "toggle", Target: "night"}, 0); len(p.stages) == 0 || p.stages[0].targets[0].ext != "102" {
		t.Fatalf("override: %+v", p)
	}
	if st := e.extensionState("*281"); st != "confirmed" {
		t.Fatalf("lamp %s", st)
	}
	// Restored after a restart.
	e2, _ := startEngine(t, cfg)
	e2.LoadDiverts(saved.Load().(DivertState))
	if !e2.ToggleOn("night") {
		t.Fatal("the switch was not restored")
	}
	ps[0].dial("*281")
	ps[0].waitBye(t, 4*time.Second)
	if e.ToggleOn("night") || e.extensionState("*281") != "terminated" {
		t.Fatal("*281 again did not put it back")
	}
}

// An outbound route with a PIN set asks for a PIN: a wrong one three times hangs up, a right one
// places the call, and its PIN goes into the call record as the account code.
func TestPINSetOnOutboundRoute(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.PINSets = []config.PINSet{{ID: "clients", Name: "Client codes", PINs: []string{"7001", "7002"}, Record: true}}
	cfg.OutboundRoutes[0].PINSet = "clients"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("PIN prompt answered %d", code)
	}
	a.gotRTP(time.Second)
	var seq uint16 = 1
	for range 3 {
		d.press(&seq, "1111#")
		time.Sleep(1500 * time.Millisecond)
	}
	a.waitBye(t, 8*time.Second)
	select {
	case n := <-c.answered:
		t.Fatalf("a wrong PIN still called %s", n)
	default:
	}
	d, _ = a.dial("2125551212")
	prompted(t, a)
	seq = 1
	d.press(&seq, "7002#")
	select {
	case n := <-c.answered:
		if n != "12125551212" {
			t.Fatalf("carrier got %q", n)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the right PIN did not place the call")
	}
	time.Sleep(200 * time.Millisecond)
	a.send(d.request(sip.BYE))
	deadline := time.Now().Add(15 * time.Second)
	for rec.last(t).Account != "7002" {
		if time.Now().After(deadline) {
			t.Fatalf("account code not recorded: %+v", rec.last(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Emergency calls never ask.
	if p := e.route(&cfg, &cfg.Extensions[0], "911"); p.local == "pinout" {
		t.Fatal("911 asked for a PIN")
	}
}

// A caller ID lookup names a caller the provider did not: JSON from a web address, cached, and a
// slow service never holds the call up past its timeout.
func TestCallerIDLookup(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Get("n") == "3125550000" {
			select { // far longer than the lookup's 300 ms: a call that waited for it would show
			case <-time.After(8 * time.Second):
			case <-r.Context().Done():
			}
		}
		if u, p, _ := r.BasicAuth(); u != "crm" || p != "secret" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"data":{"name":"Northwind Traders"}}`))
	}))
	defer srv.Close()
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Lookups = []config.CallerIDLookup{{ID: "crm", Name: "CRM", Kind: "http", URL: srv.URL + "/who?n={number}",
		Field: "data.name", Username: "crm", Password: "secret", TimeoutMS: 300}}
	cfg.InboundRoutes[0].Lookup = "crm"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]
	call := func(from string) *sip.Message {
		pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
		inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
		inv.Set("From", "<sip:"+from+"@carrier.test>;tag=in"+from)
		inv.Set("To", "<sip:+12125550100@carrier.test>")
		inv.Set("Call-ID", sip.NewCallID())
		inv.Set("CSeq", "1 INVITE")
		inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
		inv.Set("Content-Type", "application/sdp")
		inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
		c.stack.Request(inv, pbx)
		ring := b.waitRing(3 * time.Second)
		if ring == nil {
			t.Fatal("the call did not ring")
		}
		b.send(sip.NewResponse(ring.Req, 486, ""))
		return ring.Req
	}
	if f := call("3125559999").Get("From"); !strings.HasPrefix(f, `"Northwind Traders"`) {
		t.Fatalf("no name looked up: %s", f)
	}
	t0 := time.Now()
	call("3125559999")
	base := time.Since(t0) // a call with nothing to wait for
	if hits.Load() != 1 {
		t.Fatalf("the answer was not cached: %d asks", hits.Load())
	}
	start := time.Now()
	if f := call("3125550000").Get("From"); strings.Contains(f, "Northwind") {
		t.Fatalf("a slow answer was used: %s", f)
	}
	if time.Since(start)-base > 4*time.Second {
		t.Fatalf("a slow lookup held the call %s", time.Since(start))
	}
	if n, err := e.TestLookup(context.Background(), &cfg.Lookups[0], "+1 312 555 9999"); err != nil || n != "Northwind Traders" {
		t.Fatalf("test %q %v", n, err)
	}
	if cleanName("UNKNOWN") != "" || cleanName("Bob <x>\nsecond") != "Bob x" {
		t.Fatal("cleanName")
	}
}

// OpenCNAM is asked in E.164 with the account in the query and format=pbx.
func TestOpenCNAMLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/v3/phone/+13125559999" || q.Get("account_sid") != "AC1" || q.Get("auth_token") != "tok" || q.Get("format") != "pbx" {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte("ACME PLUMBING\n"))
	}))
	defer srv.Close()
	l := &config.CallerIDLookup{ID: "o", Kind: "opencnam", URL: srv.URL + "/v3/phone", Username: "AC1", Password: "tok", TimeoutMS: 1000}
	n, err := fetchName(context.Background(), l, "3125559999")
	if err != nil || n != "ACME PLUMBING" {
		t.Fatalf("%q %v", n, err)
	}
}
