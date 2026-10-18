package pbx

import (
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
)

func planEngine(t *testing.T, cfg config.Config) *Engine {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return New(cfg, events.New(), &memRecorder{}, nil)
}

func findingOf(rep PlanReport, kind string) *PlanFinding {
	for i := range rep.Findings {
		if rep.Findings[i].Kind == kind {
			return &rep.Findings[i]
		}
	}
	return nil
}

func TestPlanCheckFindsEverySituation(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: 5099, Transport: "udp"}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"911", "NXXNXXXXXX"}, Trunks: []string{"t1"}}}
	cfg.System.EmergencyNumbers = []string{"911"}
	// Weekdays 9 to 5: open goes to the ring group, closed to a group whose only member is switched off and
	// that has nowhere to go when nobody answers.
	cfg.Extensions[2].Enabled = false
	cfg.RingGroups = append(cfg.RingGroups, config.RingGroup{Number: "601", Name: "Night", Members: []string{"102"}, RingSeconds: 5})
	cfg.Schedules = []config.Schedule{
		{ID: "hours", Name: "Office hours", Hours: []config.OpenHours{{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}},
			Open: config.Destination{Type: "ringgroup", Target: "600"}, Closed: config.Destination{Type: "ringgroup", Target: "601"}},
		{ID: "always", Name: "Always", Hours: []config.OpenHours{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "23:59"}},
			Open: config.Destination{Type: "extension", Target: "100"}, Closed: config.Destination{Type: "hangup"}},
	}
	cfg.Menus = []config.Menu{{ID: "lost", Name: "Forgotten menu", TimeoutSeconds: 5}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100", Destination: config.Destination{Type: "schedule", Target: "hours"}}}
	e := planEngine(t, cfg)
	rep := e.CheckPlan()
	if rep.Times < 2 {
		t.Fatalf("times %d", rep.Times)
	}
	dead := findingOf(rep, "dead-end")
	if dead == nil || !strings.Contains(dead.Says, "Night") {
		t.Fatalf("no dead end for the night group: %+v", rep.Findings)
	}
	if s := dead.Situations[0]; !strings.Contains(s.When, "Office hours closed") || s.Path[0] != "a call to +12125550100" {
		t.Errorf("situation %+v", s)
	}
	if f := findingOf(rep, "single-trunk"); f == nil || !strings.Contains(f.Says, "Carrier") {
		t.Errorf("single trunk not found: %+v", rep.Findings)
	}
	if f := findingOf(rep, "unreachable"); f == nil || f.Where.Target != "lost" {
		t.Errorf("unreachable menu not found: %+v", rep.Findings)
	}
	// "Always" is never reached from a route: it is reported as unreachable, not as never closed.
	var always int
	for _, f := range rep.Findings {
		if f.Where.Target == "always" {
			always++
		}
	}
	if always != 1 {
		t.Errorf("findings about Always: %d", always)
	}
	if rep.Errors == 0 || rep.Warnings == 0 {
		t.Errorf("counts %d %d", rep.Errors, rep.Warnings)
	}
}

// Every combination of the toggles is tried: a dead end behind a switched-over toggle is found though the
// toggle is not switched now.
func TestPlanCheckTriesToggles(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[2].Enabled = false
	cfg.RingGroups = append(cfg.RingGroups, config.RingGroup{Number: "601", Name: "Night", Members: []string{"102"}, RingSeconds: 5})
	cfg.Toggles = []config.Toggle{{ID: "night", Name: "Night mode", Code: "1",
		Normal: config.Destination{Type: "ringgroup", Target: "600"}, Override: config.Destination{Type: "ringgroup", Target: "601"}}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100", Destination: config.Destination{Type: "toggle", Target: "night"}}}
	rep := planEngine(t, cfg).CheckPlan()
	dead := findingOf(rep, "dead-end")
	if dead == nil || !dead.Situations[0].Toggles["night"] {
		t.Fatalf("%+v", rep.Findings)
	}
}

// A clean plan has no errors.
func TestPlanCheckClean(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: 5099, Transport: "udp"},
		{ID: "t2", Name: "Backup", Enabled: true, Host: "127.0.0.2", Port: 5099, Transport: "udp"}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: cfg.System.EmergencyNumbers, Trunks: []string{"t1", "t2"}}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100", Destination: config.Destination{Type: "ringgroup", Target: "600"}}}
	rep := planEngine(t, cfg).CheckPlan()
	if rep.Errors != 0 || findingOf(rep, "single-trunk") != nil {
		t.Fatalf("%+v", rep.Findings)
	}
}
