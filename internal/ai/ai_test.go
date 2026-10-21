package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribeAndSummarize(t *testing.T) {
	var sawAudio, sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("Authorization") == "Bearer k"
		var in struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []part `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		answer := "Summary."
		for _, m := range in.Messages {
			for _, p := range m.Content {
				if p.InputAudio != nil && p.InputAudio.Format == "wav" && p.InputAudio.Data != "" {
					sawAudio = true
					answer = "Hi, this is Sam, call me on 555 0100."
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k", Model: "test/model"}
	text, err := c.Transcribe(context.Background(), []byte("RIFF...."))
	if err != nil || !sawAudio || !sawKey || !strings.Contains(text, "Sam") {
		t.Fatalf("%q %v audio=%v key=%v", text, err, sawAudio, sawKey)
	}
	if s, err := c.Summarize(context.Background(), text); err != nil || s != "Summary." {
		t.Fatalf("%q %v", s, err)
	}
}

func TestServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"No auth credentials found"}}`))
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL, Model: "m"}).Summarize(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "No auth credentials") {
		t.Fatal(err)
	}
}

// answering returns a fake model service that always gives the same answer.
func answering(answer string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
}

func TestTriage(t *testing.T) {
	srv := answering("Here you go:\n```json\n{\"urgency\": \"URGENT\", \"category\": \"support\", \"reason\": \"server down\"}\n```")
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Model: "m"}
	tr, err := c.TriageVoicemail(context.Background(), "our server is down please call back")
	if err != nil || tr.Urgency != "urgent" || tr.Category != "support" {
		t.Fatalf("%+v %v", tr, err)
	}
	odd := answering(`{"urgency": "whenever", "category": "aliens"}`)
	defer odd.Close()
	c.BaseURL = odd.URL
	if tr, _ := c.TriageVoicemail(context.Background(), "x"); tr.Urgency != "normal" || tr.Category != "other" {
		t.Fatalf("unknown values kept: %+v", tr)
	}
	bad := answering("I cannot help")
	defer bad.Close()
	c.BaseURL = bad.URL
	if _, err := c.TriageVoicemail(context.Background(), "x"); err == nil {
		t.Fatal("a non-JSON answer was accepted")
	}
}

// A callback with a detail missing becomes a question for it; the caller's own number fills an unusable
// one; plain words are asked for again as JSON.
func TestReceptionCallbackChecks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ answer, action, say, number string }{
		{`{"say":"ok","action":"callback","callback":{"name":"Ana","number":"","when":"","reason":"a quote"}}`, "answer", "When is a good time to call you back?", ""},
		{`{"say":"ok","action":"callback","callback":{"name":"","number":"5550123","when":"noon","reason":"x"}}`, "answer", "May I have your name, please?", ""},
		{`{"say":"ok","action":"callback","callback":{"name":"Ana","number":"the usual","when":"noon","reason":"x"}}`, "callback", "ok", "+12125550123"},
		{`{"say":"ok","action":"callback"}`, "answer", "ok", ""},
	} {
		srv := answering(tc.answer)
		c := &Client{BaseURL: srv.URL, Model: "m"}
		got, err := c.Reception(ctx, "Acme", "", "+12125550123", []string{"call me back"})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got.Action != tc.action || got.Say != tc.say || (tc.number != "" && got.Callback.Number != tc.number) {
			t.Errorf("%s: got %+v %+v", tc.answer, got, got.Callback)
		}
	}
	tries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries++
		answer := "Sure, what is your name?"
		if tries > 1 {
			answer = `{"say":"What is your name?","action":"answer"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Model: "m"}
	if got, err := c.Reception(ctx, "Acme", "", "", []string{"hi"}); err != nil || tries != 2 || got.Say != "What is your name?" {
		t.Fatalf("retry: %v %d %+v", err, tries, got)
	}
}
