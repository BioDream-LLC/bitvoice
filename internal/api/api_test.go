package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	code string
}

func newServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := storetest.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Config()
	cfg.System.RTPMin, cfg.System.RTPMax = 34000, 34200
	bus := events.New()
	eng := pbx.New(cfg, bus, st, nil)
	eng.Mail = st
	if err := eng.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	st.OnChange(eng.Apply)
	s := &Server{Store: st, Engine: eng, Bus: bus, Version: "test", SetupCode: "123456", Trace: NewTraceBuffer(100), DataDir: t.TempDir()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any, headers ...string) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-BitVoice", "1")
	if c.code != "" {
		req.Header.Set("X-Setup-Code", c.code)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func setupBody() map[string]any {
	return map[string]any{
		"organisation": "Acme Dental", "purpose": "office", "country": "us",
		"admin":      map[string]any{"username": "Owner", "name": "Practice Owner", "password": "a long enough password"},
		"extensions": []map[string]any{{"number": "100", "name": "Front desk"}, {"number": "101", "name": "Dr Lee"}},
		"trunk":      map[string]any{"host": "sip.example.net", "username": "acct", "password": "pw", "register": true},
		"inbound":    "all",
	}
}

func TestSetupThenManage(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)

	if code, _ := c.do("GET", "/api/extensions", nil); code != 401 {
		t.Fatalf("unauthenticated list: %d", code)
	}
	code, st := c.do("GET", "/api/setup/status", nil)
	if code != 200 || st["complete"] != false || st["allowed"] != true {
		t.Fatalf("setup status %d %v", code, st)
	}
	if code, out := c.do("POST", "/api/setup/complete", setupBody()); code != 200 || out["role"] != "admin" {
		t.Fatalf("setup: %d %v", code, out)
	}
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 409 {
		t.Fatalf("second setup allowed: %d", code)
	}
	cfg, rev := s.Store.Config()
	if len(cfg.Extensions) != 2 || len(cfg.Trunks) != 1 || len(cfg.RingGroups) != 1 || cfg.InboundRoutes[0].Destination.Type != "ringgroup" {
		t.Fatalf("setup built %+v", cfg)
	}
	if len(cfg.OutboundRoutes) != 2 || cfg.OutboundRoutes[0].Name != "Emergency" {
		t.Fatalf("emergency route missing: %+v", cfg.OutboundRoutes)
	}

	// Signed in by setup: add an extension.
	code, out := c.do("POST", "/api/extensions", map[string]any{"number": "102", "name": "Hygienist", "enabled": true},
		"If-Match", strconv.Itoa(rev))
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	item := out["item"].(map[string]any)
	id, secret := item["id"].(string), item["secret"].(string)
	if len(secret) != 20 {
		t.Fatalf("no generated secret: %v", item)
	}
	// A stale revision is refused.
	if code, _ := c.do("PUT", "/api/extensions/"+id, map[string]any{"number": "102", "name": "X"}, "If-Match", strconv.Itoa(rev)); code != 409 {
		t.Fatalf("stale write: %d", code)
	}
	// Editing without a secret keeps the secret.
	code, out = c.do("PUT", "/api/extensions/"+id, map[string]any{"number": "102", "name": "Hygiene", "enabled": true})
	if code != 200 || out["item"].(map[string]any)["secret"] != secret {
		t.Fatalf("edit changed the secret: %d %v", code, out)
	}
	// Validation problems come back per field.
	code, out = c.do("POST", "/api/extensions", map[string]any{"number": "100", "name": "Dup"})
	if code != 422 || !strings.Contains(toJSON(out["problems"]), "already used") {
		t.Fatalf("duplicate: %d %v", code, out)
	}
	// Missing CSRF header is refused.
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/extensions/"+id, nil)
	if resp, _ := c.http.Do(req); resp.StatusCode != 403 {
		t.Fatalf("no CSRF header: %d", resp.StatusCode)
	}

	// A viewer sees no secrets and cannot change anything.
	if code, _ := c.do("POST", "/api/users", map[string]any{"username": "viewer", "name": "V", "role": "viewer", "password": "viewer password"}); code != 200 {
		t.Fatalf("create viewer: %d", code)
	}
	v := newClient(t, ts.URL)
	if code, _ := v.do("POST", "/api/auth/login", map[string]any{"username": "viewer", "password": "viewer password"}); code != 200 {
		t.Fatalf("viewer login %d", code)
	}
	_, list := v.do("GET", "/api/extensions", nil)
	for _, x := range list["items"].([]any) {
		if x.(map[string]any)["secret"] != "" {
			t.Fatal("viewer saw a SIP secret")
		}
	}
	if code, _ := v.do("DELETE", "/api/extensions/"+id, nil); code != 403 {
		t.Fatalf("viewer delete: %d", code)
	}

	// History and restore.
	_, h := c.do("GET", "/api/history", nil)
	if n := len(h["items"].([]any)); n < 4 {
		t.Fatalf("history has %d entries", n)
	}
	if code, _ := c.do("POST", "/api/history/2/restore", nil); code != 200 {
		t.Fatalf("restore %d", code)
	}
	cfg, _ = s.Store.Config()
	if cfg.FindExtension("102") != nil {
		t.Fatal("restore did not remove 102")
	}
	if code, out := c.do("GET", "/api/health", nil); code != 200 || len(out["items"].([]any)) == 0 {
		t.Fatalf("health %d", code)
	}
}

