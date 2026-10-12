package pbx

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// A call from the web gateway with a call link's pass rings the link's destination, named after the
// caller; without the pass (or with a closed link) it must authenticate like any phone, and fails.
func TestCallLink(t *testing.T) {
	cfg := testConfig()
	cfg.System.CallLinks = []config.CallLink{{ID: "L1", Name: "Support", Enabled: true, Destination: config.Destination{Type: "extension", Target: "101"}, MaxCalls: 1},
		{ID: "L2", Name: "Closed", Enabled: false, Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	e.SetLinkToken("pass")
	b := registered(t, e, "101")[0]
	web := newPhone(t, e.SIPPort(), "web", "not-a-secret")
	web.extra = map[string]string{"X-BitVoice-Link": "pass;L1", "X-BitVoice-Link-Name": `Jo "<b>" Web`}
	go web.dial("1")
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the link's destination did not ring")
	}
	if from := ring.Req.Get("From"); !strings.HasPrefix(from, `"Web: Jo b Web"`) {
		t.Fatalf("caller shown as %s", from)
	}
	// One call at a time on this link.
	second := newPhone(t, e.SIPPort(), "web", "x")
	second.extra = web.extra
	if _, code := second.dial("1"); code != 486 {
		t.Fatalf("a second call: %d, want 486", code)
	}
	b.answer(ring)
	time.Sleep(100 * time.Millisecond)
	if cs := e.Calls(); len(cs) != 1 || cs[0].Direction != "inbound" || cs[0].Trunk != "Call link: Support" {
		t.Fatalf("calls %+v", cs)
	}
	_ = rec
	for _, h := range []string{"wrong;L1", "pass;L2", "pass"} {
		p := newPhone(t, e.SIPPort(), "web", "x")
		p.extra = map[string]string{"X-BitVoice-Link": h}
		if _, code := p.dial("101"); code != 403 && code != 407 && code != 401 {
			t.Fatalf("%q: %d, want an authentication failure", h, code)
		}
	}
}
