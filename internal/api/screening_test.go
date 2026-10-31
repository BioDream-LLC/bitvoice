package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A caller is known when called from here (written any usual way) or past the robot test lately.
func TestKnownCaller(t *testing.T) {
	s, _ := newServer(t)
	now := time.Now().UTC()
	add := func(id, dir, from, to, screened string, ago time.Duration) {
		t.Helper()
		if err := s.Store.AddCDR(store.CDR{ID: id, Start: now.Add(-ago), End: now.Add(-ago), Direction: dir, From: from, To: to,
			Trunk: "Carrier", Screened: screened}); err != nil {
			t.Fatal(err)
		}
	}
	add("o1", "outbound", "100", "12125550142", "", 48*time.Hour) // dialled with 1 in front
	add("o2", "outbound", "100", "02071234567", "", 24*time.Hour) // a UK number dialled nationally
	add("i1", "inbound", "+13125550100", "+12125550100", "passed", time.Hour)
	add("i2", "inbound", "+13125550101", "+12125550100", "passed", 100*24*time.Hour) // too long ago
	add("i3", "inbound", "+13125550102", "+12125550100", "failed", time.Hour)
	for n, want := range map[string]string{
		"+12125550142":  "called from here",
		"2125550142":    "called from here",
		"+442071234567": "called from here",
		"3125550100":    "passed the robot test",
		"+13125550101":  "",
		"+13125550102":  "",
		"+19175550000":  "",
	} {
		if got := s.KnownCaller(n); !strings.HasPrefix(got, want) || (want == "" && got != "") {
			t.Errorf("%s: %q, want %q", n, got, want)
		}
	}
}

// The block-last-caller code adds a number once, refuses always-allowed ones, and is audited.
func TestBlockNumber(t *testing.T) {
	s, _ := newServer(t)
	if _, _, err := s.Store.Update("admin", "allow", 0, func(c *config.Config) error {
		c.System.Screening.Allowed = []config.ScreenNumber{{Number: "+13125550177"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.BlockNumber("+1 312 555 0666;verstat=No-TN-Validation", "extension 101", "after a call"); err != nil {
		t.Fatal(err)
	}
	if err := s.BlockNumber("+13125550666", "extension 102", "again"); err != nil {
		t.Fatal(err)
	}
	if err := s.BlockNumber("3125550177", "extension 101", "x"); !errors.Is(err, pbx.ErrAlwaysAllowed) {
		t.Fatalf("always allowed: %v", err)
	}
	cfg, _ := s.Store.Config()
	b := cfg.System.Screening.Blocked
	if len(b) != 1 || b[0].Number != "+13125550666" || b[0].By != "extension 101" || b[0].Added.IsZero() {
		t.Fatalf("blocked %+v", b)
	}
	found := false
	for _, a := range s.Store.AuditLog(50) {
		found = found || a.Action == "screening.blocked"
	}
	if !found {
		t.Fatal("not audited")
	}
}

// Numbers that call again and again and are got rid of at once are suggested; a real conversation,
// a known number or a dismissed one never is.
func TestScreeningSuggestions(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	c.code = "123456"
	if code, out := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	now := time.Now().UTC()
	n := 0
	add := func(from string, answered bool, billsec, duration int, ext, screened string) {
		t.Helper()
		n++
		r := store.CDR{ID: "s" + string(rune('a'+n)), Start: now.Add(-time.Duration(n) * time.Hour), Direction: "inbound", From: from,
			To: "+12125550100", Trunk: "Carrier", Billsec: billsec, Duration: duration, AnsweredExt: ext, Screened: screened}
		if answered {
			r.Answer = r.Start
		}
		if err := s.Store.AddCDR(r); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 { // answered and hung up within seconds
		add("+13125550666", true, 3, 8, "101", "")
	}
	add("+13125550666", false, 0, 4, "", "")
	add("+13125550777", false, 0, 12, "", "failed") // two robot tests failed
	add("+13125550777", false, 0, 12, "", "failed")
	for range 4 { // short calls, then one real conversation
		add("+13125550888", true, 2, 5, "101", "")
	}
	add("+13125550888", true, 300, 305, "101", "")
	for range 4 { // short calls from a number that is dismissed
		add("+13125550999", true, 2, 5, "101", "")
	}
	add("+13125550555", true, 2, 5, "101", "") // only twice
	add("+13125550555", true, 2, 5, "101", "")
	if _, _, err := s.Store.Update("admin", "dismiss", 0, func(c *config.Config) error {
		c.System.Screening.Dismissed = []config.ScreenNumber{{Number: "3125550999"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	list := func() []Suggestion {
		t.Helper()
		code, out := c.do("GET", "/api/screening/suggestions", nil)
		if code != 200 {
			t.Fatalf("suggestions: %d %v", code, out)
		}
		b, _ := json.Marshal(out["items"])
		var items []Suggestion
		_ = json.Unmarshal(b, &items)
		return items
	}
	got := struct{ Items []Suggestion }{list()}
	if len(got.Items) != 2 || got.Items[0].Number != "+13125550666" || got.Items[0].Short != 5 || got.Items[1].Number != "+13125550777" ||
		!strings.Contains(got.Items[1].Why, "twice") {
		t.Fatalf("suggestions %+v", got.Items)
	}
	// Once blocked it is no longer suggested.
	if err := s.BlockNumber("+13125550666", "admin", "suggested"); err != nil {
		t.Fatal(err)
	}
	got.Items = list()
	if len(got.Items) != 1 {
		t.Fatalf("after blocking %+v", got.Items)
	}
}
