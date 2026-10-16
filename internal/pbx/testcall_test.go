package pbx

import (
	"context"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The test call generator in one server calls the PSAP in another: the PSAP answers by itself with
// its name, the service and the location, loops three packets back and hangs up; a second test
// from the same place within two minutes is refused.
func TestTestCalls(t *testing.T) {
	psapCfg := testConfig()
	psapCfg.System.Name = "County 911"
	psapCfg.System.PSAP.AgencyID = "psap.county.example.gov"
	psapCfg.Trunks = []config.Trunk{{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: 9, ESInet: true, AllowFrom: []string{"127.0.0.1"}}}
	psapCfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "hangup"}}}
	if err := psapCfg.Validate(); err != nil {
		t.Fatal(err)
	}
	psap, _ := startEngine(t, psapCfg)

	genCfg := testConfig()
	genCfg.Trunks = []config.Trunk{{ID: "p", Name: "County", Enabled: true, Host: "127.0.0.1", Port: psap.SIPPort()}}
	genCfg.System.Core = config.Core{PSAPs: []config.CorePSAP{{URI: "sip:sos@psap.county.example.gov", Name: "County", Trunk: "p"}}, Source: "tcg.example.gov"}
	if err := genCfg.Validate(); err != nil {
		t.Fatal(err)
	}
	gen, _ := startEngine(t, genCfg)

	res, err := gen.PlaceTestCall(context.Background(), "sip:sos@psap.county.example.gov", []byte(testPIDF))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != 200 || res.Echoed != 3 || !strings.Contains(res.Answer, "County 911 (psap.county.example.gov)") ||
		!strings.Contains(res.Answer, "Harrisburg") {
		t.Fatalf("%+v", res)
	}
	res, _ = gen.PlaceTestCall(context.Background(), "sip:sos@psap.county.example.gov", []byte(testPIDF))
	if res.Status != 486 || res.OK {
		t.Fatalf("a repeat: %+v", res)
	}
	waitFor(t, "the PSAP did not hang up the test call", func() bool { return len(psap.Calls()) == 0 })

	// Switched off at the PSAP: 404.
	off := psapCfg
	off.System.PSAP.TestCallsOff = true
	psap.Apply(off)
	psap.tests.mu.Lock()
	psap.tests.seen = nil
	psap.tests.mu.Unlock()
	if res, _ = gen.PlaceTestCall(context.Background(), "sip:sos@psap.county.example.gov", []byte(testPIDF)); res.Status != 404 {
		t.Fatalf("off: %+v", res)
	}
	if _, err := gen.PlaceTestCall(context.Background(), "sip:nobody@x", nil); err == nil {
		t.Fatal("a PSAP not in the list")
	}
}
