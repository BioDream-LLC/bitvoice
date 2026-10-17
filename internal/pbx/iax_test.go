package pbx

import (
	"context"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/iax"
)

func iaxService(t *testing.T, e *Engine) *iax.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := iax.NewService(ctx, "127.0.0.1:0", e.LocalSIP, nil)
	t.Cleanup(func() { s.Close(); cancel() })
	e.SetIAX(s)
	return s
}

func toIAX(c config.Config) []iax.Trunk {
	var out []iax.Trunk
	for _, t := range c.Trunks {
		if t.Protocol == "iax2" {
			out = append(out, iax.Trunk{ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, Username: t.Username, Secret: t.Password, Register: t.Register})
		}
	}
	return out
}

// Two BitVoice servers joined by an IAX2 trunk, with a password: a call from one rings a phone on the
// other, both hear each other, and hanging up on one side ends it on both.
func TestIAX2TrunkBetweenServers(t *testing.T) {
	bcfg := testConfig()
	bcfg.Trunks = []config.Trunk{{ID: "a", Name: "Site A", Enabled: true, Protocol: "iax2", Host: "127.0.0.1", Username: "site-a", Password: "iax trunk secret"}}
	bcfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := bcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := startEngine(t, bcfg)
	sb := iaxService(t, b)
	sb.Apply(toIAX(bcfg))

	acfg := testConfig()
	acfg.Trunks = []config.Trunk{{ID: "b", Name: "Site B", Enabled: true, Protocol: "iax2", Host: "127.0.0.1", Port: sb.Port(), Username: "site-a", Password: "iax trunk secret"}}
	acfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"b"}}}
	if err := acfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a, recA := startEngine(t, acfg)
	sa := iaxService(t, a)
	sa.Apply(toIAX(acfg))

	caller := registered(t, a, "100")[0]
	callee := registered(t, b, "101")[0]
	done := make(chan *dialog, 1)
	go func() { d, _ := caller.dial("2125550100"); done <- d }()
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the call over IAX2 did not ring 101 on the other server (trunks: %+v)", a.TrunkStatus())
	}
	cd := callee.answer(ring)
	var ad *dialog
	select {
	case ad = <-done:
	case <-time.After(15 * time.Second):
	}
	if ad == nil {
		t.Fatal("the caller was not answered")
	}
	callee.drainRTP()
	ad.sendRTP(20)
	if !callee.gotRTP(2 * time.Second) {
		t.Fatal("no audio across the IAX2 trunk to the callee")
	}
	caller.drainRTP()
	cd.sendRTP(20)
	if !caller.gotRTP(2 * time.Second) {
		t.Fatal("no audio across the IAX2 trunk back to the caller")
	}
	caller.send(ad.request("BYE"))
	waitFor(t, "the far server's call did not end", func() bool { return len(b.Calls()) == 0 })
	if r := recA.last(t); r.Trunk != "Site B" && r.Trunk != "b" {
		t.Logf("record: %+v", r)
	}
}

// The wrong password is refused, and a call from a stranger never reaches the engine.
func TestIAX2WrongPassword(t *testing.T) {
	bcfg := testConfig()
	bcfg.Trunks = []config.Trunk{{ID: "a", Name: "Site A", Enabled: true, Protocol: "iax2", Host: "127.0.0.1", Username: "site-a", Password: "right secret"}}
	bcfg.InboundRoutes = []config.InboundRoute{{Name: "Main", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := bcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := startEngine(t, bcfg)
	sb := iaxService(t, b)
	sb.Apply(toIAX(bcfg))
	acfg := testConfig()
	acfg.Trunks = []config.Trunk{{ID: "b", Name: "Site B", Enabled: true, Protocol: "iax2", Host: "127.0.0.1", Port: sb.Port(), Username: "site-a", Password: "wrong secret"}}
	acfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"b"}}}
	if err := acfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a, _ := startEngine(t, acfg)
	sa := iaxService(t, a)
	sa.Apply(toIAX(acfg))
	caller := registered(t, a, "100")[0]
	callee := registered(t, b, "101")[0]
	if _, code := caller.dial("2125550100"); code < 400 {
		t.Fatalf("a call with the wrong IAX2 password: %d", code)
	}
	select {
	case <-callee.incoming:
		t.Fatal("the wrong password rang a phone")
	case <-time.After(300 * time.Millisecond):
	}
}
