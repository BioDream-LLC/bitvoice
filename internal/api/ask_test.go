package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The model proposes a change; BitVoice shows it, and applies it only when asked, keeping secrets.
func TestAskInPlainWords(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var answer string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if strings.Contains(in.Messages[1].Content[0].Text, `"secret"`) && strings.Contains(in.Messages[1].Content[0].Text, before(s)) {
			answer = `{"explanation":"leaked","changes":{}}` // a password reached the model
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer model.Close()
	_, _, _ = s.Store.Update("t", "ai", 0, func(c *config.Config) error {
		c.System.AI = config.AI{Enabled: true, BaseURL: model.URL, Model: "m"}
		return nil
	})
	cfg, _ := s.Store.Config()
	exts, _ := json.Marshal(cfg.Extensions)
	renamed := strings.Replace(string(exts), `"name":"Front desk"`, `"name":"Reception desk"`, 1)
	answer = `{"explanation":"Renamed 100.","changes":{"extensions":` + renamed + `}}`
	code, out := admin.do("POST", "/api/ask", map[string]string{"request": "call extension 100 Reception desk"})
	if code != 200 || out["id"] == "" || len(out["changes"].([]any)) == 0 {
		t.Fatalf("propose %d %v", code, out)
	}
	if c, _ := s.Store.Config(); c.Extensions[0].Name != "Front desk" {
		t.Fatal("changed before applying")
	}
	if code, _ := admin.do("POST", "/api/ask/"+out["id"].(string)+"/apply", nil); code != 200 {
		t.Fatal("apply")
	}
	after, _ := s.Store.Config()
	if after.Extensions[0].Name != "Reception desk" || after.Extensions[0].Secret != cfg.Extensions[0].Secret {
		t.Fatalf("after %+v", after.Extensions[0])
	}
	// A proposal touching settings is refused.
	answer = `{"explanation":"x","changes":{"system":{}}}`
	if code, _ := admin.do("POST", "/api/ask", map[string]string{"request": "change the SIP port"}); code != 422 {
		t.Fatalf("system change accepted: %d", code)
	}
	// An invalid proposal is refused with the reason.
	answer = `{"explanation":"x","changes":{"ringGroups":[{"number":"x1","name":"Bad","strategy":"all","members":["999"]}]}}`
	if code, out := admin.do("POST", "/api/ask", map[string]string{"request": "add a group"}); code != 422 || !strings.Contains(out["message"].(string), "not be valid") {
		t.Fatalf("invalid accepted: %d %v", code, out)
	}
}

func before(s *Server) string {
	c, _ := s.Store.Config()
	return c.Extensions[0].Secret
}
