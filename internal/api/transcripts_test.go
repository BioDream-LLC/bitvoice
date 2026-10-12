package api

import (
	"context"
	"encoding/base64"
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

// A recorded call becomes a transcript with who said what (from the recording's two sides, in time
// order), a summary and action items; it can be searched by what was said, and goes with its recording.
func TestCallTranscripts(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	// The fake model "hears" a piece of audio by its length: the caller speaks for 1 s, then the other
	// side for 2 s.
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content []struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					InputAudio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		answer := ""
		for _, m := range in.Messages {
			for _, p := range m.Content {
				if p.Type == "input_audio" {
					wav, _ := base64.StdEncoding.DecodeString(p.InputAudio.Data)
					if len(wav) < 30000 { // about 1.6 s at 8 kHz, 16-bit
						answer = "Hi, my boiler is leaking."
					} else {
						answer = "I will send a plumber tomorrow at nine."
					}
				}
				if strings.Contains(p.Text, "Sam Patel: I will send a plumber") && strings.Contains(p.Text, "Front desk: Hi, my boiler") {
					answer = `{"summary":"A caller reported a leaking boiler; a plumber was booked.","actionItems":["Sam Patel: send a plumber tomorrow at 9"]}`
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer model.Close()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "ai", rev, func(c *config.Config) error {
		c.System.AI.Enabled, c.System.AI.BaseURL, c.System.AI.Model, c.System.AI.Calls = true, model.URL, "m", true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A stereo recording: the caller talks 0.5–1.5 s, the other side 2–4 s.
	silence := func(n int) []int16 { return make([]int16, n) }
	talk := func(n int) []int16 {
		p := make([]int16, n)
		for i := range p {
			p[i] = int16(6000 * math.Sin(float64(i)/4))
		}
		return p
	}
	left := append(append(silence(4000), talk(8000)...), silence(28000)...)
	right := append(append(silence(16000), talk(16000)...), silence(8000)...)
	start := time.Now().UTC()
	name, err := s.Store.AddRecording("call1", start, media.EncodeStereoWAV(left, right))
	if err != nil {
		t.Fatal(err)
	}
	cdr := store.CDR{ID: "call1", Start: start, End: start.Add(5 * time.Second), From: "100", FromName: "Front desk", To: "101", ToName: "Sam Patel", Recording: name}
	if err := s.Store.AddCDR(cdr); err != nil {
		t.Fatal(err)
	}
	s.transcribeCall(context.Background(), cdr, false)
	code, out := admin.do("GET", "/api/recordings/"+name+"/transcript", nil)
	if code != 200 || out["status"] != "done" {
		t.Fatalf("transcript %d %v", code, out)
	}
	lines := out["lines"].([]any)
	if len(lines) != 2 || lines[0].(map[string]any)["side"] != "caller" || lines[1].(map[string]any)["side"] != "callee" ||
		!strings.Contains(lines[1].(map[string]any)["text"].(string), "plumber") {
		t.Fatalf("lines %v", lines)
	}
	if sec := lines[1].(map[string]any)["second"].(float64); sec < 1.5 || sec > 2.1 {
		t.Errorf("the second line starts at %v s", sec)
	}
	if !strings.Contains(out["summary"].(string), "leaking boiler") || len(out["actionItems"].([]any)) != 1 {
		t.Fatalf("notes %v", out)
	}
	if code, out := admin.do("GET", "/api/transcripts?q=PLUMBER+tomorrow", nil); code != 200 || len(out["items"].([]any)) != 1 {
		t.Fatalf("search %d %v", code, out)
	}
	if code, out := admin.do("GET", "/api/transcripts?q=gas+meter", nil); code != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("search for something not said %d %v", code, out)
	}
	if code, _ := admin.do("GET", "/api/transcripts?q=a", nil); code != 422 {
		t.Fatalf("one-letter search %d", code)
	}
	if code, _ := admin.do("POST", "/api/recordings/"+name+"/transcript", nil); code != 202 {
		t.Fatalf("make again %d", code)
	}
	time.Sleep(300 * time.Millisecond)
	if code, _ := admin.do("DELETE", "/api/recordings/"+name, nil); code != 204 {
		t.Fatalf("delete recording %d", code)
	}
	if code, _ := admin.do("GET", "/api/recordings/"+name+"/transcript", nil); code != 404 {
		t.Fatalf("the transcript outlived its recording: %d", code)
	}
}
