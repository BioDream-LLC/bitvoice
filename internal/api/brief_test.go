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
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

func TestBriefing(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var asked []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		all := ""
		for _, m := range in.Messages {
			all += string(m.Content)
		}
		answer := "Nothing useful was said yet."
		switch {
		case strings.Contains(all, "input_audio"):
			answer = "My invoice 4471 is wrong."
		case strings.Contains(all, "invoice 4471"):
			answer = "A customer says invoice 4471 is wrong and wants it corrected."
		case strings.Contains(all, "Caller: I need to move my appointment"):
			answer = "Jo wants to move Tuesday's appointment to Friday."
		}
		asked = append(asked, answer)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer model.Close()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "ai", rev, func(c *config.Config) error {
		c.System.AI.Enabled, c.System.AI.BaseURL, c.System.AI.Model, c.System.AI.AudioModel, c.System.AI.Briefings = true, model.URL, "m", "a", true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	wait := func() Briefing {
		t.Helper()
		for {
			select {
			case ev := <-sub.C:
				if b, ok := ev.Data.(Briefing); ok && ev.Type == "call.briefing" {
					return b
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("no briefing; the model said %v", asked)
			}
		}
	}
	// From the call's audio: the caller speaks for a second.
	talk := make([]int16, 8000)
	for i := range talk {
		talk[i] = int16(6000 * math.Sin(float64(i)/4))
	}
	go s.Brief(pbx.BriefRequest{CallID: "c1", Target: "101", From: "+12125550100", By: "100", Caller: append(make([]int16, 4000), append(talk, make([]int16, 8000)...)...), Callee: make([]int16, 20000)})
	if b := wait(); b.Summary != "A customer says invoice 4471 is wrong and wants it corrected." || b.Target != "101" {
		t.Fatalf("audio briefing %+v", b)
	}
	if code, b := admin.do("GET", "/api/calls/c1/briefing", nil); code != 200 || !strings.Contains(b["summary"].(string), "4471") {
		t.Fatalf("briefing by API %d %v", code, b)
	}
	// From the receptionist's conversation.
	go s.Brief(pbx.BriefRequest{CallID: "c2", Target: "101", From: "+12125550111", FromName: "Jo", Text: "Caller: I need to move my appointment to Friday\nAssistant: I will put you through.\n"})
	if b := wait(); !strings.Contains(b.Summary, "Friday") {
		t.Fatalf("text briefing %+v", b)
	}
	// Nothing useful: no briefing at all.
	s.Brief(pbx.BriefRequest{CallID: "c3", Target: "101", Text: "Caller: hello?\n"})
	if _, ok := s.briefingFor("c3"); ok {
		t.Fatal("a briefing for nothing")
	}
}
