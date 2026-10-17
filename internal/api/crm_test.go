package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The CRM tells BitVoice who callers are (cached), the caller card puts that with their history, and
// every call is logged to the webhook, signed, with a retry when the CRM is briefly down.
func TestCRM(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var lookups atomic.Int32
	var mu sync.Mutex
	var logged []map[string]any
	failFirst := atomic.Bool{}
	failFirst.Store(true)
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/lookup"):
			lookups.Add(1)
			if r.Header.Get("Authorization") != "Bearer crm-token" {
				w.WriteHeader(401)
				return
			}
			if r.URL.Query().Get("phone") != "+12125550123" {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(`{"name":"Ana Diaz","company":"Diaz Bakery","url":"javascript:alert(1)","notes":"VIP"}`))
		case r.Method == "POST" && r.URL.Path == "/log":
			b, _ := io.ReadAll(r.Body)
			mac := hmac.New(sha256.New, []byte("log-secret"))
			mac.Write([]byte(r.Header.Get("X-BitVoice-Timestamp") + "."))
			mac.Write(b)
			if r.Header.Get("X-BitVoice-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
				w.WriteHeader(403)
				return
			}
			if failFirst.Swap(false) {
				w.WriteHeader(503)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			logged = append(logged, m)
			mu.Unlock()
		}
	}))
	defer crm.Close()
	_, rev := s.Store.Config()
	m := config.CRM{LookupURL: crm.URL + "/lookup?phone={number}", LookupHeader: "Authorization", LookupSecret: "Bearer crm-token",
		OpenURL: "https://crm.example.com/find?p={number}", LogURL: crm.URL + "/log", LogSecret: "log-secret"}
	if _, _, err := s.Store.Update("test", "crm", rev, func(c *config.Config) error { c.System.CRM = m; return nil }); err != nil {
		t.Fatal(err)
	}
	// Bad settings are refused field by field.
	code, out := admin.do("GET", "/api/settings", nil)
	if code != 200 {
		t.Fatal(code)
	}
	sys := out["system"].(map[string]any)
	sys["crm"] = map[string]any{"lookupUrl": "ftp://x", "openUrl": "https://crm.example.com/", "logUrl": "https://x.example/log", "lookupSecret": "s"}
	if code, out := admin.do("PUT", "/api/settings", out); code != 422 || !hasProblem(out, "system.crm.lookupUrl") || !hasProblem(out, "system.crm.openUrl") ||
		!hasProblem(out, "system.crm.logSecret") || !hasProblem(out, "system.crm.lookupHeader") {
		t.Fatalf("bad CRM settings: %d %v", code, out)
	}

	// The caller card: the CRM's answer (an unsafe link dropped), the open link, history, callbacks.
	_ = s.Store.AddCDR(store.CDR{ID: "c1", Start: time.Now().Add(-time.Hour), From: "+12125550123", To: "100", Disposition: "answered"})
	_, _ = s.Store.AddCallback(store.Callback{Name: "Ana", Number: "+12125550123", When: "today", Reason: "quote"})
	code, out = admin.do("GET", "/api/callers/+12125550123", nil)
	c, _ := out["contact"].(map[string]any)
	if code != 200 || c["name"] != "Ana Diaz" || c["company"] != "Diaz Bakery" || c["url"] != nil || out["calls"].(float64) != 1 ||
		len(out["callbacks"].([]any)) != 1 || out["openUrl"] != "https://crm.example.com/find?p=%2B12125550123" {
		t.Fatalf("card %d %v", code, out)
	}
	admin.do("GET", "/api/callers/+12125550123", nil)
	if n := lookups.Load(); n != 1 {
		t.Fatalf("the CRM was asked %d times; the answer should be cached", n)
	}
	if code, out := admin.do("GET", "/api/callers/100", nil); code != 200 || out["contact"].(map[string]any)["source"] != "extension" || out["openUrl"] != nil {
		t.Fatalf("an extension's card %v", out)
	}
	if _, out := admin.do("GET", "/api/callers/+19995550000", nil); out["contact"] != nil {
		t.Fatalf("unknown caller %v", out)
	}

	// Calls are logged, signed; the first attempt fails and is tried again.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed(t, s, func() { go s.RunCRM(ctx) })
	cdr := store.CDR{ID: "c2", Start: time.Now(), From: "+12125550123", To: "100", Disposition: "answered"}
	s.Bus.Publish("call.logged", cdr)
	deadline := time.Now().Add(6 * time.Second)
	for {
		mu.Lock()
		n := len(logged)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the call was not logged after a retry")
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	got := logged[0]
	mu.Unlock()
	if got["event"] != "call.ended" || got["data"].(map[string]any)["call"].(map[string]any)["id"] != "c2" {
		t.Fatalf("logged %v", got)
	}
	if s.crmProblem() != "" {
		t.Fatalf("health still reports %q", s.crmProblem())
	}

	// The test buttons: a lookup, and a test event to the log address.
	_, cur := admin.do("GET", "/api/settings", nil)
	form := cur["system"].(map[string]any)["crm"].(map[string]any) // the secrets come back blank... for admins they are kept
	if code, out := admin.do("POST", "/api/crm/test", map[string]any{"crm": form, "number": "+12125550123", "what": "lookup"}); code != 200 || out["ok"] != true || !strings.Contains(out["message"].(string), "Ana Diaz") {
		t.Fatalf("test lookup %d %v", code, out)
	}
	form["lookupSecret"] = ""
	if code, out := admin.do("POST", "/api/crm/test", map[string]any{"crm": form, "number": "+12125550123", "what": "lookup"}); code != 200 || out["ok"] != true {
		t.Fatalf("an empty secret in the form should keep the saved one: %d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/crm/test", map[string]any{"crm": form, "what": "log"}); code != 200 || out["ok"] != true {
		t.Fatalf("test log %d %v", code, out)
	}
	form["logSecret"] = "wrong"
	if code, out := admin.do("POST", "/api/crm/test", map[string]any{"crm": form, "what": "log"}); code != 200 || out["ok"] != false || !strings.Contains(out["message"].(string), "403") {
		t.Fatalf("a wrong secret should fail: %d %v", code, out)
	}

	// A CRM that stays down shows on Health.
	crm.Close()
	_ = s.deliver(context.Background(), m, "call.ended", map[string]any{}, []time.Duration{0})
	_, h := admin.do("GET", "/api/health", nil)
	if !strings.Contains(toJSON(h), "Calls are not reaching the CRM") {
		t.Fatalf("health %v", h)
	}
}
