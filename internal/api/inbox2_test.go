package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/inbox"
)

// After WhatsApp's 24 hours only templates go: the conversation says so, the templates are listed
// and one is sent with its values; a wrong count of values is refused before Meta is asked.
func TestInboxTemplates(t *testing.T) {
	var mu sync.Mutex
	var sent []map[string]any
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`{"data":[{"name":"order_ready","language":"en_US","status":"APPROVED","category":"UTILITY","components":[{"type":"BODY","text":"Hi {{1}}, order {{2}} is ready."}]}]}`))
			return
		}
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
	cfg["system"].(map[string]any)["inbox"] = map[string]any{"whatsApp": map[string]any{"enabled": true, "phoneNumberId": "106540352242922", "businessAccountId": "abc", "token": "TOKEN", "appSecret": "appsecret"}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 422 || len(out["problems"].([]any)) != 1 {
		t.Fatalf("a business account ID with letters: %d %v", code, out)
	}
	cfg["system"].(map[string]any)["inbox"].(map[string]any)["whatsApp"].(map[string]any)["businessAccountId"] = "102290129340398"
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	hook := []byte(`{"entry":[{"changes":[{"value":{"contacts":[{"profile":{"name":"Ann"},"wa_id":"447700900123"}],
		"messages":[{"from":"447700900123","id":"wamid.old","timestamp":"1700000000","type":"text","text":{"body":"Is my order ready?"}}]}}]}]}`)
	req, _ := http.NewRequest("POST", ts.URL+"/hooks/whatsapp", bytes.NewReader(hook))
	req.Header.Set("X-Hub-Signature-256", metaSign(hook, "appsecret"))
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatal("webhook")
	}
	var id string
	for end := time.Now().Add(3 * time.Second); id == "" && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		r, _ := admin.http.Do(must(http.NewRequest("GET", ts.URL+"/api/inbox", nil)))
		var arr []map[string]any
		json.NewDecoder(r.Body).Decode(&arr)
		r.Body.Close()
		for _, c := range arr {
			if c["channel"] == "whatsapp" {
				id = c["id"].(string)
			}
		}
	}
	_, th := admin.do("GET", "/api/inbox/"+id, nil)
	if th["window"] != "template" || th["windowOpen"] != false {
		t.Fatalf("window %v", th)
	}
	r, _ := admin.http.Do(must(http.NewRequest("GET", ts.URL+"/api/inbox/templates", nil)))
	var list []map[string]any
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 1 || list[0]["params"].(float64) != 2 || list[0]["usable"] != true {
		t.Fatalf("templates %v", list)
	}
	if code, _ := admin.do("POST", "/api/inbox/"+id+"/template", map[string]any{"name": "order_ready", "language": "en_US", "params": []string{"Ann"}}); code != 400 {
		t.Fatalf("one value short: %d", code)
	}
	code, out := admin.do("POST", "/api/inbox/"+id+"/template", map[string]any{"name": "order_ready", "language": "en_US", "params": []string{"Ann", "A-17"}})
	if code != 201 || out["text"] != "Hi Ann, order A-17 is ready." {
		t.Fatalf("send %d %v", code, out)
	}
	mu.Lock()
	last := sent[len(sent)-1]
	mu.Unlock()
	if last["type"] != "template" || last["to"] != "447700900123" {
		t.Fatalf("sent %v", last)
	}
}

func must(r *http.Request, err error) *http.Request {
	if err != nil {
		panic(err)
	}
	return r
}
