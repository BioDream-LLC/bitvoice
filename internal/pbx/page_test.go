package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestPaging(t *testing.T) {
	cfg := testConfig()
	cfg.PageGroups = []config.PageGroup{{Number: "800", Name: "Warehouse", Members: []string{"101", "102"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	d, code := ps[0].dial("800")
	if code != 200 {
		t.Fatalf("page answered %d", code)
	}
	var dialogs []*dialog
	for _, p := range ps[1:] {
		ring := p.waitRing(3 * time.Second)
		if ring == nil {
			t.Fatalf("%s not paged", p.user)
		}
		if ring.Req.Get("Call-Info") == "" {
			t.Fatal("no auto-answer header")
		}
		dialogs = append(dialogs, p.answer(ring))
	}
	time.Sleep(150 * time.Millisecond)
	for _, p := range ps[1:] {
		p.drainRTP()
	}
	d.sendRTP(5)
	for _, p := range ps[1:] {
		if !p.gotRTP(time.Second) {
			t.Fatalf("%s did not hear the page", p.user)
		}
	}
	// One phone hanging up leaves the page running for the other.
	ps[1].send(dialogs[0].request("BYE"))
	time.Sleep(100 * time.Millisecond)
	ps[2].drainRTP()
	d.sendRTP(3)
	if !ps[2].gotRTP(time.Second) {
		t.Fatal("page ended for everyone")
	}
	ps[0].send(d.request("BYE"))
	ps[2].waitBye(t, 2*time.Second)
}
