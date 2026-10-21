package api

import "testing"

// The AI key is kept when a save leaves it empty while a model still uses it; once no model does,
// saving it empty removes it.
func TestAIKeyRemovedWithModel(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	save := func(model, key string) {
		t.Helper()
		_, cfg := admin.do("GET", "/api/settings", nil)
		ai := cfg["system"].(map[string]any)["ai"].(map[string]any)
		ai["enabled"], ai["model"], ai["key"], ai["localSpeech"] = true, model, key, model == "" // no model: speech to text on this server
		if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
			t.Fatalf("save %d %v", code, out)
		}
	}
	key := func() string { c, _ := s.Store.Config(); return c.System.AI.Key }
	save("meta-llama/llama-3.3-70b-instruct", "sk-test-key")
	save("meta-llama/llama-3.3-70b-instruct", "")
	if key() != "sk-test-key" {
		t.Fatal("an empty key with a model lost the saved key")
	}
	save("", "")
	if key() != "" {
		t.Fatal("the key stayed with no model to use it")
	}
}
