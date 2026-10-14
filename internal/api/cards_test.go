package api

import (
	"testing"
)

func TestCardsBridgeAPI(t *testing.T) {
	_, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	bridge := newClient(t, ts.URL)
	if code, _ := bridge.do("GET", "/api/cards/bridge", nil, "Authorization", "Bearer anything"); code != 401 {
		t.Fatalf("cards off: %d", code)
	}
	admin.do("POST", "/api/extensions", map[string]any{"number": "230", "name": "Kitchen phone", "secret": "a long enough secret 230", "enabled": true})
	_, cfg := admin.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	tok := "abcdefghijklmnopqrstuvwxyz012345"
	sys["cards"] = map[string]any{"enabled": true, "token": tok, "channels": []map[string]any{{"channel": 1, "kind": "phone", "extension": "230"}, {"channel": 2, "kind": "line", "number": "2125550100"}}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	if code, _ := bridge.do("GET", "/api/cards/bridge", nil, "Authorization", "Bearer wrong-token-wrong-token-wrong"); code != 401 {
		t.Fatalf("a wrong token got %d", code)
	}
	code, out := bridge.do("GET", "/api/cards/bridge", nil, "Authorization", "Bearer "+tok)
	if code != 200 || out["linePort"].(float64) != 5091 || out["sipPort"].(float64) == 0 {
		t.Fatalf("%d %v", code, out)
	}
	chs := out["channels"].([]any)
	if len(chs) != 2 || chs[0].(map[string]any)["secret"] != "a long enough secret 230" || chs[1].(map[string]any)["number"] != "2125550100" {
		t.Fatalf("channels %v", chs)
	}
	if code, _ := bridge.do("POST", "/api/cards/status", map[string]any{"channels": map[string]string{"1": "idle", "2": "in a call"}, "version": "test"}, "Authorization", "Bearer "+tok); code != 204 {
		t.Fatalf("status %d", code)
	}
	_, st := admin.do("GET", "/api/cards", nil)
	if st["running"] != true || st["channels"].(map[string]any)["2"] != "in a call" {
		t.Fatalf("%v", st)
	}
	if code, _ := newClient(t, ts.URL).do("GET", "/api/cards", nil); code != 401 {
		t.Fatal("the cards page answered without sign-in")
	}
}
