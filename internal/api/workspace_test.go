package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A Microsoft Graph stand-in: one page of people, one shared contact, and a meeting now on one
// person's calendar.
func graphForAPI(t *testing.T, people *string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
		case r.URL.Path == "/v1.0/users":
			io.WriteString(w, `{"value":`+*people+`}`)
		case r.URL.Path == "/v1.0/contacts":
			io.WriteString(w, `{"value":[{"id":"c1","displayName":"Fabrikam Support","companyName":"Fabrikam","phones":[{"type":"business","number":"+18005550100"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/calendar/getSchedule"):
			now := time.Now().UTC()
			f := func(t time.Time) string { return t.Format("2006-01-02T15:04:05.0000000") }
			io.WriteString(w, `{"value":[{"scheduleId":"lee@acme.example","scheduleItems":[{"status":"busy","subject":"Board meeting",
				"start":{"dateTime":"`+f(now.Add(-10*time.Minute))+`","timeZone":"UTC"},"end":{"dateTime":"`+f(now.Add(50*time.Minute))+`","timeZone":"UTC"}}]},
				{"scheduleId":"front@acme.example","scheduleItems":[]}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Microsoft 365: settings checked before saving, the directory kept in step (added, changed,
// removed; contacts made here untouched; synced ones changed only at the source), and an
// extension whose person is in a meeting marked away.
func TestWorkspaceMicrosoft365(t *testing.T) {
	people := `[{"id":"u1","displayName":"Dr Lee","mail":"lee@acme.example","businessPhones":["+1 212 555 0101"],"accountEnabled":true},
		{"id":"u2","displayName":"Front Desk","mail":"front@acme.example","businessPhones":[],"mobilePhone":"+12125550102","accountEnabled":true},
		{"id":"u3","displayName":"No Phone","mail":"nophone@acme.example","businessPhones":[],"accountEnabled":true}]`
	g := graphForAPI(t, &people)
	t.Setenv("BITVOICE_GRAPH_LOGIN", g.URL)
	t.Setenv("BITVOICE_GRAPH_URL", g.URL)
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	if _, _, err := s.Store.Update("test", "emails", 0, func(cf *config.Config) error {
		for i := range cf.Extensions {
			cf.Extensions[i].Email = map[string]string{"100": "front@acme.example", "101": "LEE@acme.example"}[cf.Extensions[i].Number]
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manual, _ := s.Store.PutContact(store.DirContact{Name: "Plumber", Numbers: []store.PhoneNumber{{Kind: "work", Number: "5550199"}}})

	ws := map[string]any{"provider": "microsoft365", "tenantId": "acme.onmicrosoft.com", "clientId": "app", "clientSecret": "secret", "contacts": true, "calendars": true}
	if code, out := c.do("POST", "/api/workspace/check", map[string]any{"provider": "microsoft365", "tenantId": "not a tenant"}); code != 422 || !strings.Contains(string(jsonOf(out)), "system.workspace.tenantId") {
		t.Fatalf("bad settings: %d %v", code, out)
	}
	code, out := c.do("POST", "/api/workspace/check", ws)
	if code != 200 || out["people"] != float64(3) || out["shared"] != float64(1) || out["withNumbers"] != float64(3) || string(jsonOf(out["matched"])) != `["100","101"]` || out["busyTimes"] != float64(1) {
		t.Fatalf("check: %d %v", code, out)
	}
	var cur map[string]any
	_, cur = c.do("GET", "/api/settings", nil)
	cur["system"].(map[string]any)["workspace"] = ws
	if code, out := c.do("PUT", "/api/settings", cur); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	code, out = c.do("POST", "/api/workspace/sync", nil)
	if code != 200 || out["added"] != float64(3) || out["total"] != float64(3) {
		t.Fatalf("sync: %d %v", code, out)
	}
	dir := s.Store.Directory()
	var lee store.DirContact
	for _, d := range dir {
		if d.Name == "Dr Lee" {
			lee = d
		}
	}
	if len(dir) != 4 || lee.Source != "microsoft365" || lee.Company != "Acme Dental" || lee.Numbers[0].Number != "+12125550101" {
		t.Fatalf("directory: %+v", dir)
	}
	// A synced contact is changed at its source.
	if code, out := c.do("PUT", "/api/directory/"+lee.ID, map[string]any{"name": "X", "numbers": []map[string]string{{"kind": "work", "number": "1"}}}); code != 409 || !strings.Contains(out["message"].(string), "Microsoft 365") {
		t.Fatalf("edit synced: %d %v", code, out)
	}
	// The source changes: Lee's number changes, Front Desk leaves; the manual contact stays.
	people = `[{"id":"u1","displayName":"Dr Lee","mail":"lee@acme.example","businessPhones":["+12125550199"],"accountEnabled":true}]`
	_, out = c.do("POST", "/api/workspace/sync", nil)
	if out["updated"] != float64(1) || out["removed"] != float64(1) || out["added"] != float64(0) {
		t.Fatalf("second sync: %v", out)
	}
	ids := map[string]string{}
	for _, d := range s.Store.Directory() {
		ids[d.Name] = d.ID
	}
	if ids["Dr Lee"] != lee.ID || ids["Plumber"] != manual.ID || ids["Front Desk"] != "" {
		t.Fatalf("after the second sync: %v", ids)
	}
	// Calendars: Lee (101) is in a meeting now; Front desk (100) is free.
	s.readCalendars(context.Background())
	away := map[string]string{}
	for _, a := range s.Engine.Aways() {
		away[a.Ext] = a.Reason
	}
	if away["101"] != "Board meeting" || away["100"] != "" {
		t.Fatalf("away: %v", away)
	}
	_, status := c.do("GET", "/api/workspace", nil)
	if status["matched"] != float64(2) || status["provider"] != "microsoft365" {
		t.Fatalf("status: %v", status)
	}
	// Viewers never see the secret.
	_, viewer, _ := s.Store.AddToken("viewer", "viewer", "owner", time.Time{}, false)
	req, _ := http.NewRequest("GET", ts.URL+"/api/settings", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	resp, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), `"clientSecret":"secret"`) {
		t.Fatal("a viewer sees the client secret")
	}
	// Saving without the secret keeps it.
	_, cur = c.do("GET", "/api/settings", nil)
	cur["system"].(map[string]any)["workspace"].(map[string]any)["clientSecret"] = ""
	c.do("PUT", "/api/settings", cur)
	if cfg, _ := s.Store.Config(); cfg.System.Workspace.ClientSecret != "secret" {
		t.Fatal("the secret was lost on save")
	}
}
