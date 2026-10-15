package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/inbox"
)

func metaSign(b []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(b)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// The inbox: a signed WhatsApp webhook opens a conversation (a forged one is refused, a repeat is
// kept once), the automatic greeting and an agent's reply go out through the Cloud API; a web chat
// visitor starts a chat from a listed site, reads the agent's answer, and another site is refused.
func TestInbox(t *testing.T) {
	var mu sync.Mutex
	var sent []map[string]any
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		mu.Lock()
		sent = append(sent, m)
		mu.Unlock()
		w.Write([]byte(`{"messages":[{"id":"wamid.out"}]}`))
	}))
	defer meta.Close()
	inbox.GraphBase = meta.URL
	_, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	_, cfg := admin.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	sys["inbox"] = map[string]any{"greeting": "Thanks, we will be right with you.",
		"whatsApp": map[string]any{"enabled": true, "phoneNumberId": "106540352242922", "token": "TOKEN", "appSecret": "appsecret"},
		"webChat":  map[string]any{"enabled": true, "origins": []string{"https://www.example.com"}}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	_, cfg = admin.do("GET", "/api/settings", nil)
	verify := cfg["system"].(map[string]any)["inbox"].(map[string]any)["whatsApp"].(map[string]any)["verifyToken"].(string)
	if len(verify) < 12 {
		t.Fatalf("no verify token made: %q", verify)
	}
	resp, _ := http.Get(ts.URL + "/hooks/whatsapp?hub.mode=subscribe&hub.verify_token=" + verify + "&hub.challenge=777")
	if b, _ := io.ReadAll(resp.Body); string(b) != "777" {
		t.Fatalf("challenge %q", b)
	}
	hook := []byte(`{"entry":[{"changes":[{"value":{"metadata":{"phone_number_id":"106540352242922"},"contacts":[{"profile":{"name":"Ann Smith"},"wa_id":"447700900123"}],
		"messages":[{"from":"447700900123","id":"wamid.in1","timestamp":"1700000000","type":"text","text":{"body":"Open on Sunday?"}}]}}]}]}`)
	post := func(sig string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/hooks/whatsapp", bytes.NewReader(hook))
		req.Header.Set("X-Hub-Signature-256", sig)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := post(metaSign(hook, "wrong")); code != 401 {
		t.Fatalf("forged webhook: %d", code)
	}
	if post(metaSign(hook, "appsecret")) != 200 || post(metaSign(hook, "appsecret")) != 200 {
		t.Fatal("webhook refused")
	}
	var conv map[string]any
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/inbox", nil)
		r, _ := admin.http.Do(req)
		var arr []map[string]any
		json.NewDecoder(r.Body).Decode(&arr)
		r.Body.Close()
		for _, c := range arr {
			if c["channel"] == "whatsapp" {
				conv = c
			}
		}
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if conv != nil && n >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conv == nil || conv["name"] != "Ann Smith" || conv["contact"] != "+447700900123" {
		t.Fatalf("conversation %v", conv)
	}
	id := conv["id"].(string)
	var msgs []any
	for end := time.Now().Add(8 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		_, thread := admin.do("GET", "/api/inbox/"+id, nil)
		if msgs = thread["messages"].([]any); len(msgs) >= 2 {
			break // the greeting is kept once Meta has taken it
		}
	}
	if len(msgs) != 2 || msgs[0].(map[string]any)["text"] != "Open on Sunday?" || msgs[1].(map[string]any)["by"] != "auto" {
		t.Fatalf("messages (the repeat kept once, the greeting sent) %v", msgs)
	}
	if code, out := admin.do("POST", "/api/inbox/"+id+"/reply", map[string]any{"text": "Yes, 10 to 4."}); code != 201 || out["status"] != "sent" {
		t.Fatalf("reply %d %v", code, out)
	}
	mu.Lock()
	last := sent[len(sent)-1]
	mu.Unlock()
	if last["to"] != "447700900123" || last["text"].(map[string]any)["body"] != "Yes, 10 to 4." {
		t.Fatalf("sent %v", last)
	}
	if code, _ := admin.do("POST", "/api/inbox/"+id, map[string]any{"state": "closed", "read": true}); code != 204 {
		t.Fatal("close")
	}

	// Web chat from a listed site.
	chat := func(method, path, origin, auth string, body any) (int, map[string]any, http.Header) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Origin", origin)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		} else if method == "POST" {
			req.Header.Set("X-BitVoice", "1")
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out, r.Header
	}
	pre, _ := http.NewRequest("OPTIONS", ts.URL+"/api/webchat/start", nil)
	pre.Header.Set("Origin", "https://www.example.com")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pr, _ := http.DefaultClient.Do(pre)
	if pr.StatusCode != 204 || pr.Header.Get("Access-Control-Allow-Origin") != "https://www.example.com" {
		t.Fatalf("preflight %d %v", pr.StatusCode, pr.Header)
	}
	if code, _, _ := chat("POST", "/api/webchat/start", "https://evil.example.org", "", map[string]any{"text": "hi"}); code != 403 {
		t.Fatalf("an unlisted site: %d", code)
	}
	code, st, h := chat("POST", "/api/webchat/start", "https://www.example.com", "", map[string]any{"name": "Bob", "text": "Do you deliver?", "page": "https://www.example.com/shop?x=1"})
	if code != 201 || h.Get("Access-Control-Allow-Origin") != "https://www.example.com" {
		t.Fatalf("start %d %v", code, st)
	}
	cid, tok := st["id"].(string), st["token"].(string)
	if code, _ := admin.do("POST", "/api/inbox/"+cid+"/reply", map[string]any{"text": "We do, within 10 miles."}); code != 201 {
		t.Fatalf("reply to the web chat %d", code)
	}
	code, got, _ := chat("GET", "/api/webchat/"+cid+"/messages?after=0", "https://www.example.com", tok, nil)
	ms := got["messages"].([]any)
	var texts []string
	for _, m := range ms {
		texts = append(texts, m.(map[string]any)["text"].(string))
	}
	if code != 200 || !strings.Contains(strings.Join(texts, "|"), "We do, within 10 miles.") || texts[0] != "Do you deliver?" {
		t.Fatalf("visitor sees %d %v", code, texts)
	}
	if code, _, _ := chat("GET", "/api/webchat/"+cid+"/messages", "https://www.example.com", "wrong", nil); code != 404 {
		t.Fatalf("a wrong key: %d", code)
	}
	if code, _, _ := chat("POST", "/api/webchat/"+cid+"/messages", "https://www.example.com", tok, map[string]any{"text": "Great, thanks"}); code != 204 {
		t.Fatalf("visitor's second message: %d", code)
	}
	_, th := admin.do("GET", "/api/inbox/"+cid, nil)
	c := th["conversation"].(map[string]any)
	if c["page"] != "https://www.example.com/shop" || c["name"] != "Bob" || c["tokenHash"] != nil {
		t.Fatalf("conversation %v", c)
	}
	resp, _ = http.Get(ts.URL + "/webchat.js")
	if b, _ := io.ReadAll(resp.Body); !strings.Contains(string(b), "attachShadow") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatal("the chat box script")
	}
}
