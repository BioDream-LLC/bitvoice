package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// A speech-to-text service (whisper.cpp's whisper-server, or an OpenAI-compatible one) writes the
// speech down: the audio goes as the form field "file", no language model is asked, and the
// recogniser's notes about silence never read as speech.
func TestTranscribeService(t *testing.T) {
	var got struct {
		path, auth, model, format string
		wav                       []byte
	}
	chatAsked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			chatAsked = true
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", 400)
			return
		}
		got.wav, _ = io.ReadAll(f)
		got.path, got.auth, got.model, got.format = r.URL.Path, r.Header.Get("Authorization"), r.FormValue("model"), r.FormValue("response_format")
		json.NewEncoder(w).Encode(map[string]string{"text": " There is smoke next door.\n[BLANK_AUDIO]\n"})
	}))
	defer srv.Close()
	c := &Client{TranscribeURL: srv.URL + "/inference", TranscribeKey: "k1", TranscribeModel: "whisper-1"}
	text, err := c.Transcribe(context.Background(), []byte("RIFFfake"))
	if err != nil || text != "There is smoke next door." {
		t.Fatalf("%q %v", text, err)
	}
	if chatAsked || got.path != "/inference" || got.auth != "Bearer k1" || got.model != "whisper-1" || got.format != "json" || string(got.wav) != "RIFFfake" {
		t.Fatalf("request %+v chat=%v", got, chatAsked)
	}
	// Its errors say what the service said.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"failed to read audio data"}`))
	}))
	defer bad.Close()
	if _, err := (&Client{TranscribeURL: bad.URL}).Transcribe(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "failed to read audio data") {
		t.Fatalf("error %v", err)
	}
	// The daily allowance still counts it.
	limit := errors.New("used up")
	if _, err := (&Client{TranscribeURL: srv.URL, Allow: func() error { return limit }}).Transcribe(context.Background(), nil); !errors.Is(err, limit) {
		t.Fatalf("allowance %v", err)
	}
}

// With only a speech-to-text service, what needs a language model says so instead of asking one.
func TestNoModel(t *testing.T) {
	if _, err := (&Client{TranscribeURL: "http://127.0.0.1:1/inference"}).Summarize(context.Background(), "hello"); !errors.Is(err, ErrNoModel) {
		t.Fatalf("got %v", err)
	}
}

// A recogniser's note about what it heard is dropped; words that only start with a bracket are speech.
func TestNonSpeechMarkers(t *testing.T) {
	for _, s := range []string{"[silence]", "[ Silence ]", "[BLANK_AUDIO]", "(silence)", "[ Pause ]", "(inaudible)", "[Music]", "*silence*"} {
		if !isNonSpeechMarker(s) {
			t.Errorf("should drop %q", s)
		}
	}
	for _, s := range []string{"No.", "I don't know.", "[laughs] I do that too, honestly", "(and then she hung up on me)", "there was silence, then a bang", "[Gunshot]"} {
		if isNonSpeechMarker(s) {
			t.Errorf("should keep %q", s)
		}
	}
	if got := speechOnly(" [ Silence ]\n Help me.\n(music)\n"); got != "Help me." {
		t.Fatalf("%q", got)
	}
}

// Against a real whisper-server: BITVOICE_TEST_WHISPER=http://127.0.0.1:8178/inference and
// BITVOICE_TEST_WHISPER_WAV=a recording, BITVOICE_TEST_WHISPER_SAYS=a word it holds.
func TestWhisperServer(t *testing.T) {
	u, wavPath := os.Getenv("BITVOICE_TEST_WHISPER"), os.Getenv("BITVOICE_TEST_WHISPER_WAV")
	if u == "" || wavPath == "" {
		t.Skip("set BITVOICE_TEST_WHISPER and BITVOICE_TEST_WHISPER_WAV")
	}
	wav, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	text, err := (&Client{TranscribeURL: u}).Transcribe(context.Background(), wav)
	if err != nil || !strings.Contains(strings.ToLower(text), strings.ToLower(os.Getenv("BITVOICE_TEST_WHISPER_SAYS"))) {
		t.Fatalf("%q %v", text, err)
	}
	t.Logf("whisper heard %q", text)
}
