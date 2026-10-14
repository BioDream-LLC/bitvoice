package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/media"
)

func TestPromptsAndMusic(t *testing.T) {
	_, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	raw := func(method, path string, body []byte) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("X-BitVoice", "1")
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	wav := media.EncodeWAV(make([]int16, media.Rate*2))

	// Prompts: record one in Spanish, see it listed; unknown keys and languages are refused.
	if code, b := raw("PUT", "/api/prompts/es/goodbye", wav); code != 200 {
		t.Fatalf("record %d %s", code, b)
	}
	if code, _ := raw("PUT", "/api/prompts/xx/goodbye", wav); code != 404 {
		t.Fatal("an unlisted language was accepted")
	}
	if code, _ := raw("PUT", "/api/prompts/es/nonsense", wav); code != 404 {
		t.Fatal("an unknown prompt was accepted")
	}
	_, list := admin.do("GET", "/api/prompts", nil)
	if rec, _ := json.Marshal(list["recorded"]); !strings.Contains(string(rec), `"es":["goodbye"]`) || list["speech"] != false {
		t.Fatalf("list %v", list)
	}
	if code, b := raw("POST", "/api/prompts/es/generate", nil); code != 422 || !strings.Contains(b, "speech model") {
		t.Fatalf("generate without speech %d %s", code, b)
	}
	// A language added in settings can be recorded, but not generated (BitVoice has no words for it).
	_, cfg := admin.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	sys["promptLanguages"] = []string{"sv"}
	sys["music"] = []map[string]any{{"id": "jazz", "name": "Jazz", "shuffle": false}}
	sys["holdMusic"] = "jazz"
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("settings %d %v", code, out)
	}
	if code, _ := raw("PUT", "/api/prompts/sv/goodbye", wav); code != 200 {
		t.Fatal("added language refused")
	}
	if code, _ := raw("POST", "/api/prompts/sv/generate", nil); code != 422 {
		t.Fatal("generated a language without words")
	}

	// Music: tracks go into a saved class only, and come back listed with their length.
	if code, _ := raw("PUT", "/api/music/rock/one", wav); code != 404 {
		t.Fatal("track in an unsaved class")
	}
	if code, b := raw("PUT", "/api/music/jazz/one", wav); code != 200 {
		t.Fatalf("track %d %s", code, b)
	}
	if code, _ := raw("PUT", "/api/music/jazz/bad.name", wav); code == 200 {
		t.Fatal("bad track name accepted")
	}
	if code, b := raw("GET", "/api/music", nil); code != 200 || !strings.Contains(b, `"jazz":[{"name":"one","seconds":2}]`) {
		t.Fatalf("music %d %s", code, b)
	}
	if code, _ := raw("GET", "/api/music/jazz/one", nil); code != 200 {
		t.Fatal("play track")
	}
	if code, _ := raw("DELETE", "/api/music/jazz/one", nil); code != 204 {
		t.Fatal("delete track")
	}
}
