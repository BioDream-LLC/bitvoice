package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestTexting(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	// A fake TextMagic: checks the account and records what was sent.
	var mu sync.Mutex
	var sent []map[string]string
	tm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, k, _ := r.BasicAuth()
		if r.URL.Path != "/api/v2/messages" || u != "acme" || k != "tm-key" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "Unauthorized"})
			return
		}
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["phones"] == "15550000000" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"message": "Validation Failed", "errors": map[string]any{"fields": map[string][]string{"phones": {"Invalid phone number"}}}})
			return
		}
		mu.Lock()
		sent = append(sent, in)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": 77, "messageId": 991, "href": "/api/v2/messages/991"})
	}))
	defer tm.Close()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "texting", rev, func(c *config.Config) error {
		c.System.Texting = config.Texting{
			Providers: []config.SMSProvider{{ID: "tm", Name: "TextMagic", Kind: "textmagic", Username: "acme", Key: "tm-key", BaseURL: tm.URL}},
			Numbers:   []config.SMSNumber{{Number: "+12125550100", Provider: "tm", Name: "Main line", Notify: []string{"100"}}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Store.Config()
	secret := cfg.System.Texting.Providers[0].HookSecret
	if len(secret) < 20 {
		t.Fatalf("hook secret %q", secret)
	}
	// Sending.
	if code, out := admin.do("POST", "/api/texts", map[string]any{"from": "+19999999999", "to": "212", "text": ""}); code != 422 || len(out["problems"].([]any)) != 3 {
		t.Fatalf("validation %d %v", code, out)
	}
	code, out := admin.do("POST", "/api/texts", map[string]any{"from": "+12125550100", "to": "+1 312 555 0142", "text": "Your appointment is at 9 tomorrow."})
	if code != 201 || out["status"] != "sent" || out["providerId"] != "991" {
		t.Fatalf("send %d %v", code, out)
	}
	if len(sent) != 1 || sent[0]["phones"] != "13125550142" || sent[0]["from"] != "12125550100" || sent[0]["text"] != "Your appointment is at 9 tomorrow." {
		t.Fatalf("TextMagic got %v", sent)
	}
	if code, out := admin.do("POST", "/api/texts", map[string]any{"from": "+12125550100", "to": "+15550000000", "text": "hi"}); code != 502 || !strings.Contains(out["message"].(string), "Invalid phone number") {
		t.Fatalf("provider refusal %d %v", code, out)
	}
	// Receiving: the wrong secret is a 404; the right one stores the text.
	hook := func(secret, body string) int {
		resp, err := http.Post(ts.URL+"/hooks/sms/tm/"+secret, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	reply := `{"id":5,"sender":"13125550142","receiver":"12125550100","messageTime":"2026-10-10T15:00:00+0000","text":"Thanks, see you then"}`
	if code := hook("wrong", reply); code != 404 {
		t.Fatalf("wrong secret: %d", code)
	}
	if code := hook(secret, reply); code != 204 {
		t.Fatalf("inbound: %d", code)
	}
	_, list := admin.do("GET", "/api/texts", nil)
	convs := list["items"].([]any)
	var conv map[string]any
	for _, c := range convs {
		if c.(map[string]any)["other"] == "+13125550142" {
			conv = c.(map[string]any)
		}
	}
	if conv == nil || conv["unread"].(float64) != 1 || conv["count"].(float64) != 2 {
		t.Fatalf("conversations %v", convs)
	}
	admin.do("POST", "/api/texts/read", map[string]any{"our": "+12125550100", "other": "+13125550142"})
	// STOP opts them out; sending then refuses, START lets it again.
	hook(secret, `{"id":6,"sender":"13125550142","receiver":"12125550100","text":"STOP"}`)
	if code, out := admin.do("POST", "/api/texts", map[string]any{"from": "+12125550100", "to": "+13125550142", "text": "One more thing"}); code != 409 || !strings.Contains(out["message"].(string), "STOP") {
		t.Fatalf("after STOP %d %v", code, out)
	}
	hook(secret, `{"id":7,"sender":"13125550142","receiver":"12125550100","text":"start"}`)
	if code, _ := admin.do("POST", "/api/texts", map[string]any{"from": "+12125550100", "to": "+13125550142", "text": "Welcome back"}); code != 201 {
		t.Fatalf("after START %d", code)
	}
	_, th := admin.do("GET", "/api/texts/thread?our=%2B12125550100&other=%2B13125550142", nil)
	if items := th["items"].([]any); len(items) != 5 {
		t.Fatalf("thread %v", th)
	}
}
