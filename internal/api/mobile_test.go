package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/push"
)

func TestMobilePairingAndPush(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var mu sync.Mutex
	var got []push.Message
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if _, err := push.Verify(r, body, func(k string) (string, bool) { return "a-relay-secret-1234", k == "inst" }, time.Now()); err != nil {
			w.WriteHeader(401)
			return
		}
		var m push.Message
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer relay.Close()
	_, _, err := s.Store.Update("test", "push", 0, func(c *config.Config) error {
		c.System.Mobile = config.Mobile{Push: "relay", RelayURL: "https://relay.example", RelayKey: "inst", RelaySecret: "a-relay-secret-1234"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Point the sender at the test relay (validation wants https; the test server is plain http).
	cfg, _ := s.Store.Config()
	s.pushSender(cfg.System.Mobile)
	s.mobile.sender = &push.Relay{URL: relay.URL, KeyID: "inst", Secret: "a-relay-secret-1234"}

	code, pair := admin.do("POST", "/api/extensions/101/pair", nil)
	if code != 200 || pair["code"] == "" || pair["link"] == "" {
		t.Fatalf("pair %d %v", code, pair)
	}
	app := newClient(t, ts.URL)
	if code, _ := app.do("POST", "/api/mobile/pair", map[string]string{"code": "wrong", "name": "x", "platform": "ios"}); code != 401 {
		t.Fatalf("wrong code: %d", code)
	}
	code, dev := app.do("POST", "/api/mobile/pair", map[string]string{"code": pair["code"].(string), "name": "Ann's iPhone", "platform": "ios"})
	if code != 200 || dev["extension"] != "101" || dev["secret"] == "" {
		t.Fatalf("paired %d %v", code, dev)
	}
	if code, _ := app.do("POST", "/api/mobile/pair", map[string]string{"code": pair["code"].(string), "name": "again", "platform": "ios"}); code != 401 {
		t.Fatal("a pairing code worked twice")
	}
	if s.HasDevices("101") {
		t.Fatal("a device without a push token cannot be woken")
	}
	if code, _ := app.do("POST", "/api/mobile/token", map[string]string{"device": dev["device"].(string), "secret": "nope", "token": "t"}); code != 401 {
		t.Fatal("token set without the secret")
	}
	if code, _ := app.do("POST", "/api/mobile/token", map[string]string{"device": dev["device"].(string), "secret": dev["secret"].(string), "token": "apns-token-1"}); code != 200 {
		t.Fatal("token")
	}
	if !s.HasDevices("101") {
		t.Fatal("device with a token not wakeable")
	}
	s.Wake("101", pbx.CallInfo{ID: "call-1", From: "100", FromName: "Front desk"})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if len(got) != 1 || got[0].Token != "apns-token-1" || got[0].CallID != "call-1" || got[0].Platform != "ios" {
		t.Fatalf("pushed %+v", got)
	}
	mu.Unlock()
	// The app signs in to the phone with its device credentials.
	if code, out := app.do("POST", "/api/phone", map[string]string{"device": dev["device"].(string), "secret": dev["secret"].(string)}); code != 200 || out["extension"] != "101" {
		t.Fatalf("phone sign-in %d %v", code, out)
	}
	code, list := admin.do("GET", "/api/extensions/101/devices", nil)
	items := list["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["canWake"] != true {
		t.Fatalf("devices %v", list)
	}
	if code, _ := admin.do("DELETE", "/api/mobile/devices/"+dev["device"].(string), nil); code != 204 {
		t.Fatal("unpair")
	}
	if code, _ := app.do("POST", "/api/phone", map[string]string{"device": dev["device"].(string), "secret": dev["secret"].(string)}); code != 401 {
		t.Fatal("an unpaired device signed in")
	}
}