func TestSetupNeedsCodeFromElsewhere(t *testing.T) {
	s, _ := newServer(t)
	h := s.Handler()
	req := httptest.NewRequest("POST", "/api/setup/complete", strings.NewReader(toJSON(setupBody())))
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("X-BitVoice", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("remote setup without code: %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/api/setup/complete", strings.NewReader(toJSON(setupBody())))
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("X-BitVoice", "1")
	req.Header.Set("X-Setup-Code", "123456")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("remote setup with code: %d %s", rec.Code, rec.Body)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = config.Default

// A restart keeps people signed in; signing out ends it for good; the cookie itself is never stored.
func TestSessionsSurviveRestart(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	if code, _ := c.do("GET", "/api/auth/me", nil); code != 200 {
		t.Fatal(code)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Store.Dir(), "sessions.json"))
	u, _ := url.Parse(ts.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if strings.Contains(string(raw), ck.Value) {
			t.Fatal("the session cookie is stored in the clear")
		}
	}
	again := &Server{Store: s.Store, Engine: s.Engine, Bus: s.Bus, Version: "test", Trace: NewTraceBuffer(10)}
	ts2 := httptest.NewServer(again.Handler())
	defer ts2.Close()
	c.base = ts2.URL
	if code, _ := c.do("GET", "/api/auth/me", nil); code != 200 {
		t.Fatalf("signed out by the restart: %d", code)
	}
	c.do("POST", "/api/auth/logout", nil)
	third := &Server{Store: s.Store, Engine: s.Engine, Bus: s.Bus, Version: "test", Trace: NewTraceBuffer(10)}
	ts3 := httptest.NewServer(third.Handler())
	defer ts3.Close()
	c.base = ts3.URL
	if code, _ := c.do("GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("signed out session came back: %d", code)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	_, ts := newServer(t)
	res, err := http.Get(ts.URL + "/api/no-such-thing")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 || !strings.Contains(res.Header.Get("Content-Type"), "json") {
		t.Fatalf("got %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

func TestMailHeadersCannotBeInjected(t *testing.T) {
	if _, err := buildMail("a@example.com", "b@example.com\r\nBcc: c@evil.test", "s", "b", "f.txt", "text/plain", nil); err == nil {
		t.Fatal("a line break in To was accepted")
	}
	if _, err := buildMail(`"Phones" <a@example.com>`, "b@example.com", "s", "b", "f.txt", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerName(t *testing.T) {
	cfg := config.Default()
	cfg.RingGroups = []config.RingGroup{{Name: "Front"}}
	if got := ownerName(&cfg, "ringGroups[0].members[1]"); got != "ring group Front" {
		t.Fatal(got)
	}
	err := stillUsed(config.Problems{{Field: "ringGroups[0].members[1]", Message: "no extension 101"}, {Field: "ringGroups[0].noAnswer"}}, "extension 101", &cfg)
	if err.Error() != "extension 101 is still used by ring group Front. Change those first, then remove it" {
		t.Fatal(err)
	}
}

func TestCallGeneratorChecks(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	field := func(body map[string]any) string {
		code, out := c.do("POST", "/api/loadtest", body)
		if code != 422 {
			return "accepted"
		}
		var fs []string
		for _, p := range out["problems"].([]any) {
			fs = append(fs, p.(map[string]any)["field"].(string))
		}
		return strings.Join(fs, ",")
	}
	ok := map[string]any{"from": "100", "to": "*43", "calls": 1, "rate": 1, "concurrent": 1, "holdSeconds": 0}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, tc := range map[string]struct {
		body map[string]any
		want string
	}{
		"nothing":   {map[string]any{}, "from,to"},
		"emergency": {with("to", "911"), "to"},
		"a trunk":   {with("to", "5551234"), "to"},
		"no calls":  {with("calls", 0), "calls"},
		"too fast":  {with("rate", 500), "rate"},
		"same":      {with("answerAs", "100"), "answerAs"},
	} {
		if got := field(tc.body); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	if code, _ := c.do("POST", "/api/loadtest", ok); code != 200 {
		t.Fatalf("a good plan was refused: %d", code)
	}
}
