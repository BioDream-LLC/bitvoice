package pbx

import (
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// historyRecorder is a recorder whose call history can be read, as the store's can.
type historyRecorder struct {
	memRecorder
	past []store.CDR
}

func (h *historyRecorder) CDRs(store.CDRQuery) ([]store.CDR, error) { return h.past, nil }

func fraudEngine(t *testing.T, rec Recorder, change func(*config.Config)) *Engine {
	t.Helper()
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: 5099, Transport: "udp"}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "World", Patterns: []string{"011.", "NXXNXXXXXX", "911"}, Trunks: []string{"t1"}}}
	cfg.Security.FraudGuard = config.FraudGuard{On: true}
	if change != nil {
		change(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return New(cfg, events.New(), rec, nil)
}

func ext(e *Engine, n string) *config.Extension { return e.Config().FindExtension(n) }

func TestFraudGuardFirstCallAbroad(t *testing.T) {
	e := fraudEngine(t, &historyRecorder{}, nil)
	p := e.routeCall(e.Config(), ext(e, "100"), "011442071234567", false)
	if p.reject != 0 {
		t.Fatalf("refused: %v", p.why)
	}
	d := e.FraudDecisions()
	if len(d) != 1 || d[0].Action != "alert" || d[0].Country != "44" {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(strings.Join(p.why, " "), "first call to that country") {
		t.Fatalf("why %v", p.why)
	}
	// The second call there is no longer a first: let through quietly.
	e.routeCall(e.Config(), ext(e, "100"), "011442071234568", false)
	if d := e.FraudDecisions(); d[0].Action != "allow" {
		t.Fatalf("%+v", d[0])
	}
	// A domestic call is not judged at all.
	e.routeCall(e.Config(), ext(e, "100"), "2125551212", false)
	if n := len(e.FraudDecisions()); n != 2 {
		t.Fatalf("%d decisions", n)
	}
}

// A first call abroad outside business hours asks for a PIN.
func TestFraudGuardAfterHoursAsksPIN(t *testing.T) {
	e := fraudEngine(t, &historyRecorder{}, func(c *config.Config) {
		c.Schedules = []config.Schedule{{ID: "never", Name: "Never open", Hours: []config.OpenHours{},
			Open: config.Destination{Type: "hangup"}, Closed: config.Destination{Type: "hangup"}}}
		c.PINSets = []config.PINSet{{ID: "ps", Name: "Travel", PINs: []string{"4321"}}}
		c.Security.FraudGuard.Schedule, c.Security.FraudGuard.PINSet = "never", "ps"
	})
	p := e.routeCall(e.Config(), ext(e, "100"), "011442071234567", false)
	if p.local != "pinout" || p.box != "ps" {
		t.Fatalf("%+v", p)
	}
}

// Many calls abroad in a short time are refused, even with an approved exception; an emergency call never.
func TestFraudGuardBurst(t *testing.T) {
	e := fraudEngine(t, &historyRecorder{}, func(c *config.Config) {
		c.Security.FraudGuard.Burst = 2
		c.Security.FraudGuard.Exceptions = []config.FraudException{{Ext: "100", Until: "2099-12-31", Note: "trip"}}
	})
	var p plan
	for range 3 {
		p = e.routeCall(e.Config(), ext(e, "100"), "011442071234567", false)
	}
	if p.reject != 403 || !strings.Contains(strings.Join(p.why, " "), "too many calls abroad") {
		t.Fatalf("%+v", p)
	}
	if p := e.routeCall(e.Config(), ext(e, "100"), "911", false); p.reject != 0 {
		t.Fatalf("emergency refused: %+v", p)
	}
}

// When the call history cannot be read, whether this is a first call is unknown, and unknown is not safe: the
// guard is cautious (and with no PIN set to ask for, refuses), saying what it could not measure.
func TestFraudGuardUnknownHistoryIsCautious(t *testing.T) {
	e := fraudEngine(t, &memRecorder{}, nil)
	p := e.routeCall(e.Config(), ext(e, "100"), "011442071234567", false)
	why := strings.Join(p.why, " ")
	if p.reject != 403 || !strings.Contains(why, "Not measured: this extension has not called that country") {
		t.Fatalf("%+v", p)
	}
}

// The Caribbean shares +1 but is abroad: a classic toll-fraud destination.
func TestAbroadNANP(t *testing.T) {
	s := config.System{Country: "US"}
	for n, want := range map[string]string{"18765551234": "1876", "8765551234": "1876", "+442071234567": "44", "011492071234": "49"} {
		if ok, code := s.Abroad(n); !ok || code != want {
			t.Errorf("%s: %v %s", n, ok, code)
		}
	}
	if ok, _ := s.Abroad("2125551212"); ok {
		t.Error("New York is abroad")
	}
	gb := config.System{Country: "GB"}
	if ok, code := gb.Abroad("0033123456789"); !ok || code != "33" {
		t.Errorf("GB to France: %v %s", ok, code)
	}
}
