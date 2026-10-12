package api

import (
	"testing"
)

func TestP25KeysNeverReturned(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, out := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	_, cur := c.do("GET", "/api/settings", nil)
	sys := cur["system"].(map[string]any)
	sys["radio"] = map[string]any{"console": "800",
		"p25": map[string]any{"wacn": "BEE07", "system": "AD5", "rfss": 2, "unit": 4097, "sipPort": 25062, "priority": 4,
			"keys": []any{map[string]any{"name": "Fire tac", "alg": "aes256", "keyId": 257, "key": "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}}},
		"channels": []any{map[string]any{"name": "TG", "kind": "p25", "port": 46010, "group": 819, "home": "127.0.0.1:5098", "homeRfss": 3}}}
	code, out := c.do("PUT", "/api/settings", cur)
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	key := func(m map[string]any) map[string]any {
		return m["system"].(map[string]any)["radio"].(map[string]any)["p25"].(map[string]any)["keys"].([]any)[0].(map[string]any)
	}
	if k := key(out); k["key"] != "" || k["id"] == "" {
		t.Fatalf("the save's answer shows the key: %v", k)
	}
	_, again := c.do("GET", "/api/settings", nil)
	if k := key(again); k["key"] != "" {
		t.Fatalf("GET shows the key: %v", k)
	}
	// Saving the form as it came back (key empty) keeps the key; the channel can now use it.
	again["system"].(map[string]any)["radio"].(map[string]any)["channels"].([]any)[0].(map[string]any)["txKey"] = key(again)["id"]
	if code, out := c.do("PUT", "/api/settings", again); code != 200 {
		t.Fatalf("save without the key: %d %v", code, out)
	}
	cfg, _ := s.Store.Config()
	if k := cfg.System.Radio.P25.Keys; len(k) != 1 || k[0].Key != "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" {
		t.Fatalf("key lost: %+v", k)
	}
	// A wrong length is refused.
	_, bad := c.do("GET", "/api/settings", nil)
	key(bad)["key"] = "abcd"
	if code, out := c.do("PUT", "/api/settings", bad); code != 422 || !hasProblem(out, "system.radio.p25.keys[0].key") {
		t.Fatalf("short key accepted: %d %v", code, out)
	}
}
