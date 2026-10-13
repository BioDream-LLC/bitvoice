package pbx

import (
	"context"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
)

// twoTenants starts the main engine with a switch, and a tenant "acme" sharing its SIP port. Both
// have extensions 100, 101 and 102.
func twoTenants(t *testing.T, mainCfg, acmeCfg config.Config) (main, acme *Engine, sw *Switch, recA *memRecorder) {
	t.Helper()
	main, _ = startEngine(t, mainCfg, func(e *Engine) { sw = NewSwitch(e) })
	recA = &memRecorder{}
	acme = New(acmeCfg, events.New(), recA, nil)
	acme.T1, acme.T2 = 50*time.Millisecond, 200*time.Millisecond
	acme.StartShared(context.Background(), main)
	sw.Add("acme", "pbx.acme.example", acme)
	t.Cleanup(acme.Stop)
	return main, acme, sw, recA
}

func TestTenantsShareThePortButNotExtensions(t *testing.T) {
	main, acme, _, recA := twoTenants(t, testConfig(), testConfig())
	mainPhones := registered(t, main, "100", "101")
	// Acme's phones sign in as acme.100 and acme.101: the same numbers, another phone system.
	acmePhones := registered(t, main, "acme.100", "acme.101")
	if len(acme.Registrations()) != 2 || len(main.Registrations()) != 2 {
		t.Fatalf("registrations: main %d, acme %d", len(main.Registrations()), len(acme.Registrations()))
	}
	// Acme's 100 calls 101: Acme's 101 rings, the main system's 101 does not.
	go acmePhones[0].dial("101")
	ring := acmePhones[1].waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("acme's 101 did not ring")
	}
	select {
	case <-mainPhones[1].incoming:
		t.Fatal("the main system's 101 rang for acme's call")
	case <-time.After(300 * time.Millisecond):
	}
	acmePhones[1].answer(ring)
	waitFor(t, "acme's call not answered", func() bool {
		cs := acme.Calls()
		return len(cs) == 1 && cs[0].State == "answered"
	})
	if len(main.Calls()) != 0 {
		t.Fatalf("the main system holds acme's call: %+v", main.Calls())
	}
	acme.Hangup(acme.Calls()[0].ID, "test")
	if r := recA.last(t); r.AnsweredExt != "101" {
		t.Fatalf("acme's record: %+v", r)
	}
	// The wrong secret for acme's 100 is refused even though the main system's 100 shares it? No:
	// a phone with acme's prefix is checked against acme only.
	p := newPhone(t, main.SIPPort(), "acme.109", testSecret)
	if code := p.register(); code == 200 {
		t.Fatal("an extension acme does not have registered")
	}
}

func TestTenantByDomain(t *testing.T) {
	main, acme, _, _ := twoTenants(t, testConfig(), testConfig())
	_ = main
	p := newPhone(t, main.SIPPort(), "100", testSecret)
	p.domain = "pbx.acme.example"
	if code := p.register(); code != 200 {
		t.Fatalf("register on acme's domain: %d", code)
	}
	if len(acme.Registrations()) != 1 || len(main.Registrations()) != 0 {
		t.Fatalf("the domain did not choose acme: main %d, acme %d", len(main.Registrations()), len(acme.Registrations()))
	}
}

func TestTenantProviderCallsGoByNumber(t *testing.T) {
	trunk := func(did, ext string) config.Config {
		c := testConfig()
		c.Trunks = []config.Trunk{{ID: "carrier", Name: "Carrier", Enabled: true, Host: "127.0.0.1", AllowFrom: []string{"127.0.0.1"}}}
		c.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: did, Destination: config.Destination{Type: "extension", Target: ext}}}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	main, acme, _, _ := twoTenants(t, trunk("+12125550100", "100"), trunk("+13125550100", "101"))
	acmePhones := registered(t, main, "acme.101")
	mainPhones := registered(t, main, "100")
	// The provider: a phone-like client sending from 127.0.0.1 to Acme's number.
	carrier := newPhone(t, main.SIPPort(), "carrier", "")
	go carrier.dial("+13125550100")
	if acmePhones[0].waitRing(2*time.Second) == nil {
		t.Fatal("a call to acme's number did not reach acme")
	}
	go carrier.dial("+12125550100")
	if mainPhones[0].waitRing(2*time.Second) == nil {
		t.Fatal("a call to the main number did not reach the main system")
	}
	_ = acme
}

func TestTenantLimitsSpareEmergencyCalls(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "out", Name: "Line", Enabled: true, Host: "127.0.0.1", Port: 9}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "911", Patterns: []string{"911"}, Trunks: []string{"out"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	main, acme, _, _ := twoTenants(t, testConfig(), cfg)
	ps := registered(t, main, "acme.100", "acme.101")
	acme.Suspended.Store(true)
	if _, code := ps[0].dial("101"); code != 503 {
		t.Fatalf("a suspended tenant's call: %d", code)
	}
	// 911 is still tried (the trunk is not there, so it fails further on, never at the limit).
	_, code := ps[0].dial("911")
	if code == 503 && acme.lastEmergency.Load() == 0 {
		t.Fatal("an emergency call was refused by the suspension")
	}
	acme.Suspended.Store(false)
	acme.MaxCalls = 1
	answeredCall(t, ps[0], ps[1], "101")
	if _, code := ps[1].dial("100"); code != 503 {
		t.Fatalf("a call over the limit: %d", code)
	}
}
