package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// ErrNoModel is a request that needs a language model when none is set: with only a speech-to-text
// service, transcripts are written and what would need a model (summaries, triage) is left out.
var ErrNoModel = errors.New("ai: no language model is set")

// transcribeService sends a WAV recording to a speech-to-text service and returns what was said. It
// speaks the form both whisper.cpp's whisper-server (POST /inference) and OpenAI-compatible services
// (POST …/audio/transcriptions) take: the audio as the multipart field "file", the answer as JSON
// {"text": …}. No language model is involved: a speech recogniser writes down what it hears, and
// whisper runs on the same computer, so the callers' audio need never leave the building.
func (c *Client) transcribeService(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="speech.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	part.Write(wav)
	w.WriteField("response_format", "json")
	w.WriteField("temperature", "0")
	if c.TranscribeModel != "" {
		w.WriteField("model", c.TranscribeModel)
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TranscribeURL, &body)
	if err != nil {
		return "", fmt.Errorf("ai: speech-to-text address: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.TranscribeKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.TranscribeKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: speech-to-text: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		Text  string `json:"text"`
		Error any    `json:"error"`
	}
	jerr := json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		if m, ok := out.Error.(map[string]any); ok && m["message"] != nil {
			return "", fmt.Errorf("ai: speech-to-text: %v", m["message"])
		}
		if s, ok := out.Error.(string); ok && s != "" {
			return "", fmt.Errorf("ai: speech-to-text: %s", s)
		}
		return "", fmt.Errorf("ai: the speech-to-text service answered %d", resp.StatusCode)
	}
	if jerr != nil {
		return "", errors.New("ai: the speech-to-text service did not answer in JSON")
	}
	return speechOnly(out.Text), nil
}

// nonSpeechWords are what a speech recogniser writes, in brackets, when it heard no words.
var nonSpeechWords = []string{"silence", "blank_audio", "blank audio", "no speech", "inaudible", "music", "pause", "background noise"}

// isNonSpeechMarker reports whether a line is a recogniser's note about what it heard, such as
// "[ Silence ]" or "[BLANK_AUDIO]", rather than anything said. The test is by shape (short and wholly
// in brackets) as well as by word, since the wording changes between whisper builds; words that only
// start with a bracket, "[laughs] I do that too", are speech and kept.
func isNonSpeechMarker(s string) bool {
	if len(s) < 2 || len(s) > 24 {
		return false
	}
	open, close := s[0], s[len(s)-1]
	if !(open == '[' && close == ']' || open == '(' && close == ')' || open == '*' && close == '*') {
		return false
	}
	inner := strings.ToLower(strings.TrimSpace(s[1 : len(s)-1]))
	for _, w := range nonSpeechWords {
		if strings.Contains(inner, w) {
			return true
		}
	}
	return false
}

// speechOnly trims a transcript and drops the lines that only describe non-speech, so a quiet stretch
// never reads as though something was said.
func speechOnly(text string) string {
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !isNonSpeechMarker(l) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, " ")
}
