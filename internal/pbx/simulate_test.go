package pbx

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
)

// taken follows the taken branches and returns each step's kind, in order.
func taken(st *SimStep) []string {
	var ks []string
	for st != nil {
		ks = append(ks, st.Kind)
		var next *SimStep
		for _, b := range st.Branches {
			if b.Taken {
				next = b.Step
			}
		}
		st = next
	}
	return ks
}

func TestSimulate(t *testing.T) {
	cfg := testConfig()
	cfg.System.Timezone = "UTC"
	cfg.Scripts = []config.Script{{ID: "vip", Name: "VIPs", Language: "js",
		Source:   `if (call.from === "+15550001") return {type: "extension", target: "102"}`,
		Fallback: config.Destination{Type: "menu", Target: "m1"}}}
	cfg.Menus = []config.Menu{{ID: "m1", Name: "Main", TimeoutSeconds: 5, Repeats: 2,
		Options: []config.MenuOption{{Key: "1", Label: "Sales", Destination: config.Destination{Type: "ringgroup", Target: "600"}},
			{Key: "9", Destination: config.Destination{Type: "schedule", Target: "s1"}}}}}
	cfg.Schedules = []config.Schedule{{ID: "s1", Name: "Office hours",
		Hours:  []config.OpenHours{{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}},
		Open:   config.Destination{Type: "script", Target: "vip"},
		Closed: config.Destination{Type: "voicemail", Target: "100"}}}
	cfg.FindExtension("100").Voicemail.Enabled = true
	cfg.FindExtension("101").Voicemail.Enabled = true
	cfg.FindExtension("101").NoAnswer = &config.Destination{Type: "voicemail", Target: "101"}
	cfg.InboundRoutes = []config.InboundRoute{{ID: "r1", Name: "Main line", DID: "+12125550100", Destination: config.Destination{Type: "schedule", Target: "s1"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := New(cfg, events.New(), nil, nil)
	monday10 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	sunday := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)

	// Open hours, a VIP: the script picks 102, which has no phone, and no "if nobody answers".
	r := e.Simulate(SimInput{From: "+15550001", To: "+12125550100", At: monday10})
	if got := strings.Join(taken(r.Start), ">"); got != "route>schedule>script>extension" || r.Direction != "inbound" || !strings.HasPrefix(r.Outcome, "Fails:") {
		t.Fatalf("VIP in hours: %s / %s", got, r.Outcome)
	}
	// Assuming phones are connected, 102 rings.
	r = e.Simulate(SimInput{From: "+15550001", To: "+12125550100", At: monday10, AssumePhones: true})
	if !strings.HasPrefix(r.Outcome, "Rings Ext 102") {
		t.Fatalf("with phones: %s", r.Outcome)
	}
	// Anyone else in hours: the script has no opinion, so the fallback menu; the caller chooses.
	r = e.Simulate(SimInput{From: "+15559999", To: "+12125550100", At: monday10})
	if got := strings.Join(taken(r.Start), ">"); got != "route>schedule>script>menu" || !strings.Contains(r.Outcome, "chooses in Main") {
		t.Fatalf("others in hours: %s / %s", got, r.Outcome)
	}
	// The menu's 9 goes back to the schedule: shown as a loop, not followed for ever.
	menu := r.Start.Branches[0].Step.Branches[0].Step.Branches[0].Step
	if menu.Kind != "menu" || menu.Branches[1].Step.Kind != "loop" || len(menu.Branches) != 3 {
		t.Fatalf("menu %+v", menu)
	}
	// Sunday: closed, voicemail.
	r = e.Simulate(SimInput{From: "+15559999", To: "+12125550100", At: sunday})
	if got := strings.Join(taken(r.Start), ">"); got != "route>schedule>voicemail" || !strings.Contains(r.Start.Branches[0].Step.Says, "closed") {
		t.Fatalf("Sunday: %s", got)
	}
	// A number nobody routes.
	if r = e.Simulate(SimInput{From: "+15559999", To: "+19999999999"}); r.Start.Kind != "refused" || !strings.Contains(r.Outcome, "No incoming route") {
		t.Fatalf("unrouted: %+v", r)
	}
	// A route kept for one trunk is found with "any trunk", and says which.
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Host: "sip.example.net", Enabled: true}}
	cfg.InboundRoutes = append(cfg.InboundRoutes, config.InboundRoute{ID: "r2", Name: "Sales line", DID: "+12125550200", Trunk: "t1", Destination: config.Destination{Type: "ringgroup", Target: "600"}})
	e.Apply(cfg)
	if r = e.Simulate(SimInput{From: "+15559999", To: "+12125550200"}); r.Start.Name != "Sales line" || !strings.Contains(r.Start.Says, "on Carrier") {
		t.Fatalf("trunk route: %+v", r.Start)
	}
	// From an extension: another extension with no phone, then its voicemail.
	r = e.Simulate(SimInput{From: "100", To: "101"})
	if got := strings.Join(taken(r.Start), ">"); got != "extension>voicemail" || r.Direction != "internal" {
		t.Fatalf("internal: %s", got)
	}
	// An outside number with no outbound route.
	if r = e.Simulate(SimInput{From: "100", To: "+442071234567"}); r.Start.Kind != "refused" || !strings.Contains(r.Outcome, "Call routing") {
		t.Fatalf("outbound: %+v", r.Start)
	}
	// Emergency mode overrides every route.
	cfg.System.EmergencyMode = config.EmergencyMode{On: true, Destination: config.Destination{Type: "voicemail", Target: "100"}}
	e.Apply(cfg)
	if r = e.Simulate(SimInput{From: "+15559999", To: "+12125550100", At: monday10}); r.Start.Name != "Emergency mode" || r.Start.Branches[0].Step.Kind != "voicemail" {
		t.Fatalf("emergency mode: %+v", r.Start)
	}
}

// A trunk allowed in from the phones' own network does not take their calls: a registered phone's
// requests are a phone's.
func TestTrunkAllowFromDoesNotCapturePhones(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Host: "192.0.2.1", Enabled: true, AllowFrom: []string{"127.0.0.1"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	go ps[0].dial("101")
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("an internal call was taken for a call from the trunk")
	}
}
