package api

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestStaffPortal(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	if _, _, err := s.Store.Update("test", "PIN", 0, func(c *config.Config) error {
		x := c.FindExtension("101")
		x.Voicemail.Enabled, x.Voicemail.PIN, x.Outbound = true, "2468", true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Engine.Apply(s.config())
	staff := newClient(t, ts.URL)
	if code, _ := staff.do("POST", "/api/portal/login", map[string]any{"extension": "101", "pin": "1111"}); code != 401 {
		t.Fatalf("wrong PIN: %d", code)
	}
	if code, _ := staff.do("POST", "/api/portal/login", map[string]any{"extension": "100", "pin": ""}); code != 401 {
		t.Fatalf("an extension without a PIN signed in: %d", code)
	}
	if code, _ := staff.do("POST", "/api/portal/login", map[string]any{"extension": "101", "pin": "2468"}); code != 200 {
		t.Fatalf("portal sign-in: %d", code)
	}
	// The portal reaches only the person's own settings.
	for _, path := range []string{"/api/extensions", "/api/settings", "/api/cdr", "/api/voicemail"} {
		if code, _ := staff.do("GET", path, nil); code != 403 {
			t.Errorf("portal session read %s: %d", path, code)
		}
	}
	code, me := staff.do("GET", "/api/me", nil)
	if code != 200 || me["number"] != "101" {
		t.Fatalf("me: %d %v", code, me)
	}
	if code, _ := staff.do("PUT", "/api/me/divert", map[string]any{"all": "100", "dnd": true}); code != 204 {
		t.Fatalf("own forwarding: %d", code)
	}
	if d := s.Engine.DivertOf("101"); d.All != "100" || !s.Engine.DND("101") {
		t.Fatalf("forwarding not set: %+v", d)
	}
	if code, _ := staff.do("PUT", "/api/me/divert", map[string]any{"all": "101"}); code != 422 {
		t.Fatalf("forwarding to itself: %d", code)
	}
	code, body := staff.do("PUT", "/api/me/settings", map[string]any{"followMe": map[string]any{"enabled": true, "numbers": []string{"x"}}})
	if code != 422 {
		t.Fatalf("bad follow-me number accepted: %d %v", code, body)
	}
	if code, _ := staff.do("PUT", "/api/me/settings", map[string]any{
		"followMe": map[string]any{"enabled": true, "numbers": []string{"2125550123"}, "ringSeconds": 15},
		"speedDials": []map[string]any{{"code": "1", "number": "100", "name": "Front desk"}}}); code != 200 {
		t.Fatalf("own settings: %d", code)
	}
	cfg := s.config()
	if x := cfg.FindExtension("101"); len(x.FollowMe.Numbers) != 1 || len(x.SpeedDials) != 1 {
		t.Fatalf("own settings not saved: %+v", x)
	}
	if code, _ := staff.do("PUT", "/api/me/pin", map[string]any{"old": "0000", "new": "1357"}); code != 403 {
		t.Fatalf("PIN change with the wrong PIN: %d", code)
	}
	if code, _ := staff.do("PUT", "/api/me/pin", map[string]any{"old": "2468", "new": "1357"}); code != 204 {
		t.Fatalf("PIN change: %d", code)
	}
	if code, _ := staff.do("GET", "/api/me/voicemail", nil); code != 200 {
		t.Fatalf("own voicemail: %d", code)
	}
	if code, _ := staff.do("GET", "/api/me/calls", nil); code != 200 {
		t.Fatalf("own calls: %d", code)
	}
	// An administrator's session cannot use the portal routes as somebody.
	if code, _ := admin.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("admin on /api/me: %d", code)
	}
}
