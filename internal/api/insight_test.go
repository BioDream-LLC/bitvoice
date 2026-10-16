package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestInsight(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var prompt string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, m := range in.Messages {
			var s string
			if json.Unmarshal(m.Content, &s) != nil {
				s = string(m.Content)
			}
			prompt += s
		}
		answer := `{"moodStart":"Frustrated","moodEnd":"relieved","observations":[{"second":3,"kind":"look-at","note":"Talked over the caller","quote":"let me stop you there"},{"second":9,"kind":"promise","note":"Plumber booked","quote":"tomorrow at nine"},{"second":1,"kind":"score","note":"8/10"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer model.Close()
	_, rev := s.Store.Config()
	s.Store.Update("test", "ai", rev, func(c *config.Config) error {
		c.System.AI.Enabled, c.System.AI.BaseURL, c.System.AI.Model, c.System.AI.Insights = true, model.URL, "m", true
		return nil
	})
	tone := func(n int) []int16 {
		p := make([]int16, n)
		for i := range p {
			p[i] = int16(6000 * math.Sin(float64(i)/4))
		}
		return p
	}
	left := append(tone(4*8000), make([]int16, 12*8000)...)
	right := append(make([]int16, 3*8000), append(tone(10*8000), make([]int16, 3*8000)...)...)
	name, err := s.Store.AddRecording("call1", time.Now(), media.EncodeStereoWAV(left, right))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := admin.do("POST", "/api/recordings/"+name+"/insight", nil); code != 422 {
		t.Fatal("insight before a transcript")
	}
	s.Store.SetCallTranscript(store.CallTranscript{Recording: name, CallID: "call1", At: time.Now(), From: "+12125550199", To: "101", ToName: "Sam", AnsweredExt: "101", Status: "done",
		Lines: []store.TranscriptLine{{Second: 0.5, Side: "caller", Text: "My boiler is leaking"}, {Second: 3, Side: "callee", Text: "let me stop you there"}}})
	code, out := admin.do("POST", "/api/recordings/"+name+"/insight", nil)
	if code != 200 {
		t.Fatalf("insight %d %v", code, out)
	}
	in := out["insight"].(map[string]any)
	obs := in["observations"].([]any)
	if in["moodStart"] != "frustrated" || len(obs) != 2 || out["talk"].(map[string]any)["calleeCutIn"].(float64) != 1 {
		t.Fatalf("insight %v talk %v", in, out["talk"])
	}
	if !strings.Contains(prompt, "[3s] Sam: let me stop you there") || !strings.Contains(prompt, "Do not score") {
		t.Fatalf("prompt %q", prompt)
	}
	_, agg := admin.do("GET", "/api/insight?days=7", nil)
	a := agg["agents"].([]any)[0].(map[string]any)
	if a["name"] != "Sam" || a["calls"].(float64) != 1 || a["cutIns"].(float64) != 1 || a["lookAt"].(float64) != 1 || a["talkShare"].(float64) < 65 {
		t.Fatalf("by agent %v", a)
	}

	// Coaching notes: written at a moment of the call, kept in order, counted per person, removable.
	coach := "/api/recordings/" + name + "/coaching"
	for _, bad := range []map[string]any{{"second": 3, "note": "  "}, {"second": -1, "note": "x"}, {"second": 99, "note": "x"}} {
		if code, _ := admin.do("POST", coach, bad); code != 422 {
			t.Fatalf("note %v accepted: %d", bad, code)
		}
	}
	admin.do("POST", coach, map[string]any{"second": 9.4, "note": "Good: booked the plumber and said when"})
	code, out = admin.do("POST", coach, map[string]any{"second": 3, "note": "Let the caller finish before answering"})
	notes := out["coaching"].([]any)
	if code != 200 || len(notes) != 2 || notes[0].(map[string]any)["second"].(float64) != 3 || notes[1].(map[string]any)["second"].(float64) != 9 || notes[0].(map[string]any)["by"] != "owner" {
		t.Fatalf("notes %d %v", code, notes)
	}
	_, agg = admin.do("GET", "/api/insight?days=7", nil)
	if n := agg["agents"].([]any)[0].(map[string]any)["coaching"].(float64); n != 2 {
		t.Fatalf("coaching count %v", n)
	}
	viewer := newClient(t, ts.URL)
	admin.do("POST", "/api/users", map[string]any{"username": "viewer", "name": "V", "role": "viewer", "password": "viewer password"})
	viewer.do("POST", "/api/auth/login", map[string]any{"username": "viewer", "password": "viewer password"})
	if code, _ := viewer.do("POST", coach, map[string]any{"second": 1, "note": "x"}); code != 403 {
		t.Fatalf("a viewer wrote a note: %d", code)
	}
	id := notes[0].(map[string]any)["id"].(string)
	if code, out := admin.do("DELETE", coach+"/"+id, nil); code != 200 || len(out["coaching"].([]any)) != 1 {
		t.Fatalf("delete %d %v", code, out)
	}
	if code, _ := admin.do("DELETE", coach+"/"+id, nil); code != 404 {
		t.Fatal("deleted twice")
	}
}
