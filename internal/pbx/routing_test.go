package pbx

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// An emergency number must reach a trunk even when no outbound pattern matches it.
func TestEmergencyWithoutMatchingRoute(t *testing.T) {
	cfg := config.Default()
	cfg.System.EmergencyNumbers = []string{"999", "112"}
	cfg.Trunks = []config.Trunk{{ID: "a", Name: "Off", Enabled: false}, {ID: "b", Name: "Line", Enabled: true}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Emergency", Patterns: []string{"911"}, Trunks: []string{"a"}}}
	e := &Engine{}
	p := e.route(&cfg, &config.Extension{Number: "100", Outbound: false}, "999")
	if p.reject != 0 || p.direction != "emergency" || len(p.stages) != 1 || p.stages[0].targets[0].trunk.ID != "b" || p.stages[0].targets[0].number != "999" {
		t.Fatalf("plan %+v", p)
	}
	cfg.Trunks[1].Enabled = false
	if p := e.route(&cfg, nil, "112"); p.reject != 503 {
		t.Fatalf("no trunk: %+v", p)
	}
}

func TestEmergencyPrefersExactRoute(t *testing.T) {
	cfg := config.Default()
	cfg.System.EmergencyNumbers = []string{"911"}
	cfg.Trunks = []config.Trunk{{ID: "a", Name: "Cheap", Enabled: true}, {ID: "b", Name: "E911", Enabled: true}}
	cfg.OutboundRoutes = []config.OutboundRoute{
		{Name: "Everything", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"a"}},
		{Name: "Emergency", Patterns: []string{"911"}, Trunks: []string{"b"}},
	}
	p := (&Engine{}).route(&cfg, nil, "911")
	if p.trunk != "E911" || p.stages[0].targets[0].number != "911" {
		t.Fatalf("plan %+v", p)
	}
}

func TestSameNumber(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"+12125550100", "2125550100", 1}, {"12125550100", "+1 (212) 555-0100", 2}, {"+442071234567", "02071234567", 1},
		{"00442071234567", "+442071234567", 2}, {"100", "1100", 0}, {"2125550100", "2125550101", 0}, {"", "123", 0},
	} {
		if got := sameNumber(c.a, c.b); got != c.want {
			t.Errorf("sameNumber(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
