package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/fax"
)

// TestFaxBetweenServers sends a fax from one BitVoice to another over a trunk: the sender places
// the call, the receiving fax number answers, both switch to T.38 (whichever asks first), and the
// pages arrive.
func TestFaxBetweenServers(t *testing.T) {
	// The receiving server: calls from the trunk to +12125550199 go to fax number 300.
	rcfg := testConfig()
	rcfg.Faxes = []config.FaxBox{{ID: "f1", Number: "300", Name: "Office fax", Station: "+12125550199"}}
	rcfg.Trunks = []config.Trunk{{ID: "in", Name: "Sender", Enabled: true, Host: "127.0.0.1", AllowFrom: []string{"127.0.0.1"}}}
	rcfg.InboundRoutes = []config.InboundRoute{{Name: "Fax", DID: "+12125550199", Destination: config.Destination{Type: "fax", Target: "300"}}}
	if err := rcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := make(chan FaxIn, 1)
	rx, _ := startEngine(t, rcfg, func(e *Engine) { e.OnFax = func(in FaxIn) { got <- in } })

	// The sending server: fax number 310, numbers starting + go out on a trunk to the other server.
	scfg := testConfig()
	scfg.Faxes = []config.FaxBox{{ID: "f2", Number: "310", Name: "Sales fax", Station: "+13125550110"}}
	scfg.Trunks = []config.Trunk{{ID: "out", Name: "Line", Enabled: true, Host: "127.0.0.1", Port: rx.SIPPort()}}
	scfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Prepend: "+1", Trunks: []string{"out"}}}
	if err := scfg.Validate(); err != nil {
		t.Fatal(err)
	}
	tx, _ := startEngine(t, scfg)

	pages := fax.TextPages("Invoice 1234\n\nPlease find the invoice attached.\n\fSecond page.", "letter")
	res, err := tx.SendFax("310", "2125550199", pages)
	if err != nil || res.Pages != 2 {
		t.Fatalf("send: %d pages, %v", res.Pages, err)
	}
	select {
	case in := <-got:
		if in.Result.Err != nil || len(in.Result.Pages) != 2 || in.Station != "+13125550110" {
			t.Fatalf("received %d pages from %q: %v", len(in.Result.Pages), in.Station, in.Result.Err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("nothing received")
	}
	if n := len(tx.Calls()) + len(rx.Calls()); n != 0 {
		waitFor(t, "calls left up", func() bool { return len(tx.Calls())+len(rx.Calls()) == 0 })
	}
}
