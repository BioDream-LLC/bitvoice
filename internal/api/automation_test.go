package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func bearer(t *testing.T, base, token, method, path string, body any) (int, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTokensOpenAPIAndGit(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	code, tok := admin.do("POST", "/api/tokens", map[string]any{"name": "Terraform", "role": "admin", "days": 30})
	if code != 200 || !strings.HasPrefix(tok["secret"].(string), "bv_") {
		t.Fatalf("token %d %v", code, tok)
	}
	secret := tok["secret"].(string)
	_, viewer := admin.do("POST", "/api/tokens", map[string]any{"name": "Wallboard", "role": "viewer"})
	// A token works without a session or the X-BitVoice header.
	if code, out := bearer(t, ts.URL, secret, "POST", "/api/extensions", map[string]any{"number": "150", "name": "From Terraform", "enabled": true}); code != 201 {
		t.Fatalf("create by token %d %v", code, out)
	}
	// Read one item back, as Terraform does after creating it.
	_, list := bearer(t, ts.URL, secret, "GET", "/api/extensions", nil)
	var made string
	for _, it := range list["items"].([]any) {
		if it.(map[string]any)["number"] == "150" {
			made = it.(map[string]any)["id"].(string)
		}
	}
	if code, one := bearer(t, ts.URL, secret, "GET", "/api/extensions/"+made, nil); code != 200 || one["item"].(map[string]any)["name"] != "From Terraform" {
		t.Fatalf("read one %d %v", code, one)
	}
	if code, _ := bearer(t, ts.URL, secret, "GET", "/api/extensions/nope", nil); code != 404 {
		t.Fatal("missing item not 404")
	}
	if code, _ := bearer(t, ts.URL, viewer["secret"].(string), "POST", "/api/extensions", map[string]any{"number": "151", "name": "x", "enabled": true}); code != 403 {
		t.Fatalf("viewer token wrote: %d", code)
	}
	if code, _ := bearer(t, ts.URL, viewer["secret"].(string), "GET", "/api/extensions", nil); code != 200 {
		t.Fatal("viewer token cannot read")
	}
	if code, _ := bearer(t, ts.URL, secret, "POST", "/api/tokens", map[string]any{"name": "more", "role": "admin"}); code != 403 {
		t.Fatal("a token made a token")
	}
	if code, _ := bearer(t, ts.URL, "bv_nope", "GET", "/api/extensions", nil); code != 401 {
		t.Fatal("a wrong token was accepted")
	}
	id := tok["token"].(map[string]any)["id"].(string)
	if code, _ := admin.do("DELETE", "/api/tokens/"+id, nil); code != 204 {
		t.Fatal("revoke")
	}
	if code, _ := bearer(t, ts.URL, secret, "GET", "/api/extensions", nil); code != 401 {
		t.Fatal("a revoked token still works")
	}

	// The OpenAPI description lists the collections with their schemas.
	resp, _ := http.Get(ts.URL + "/api/openapi.json")
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	resp.Body.Close()
	paths := doc["paths"].(map[string]any)
	ext := paths["/api/extensions"].(map[string]any)
	if doc["openapi"] != "3.1.0" || ext["post"] == nil || paths["/api/extensions/{id}"] == nil || doc["components"].(map[string]any)["schemas"].(map[string]any)["Extension"] == nil {
		t.Fatalf("openapi paths %d", len(paths))
	}

	// Git: export without secrets, change a name in the "repository", sync it in.
	req, _ := http.NewRequest("GET", ts.URL+"/api/config/export?secrets=0", nil)
	req.Header.Set("X-BitVoice", "1")
	resp, _ = admin.http.Do(req)
	var file config.Config
	_ = json.NewDecoder(resp.Body).Decode(&file)
	resp.Body.Close()
	if file.Extensions[0].Secret != "" {
		t.Fatal("export for Git has secrets")
	}
	file.Extensions[0].Name = "Renamed in Git"
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-token" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(file)
	}))
	defer repo.Close()
	_, _, err := s.Store.Update("test", "git", 0, func(c *config.Config) error {
		c.System.GitOps = config.GitOps{URL: repo.URL, Token: "gh-token", EveryMinutes: 5}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.Config()
	_, st := admin.do("POST", "/api/gitops/sync", nil)
	if st["ok"] != true || len(st["changes"].([]any)) == 0 {
		t.Fatalf("report %v", st)
	}
	if c, _ := s.Store.Config(); c.Extensions[0].Name == "Renamed in Git" {
		t.Fatal("applied while only reporting")
	}
	_, _, _ = s.Store.Update("test", "git", 0, func(c *config.Config) error { c.System.GitOps.Apply = true; return nil })
	_, st = admin.do("POST", "/api/gitops/sync", nil)
	after, _ := s.Store.Config()
	if st["ok"] != true || after.Extensions[0].Name != "Renamed in Git" || after.Extensions[0].Secret != before.Extensions[0].Secret || after.Extensions[0].Secret == "" {
		t.Fatalf("applied %v: %q secret kept %v", st, after.Extensions[0].Name, after.Extensions[0].Secret == before.Extensions[0].Secret)
	}
}

// A trunk password written as a vault reference reaches the engine resolved, and is stored as written.
func TestVaultReferences(t *testing.T) {
	s, _ := newServer(t)
	cfg := config.Default()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "T", Host: "sip.example.com", Password: "aws-sm:us-east-1:missing"}}
	got := s.Resolved(cfg)
	if got.Trunks[0].Password != "" {
		t.Fatalf("an unreadable reference was used as the password: %q", got.Trunks[0].Password)
	}
	if len(s.vaultProblems()) != 1 || !strings.Contains(s.vaultProblems()[0], "trunks[0].password") {
		t.Fatalf("problems %v", s.vaultProblems())
	}
	if cfg.Trunks[0].Password != "aws-sm:us-east-1:missing" {
		t.Fatal("the stored reference was changed")
	}
}
