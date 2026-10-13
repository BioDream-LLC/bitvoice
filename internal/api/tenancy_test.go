package api

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/pbx"
)

func TestTenancy(t *testing.T) {
	s, _ := newServer(t)
	sw := pbx.NewSwitch(s.Engine)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// On PostgreSQL each tenant is a schema of the main database.
	spec := ""
	if s.Store.DB().Postgres {
		spec = s.Store.Spec() // a fresh database for this test
	}
	ty := &Tenancy{Root: s, Switch: sw, DataDir: t.TempDir(), DBSpec: spec, Log: slog.Default(), Version: "test"}
	s.Tenancy = ty
	ts := httptest.NewServer(ty.Wrap(s.Handler()))
	t.Cleanup(ts.Close)
	ty.Start(ctx)
	t.Cleanup(ty.Stop)

	root := newClient(t, ts.URL)
	if code, _ := root.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	newTenant := map[string]any{"id": "acme", "name": "Acme Dental", "limits": map[string]any{"extensions": 2},
		"admin": map[string]any{"username": "boss", "name": "Acme boss", "password": "acme long password"}}
	if code, body := root.do("POST", "/api/tenants", map[string]any{"id": "API", "name": "", "admin": map[string]any{}}); code != 422 {
		t.Fatalf("a bad tenant: %d %v", code, body)
	}
	if code, body := root.do("POST", "/api/tenants", newTenant); code != 201 {
		t.Fatalf("create tenant: %d %v", code, body)
	}
	if code, _ := root.do("POST", "/api/tenants", newTenant); code != 422 {
		t.Fatalf("the same tenant twice: %d", code)
	}
	// Acme's administrator signs in at /t/acme/ and sees only Acme.
	acme := newClient(t, ts.URL+"/t/acme")
	if code, _ := acme.do("POST", "/api/auth/login", map[string]any{"username": "boss", "password": "acme long password"}); code != 200 {
		t.Fatalf("acme sign-in: %d", code)
	}
	if code, body := acme.do("GET", "/api/extensions", nil); code != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("acme sees extensions it does not have: %d %v", code, body)
	}
	for i, n := range []string{"200", "201", "202"} {
		code, body := acme.do("POST", "/api/extensions", map[string]any{"number": n, "name": "Desk " + n, "enabled": true, "secret": "a-long-secret-123"})
		if i < 2 && code != 201 {
			t.Fatalf("acme extension %s: %d %v", n, code, body)
		}
		if i == 2 && code != 422 {
			t.Fatalf("over the extension allowance: %d %v", code, body)
		}
	}
	// The main system is untouched, and the main system's sign-in does not reach Acme.
	if code, body := root.do("GET", "/api/extensions", nil); code != 200 || len(body["items"].([]any)) != 2 {
		t.Fatalf("main extensions: %d %v", code, body)
	}
	anon := newClient(t, ts.URL+"/t/acme")
	if code, _ := anon.do("POST", "/api/auth/login", map[string]any{"username": "Owner", "password": "a long enough password"}); code != 401 {
		t.Fatalf("the main administrator signed in to acme with the main password: %d", code)
	}
	// Acme's administrator cannot reach the server's own screens or the tenant list.
	for _, p := range []string{"/api/tenants", "/api/database", "/api/sip-trace", "/api/system/upgrade"} {
		if code, _ := acme.do("GET", p, nil); code != 403 && code != 404 {
			t.Errorf("acme reached %s: %d", p, code)
		}
	}
	// The provider opens Acme's interface from the main system.
	code, body := root.do("POST", "/api/tenants/acme/open", nil)
	if code != 200 || body["url"] != "/t/acme/" {
		t.Fatalf("open: %d %v", code, body)
	}
	provider := &client{t: t, base: ts.URL + "/t/acme", http: root.http}
	if code, _ := provider.do("GET", "/api/extensions", nil); code != 200 {
		t.Fatalf("the provider could not use acme's interface: %d", code)
	}
	// Suspended: nobody uses Acme's interface.
	if code, _ := root.do("PUT", "/api/tenants/acme", map[string]any{"name": "Acme Dental", "suspended": true}); code != 200 {
		t.Fatalf("suspend: %d", code)
	}
	if code, _ := acme.do("GET", "/api/extensions", nil); code != 403 {
		t.Fatalf("a suspended tenant's interface: %d", code)
	}
	if !ty.get("acme").eng.Suspended.Load() {
		t.Fatal("the suspended tenant's engine still takes calls")
	}
	code, body = root.do("GET", "/api/tenants", nil)
	if code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, body)
	}
	if code, _ := root.do("DELETE", "/api/tenants/acme", nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := acme.do("GET", "/api/auth/me", nil); code != 404 {
		t.Fatalf("a removed tenant still answers: %d", code)
	}
}
