package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A CAD's token made to name its people records the dispatcher named in each request; any other token
// cannot pass for a person, and a name is cleaned before it is kept.
func TestTokenOnBehalfOf(t *testing.T) {
	s, _ := newServer(t)
	_, cad, err := s.Store.AddToken("bitdispatcher CAD", "admin", "admin", time.Time{}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, plain, _ := s.Store.AddToken("Terraform", "admin", "admin", time.Time{}, false)
	who := func(secret, person string) store.User {
		r := httptest.NewRequest("GET", "/api/calls", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		if person != "" {
			r.Header.Set(OnBehalfHeader, person)
		}
		u, ok := s.user(r)
		if !ok {
			t.Fatal("token refused")
		}
		return u
	}
	if u := who(cad, "Dana Ruiz"); u.Name != "Dana Ruiz (bitdispatcher CAD)" || u.Username != "token:bitdispatcher CAD/Dana Ruiz" {
		t.Fatalf("on behalf: %+v", u)
	}
	if u := who(cad, ""); u.Name != "bitdispatcher CAD" {
		t.Fatalf("no person named: %+v", u)
	}
	if u := who(plain, "Dana Ruiz"); u.Name != "Terraform" || strings.Contains(u.Username, "Dana") {
		t.Fatalf("an ordinary token passed for a person: %+v", u)
	}
	if u := who(cad, "  Dana\r\nX-Evil: 1 / "+strings.Repeat("a", 200)); strings.ContainsAny(u.Name, "\r\n/") || len([]rune(strings.TrimSuffix(u.Name, " (bitdispatcher CAD)"))) > 80 {
		t.Fatalf("name not cleaned: %q", u.Name)
	}
}

// The answer-time report names whoever took a 911 call: an extension, or the person who took a text
// call on the 911 screen or through a CAD (who has no extension).
func TestAnswerReportTakers(t *testing.T) {
	cfg := config.Default()
	cfg.Extensions = []config.Extension{{Number: "100", Name: "Call taker 1"}}
	start := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	cdrs := []store.CDR{
		{Start: start, Emergency: true, Answer: start.Add(5 * time.Second), AnsweredExt: "100", Taken: start.Add(5 * time.Second), TakenBy: "100", TakenName: "Call taker 1"},
		{Start: start, Emergency: true, Answer: start, Taken: start.Add(67 * time.Second), TakenBy: "token:bitdispatcher CAD/Dana Ruiz", TakenName: "Dana Ruiz (bitdispatcher CAD)"},
	}
	r := answerReport(cfg, cdrs, start, start.Add(time.Hour))
	names := map[string]string{}
	for _, tk := range r.ByTaker {
		names[tk.Name] = tk.Ext
	}
	if len(r.ByTaker) != 2 || names["Call taker 1"] != "100" || names["Dana Ruiz (bitdispatcher CAD)"] != "" {
		t.Fatalf("takers %+v", r.ByTaker)
	}
}

// A 911 route that would refuse calls is flagged in Health.
func TestEmergencyRouteWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.Trunks = []config.Trunk{{ID: "in", Name: "ESInet", Enabled: true, ESInet: true}, {ID: "t2", Name: "Carrier", Enabled: true}}
	if w := emergencyRouteWarnings(cfg); len(w) != 1 || !strings.Contains(w[0][0], "No incoming route for 911 calls from ESInet") {
		t.Fatalf("no route: %v", w)
	}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Closed", Trunk: "in", Destination: config.Destination{Type: "hangup"}}, {Name: "Office", Trunk: "t2", Destination: config.Destination{Type: "hangup"}}}
	if w := emergencyRouteWarnings(cfg); len(w) != 1 || !strings.Contains(w[0][0], "Closed hangs up 911 calls from ESInet") {
		t.Fatalf("hang-up route: %v", w)
	}
	cfg.InboundRoutes[0].Destination = config.Destination{Type: "extension", Target: "100"}
	if w := emergencyRouteWarnings(cfg); len(w) != 0 {
		t.Fatalf("a good route warned: %v", w)
	}
}

// A video link that cannot be texted says why, for the 911 screen and CAD.
func TestVideoTextProblem(t *testing.T) {
	if p := videoTextProblem("", "+17175550100"); !strings.HasPrefix(p, "no texting number is set for video links (911 settings") {
		t.Fatalf("no number: %q", p)
	}
	if p := videoTextProblem("+15405550100", "anonymous"); !strings.HasPrefix(p, "the caller sent no number") {
		t.Fatalf("anonymous: %q", p)
	}
	if p := videoTextProblem("+15405550100", "+17175550100"); p != "" {
		t.Fatalf("can send: %q", p)
	}
}
