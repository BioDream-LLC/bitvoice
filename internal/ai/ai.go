// Package ai talks to a language model over the OpenAI-compatible chat completions API (OpenRouter by
// default, or any compatible endpoint, a local one included). BitVoice uses it only when an administrator
// switches it on, and only for what they choose: voicemail transcripts and summaries.
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is OpenRouter's API.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Client is one configured model.
type Client struct {
	BaseURL string
	Key     string
	Model   string
	// AudioModel, when set, is used instead of Model for turning speech into text (a model that takes
	// audio, such as google/gemini-2.5-flash, while Model may be a text-only one).
	AudioModel string
	HTTP       *http.Client
}

type part struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	InputAudio *inputAudio `json:"input_audio,omitempty"`
}

type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type message struct {
	Role    string `json:"role"`
	Content []part `json:"content"`
}

func (c *Client) chat(ctx context.Context, msgs []message) (string, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	body, _ := json.Marshal(map[string]any{"model": c.Model, "messages": msgs, "temperature": 0})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	req.Header.Set("X-Title", "BitVoice")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil && out.Error.Message != "" {
			return "", fmt.Errorf("ai: %s", out.Error.Message)
		}
		return "", fmt.Errorf("ai: the model service answered %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("ai: the model gave no answer")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// Transcribe returns what is said in a WAV recording, using a model that accepts audio.
func (c *Client) Transcribe(ctx context.Context, wav []byte) (string, error) {
	return c.audio().chat(ctx, []message{{Role: "user", Content: []part{
		{Type: "text", Text: "Transcribe this recording word for word. Reply with the transcript only. If nothing is said, reply with an empty line."},
		{Type: "input_audio", InputAudio: &inputAudio{Data: base64.StdEncoding.EncodeToString(wav), Format: "wav"}},
	}}})
}

// audio is the client to use for speech: the same service, with AudioModel when one is set.
func (c *Client) audio() *Client {
	if c.AudioModel == "" {
		return c
	}
	a := *c
	a.Model = c.AudioModel
	return &a
}

// CallNotes are what a model makes of a call's transcript.
type CallNotes struct {
	Summary     string   `json:"summary"`
	ActionItems []string `json:"actionItems"`
}

// SummarizeCall gives a call's summary (two or three sentences) and its action items: things someone
// agreed or was asked to do, each with who should do it when the call says so.
func (c *Client) SummarizeCall(ctx context.Context, transcript string) (CallNotes, error) {
	var n CallNotes
	if strings.TrimSpace(transcript) == "" {
		return n, nil
	}
	err := c.JSON(ctx, "You read phone call transcripts for a business. Write a summary of two or three sentences: who called, why, and how it ended. "+
		"Then list the action items: things someone agreed or was asked to do (call back, send a quote, book a visit), each one short, naming who does it when the call says. "+
		"Use only what the transcript says; never invent names, numbers, dates or promises. No action items is an empty list.\n"+
		`Fields: {"summary": "...", "actionItems": ["..."]}`, transcript, &n)
	if err != nil {
		return n, err
	}
	n.Summary = clip(n.Summary, 1000)
	items := n.ActionItems[:0]
	for _, a := range n.ActionItems {
		if a = clip(a, 300); a != "" && len(items) < 20 {
			items = append(items, a)
		}
	}
	n.ActionItems = items
	return n, nil
}

// Insight is what a supervisor may want to look at in a call: how the caller's mood moved, and
// moments worth hearing, each tied to what was said. It is evidence, never a score.
type Insight struct {
	MoodStart    string        `json:"moodStart"` // calm, worried, frustrated, angry, pleased ...
	MoodEnd      string        `json:"moodEnd"`
	Observations []Observation `json:"observations"`
}

// Observation is one moment in a call and why it may matter.
type Observation struct {
	Second float64 `json:"second"`
	Kind   string  `json:"kind"` // went-well, look-at, unanswered, promise
	Note   string  `json:"note"`
	Quote  string  `json:"quote"`
}

// CallInsight reads a timestamped transcript ("[12s] Name: words" lines) for a supervisor.
func (c *Client) CallInsight(ctx context.Context, transcript string) (Insight, error) {
	var in Insight
	if strings.TrimSpace(transcript) == "" {
		return in, nil
	}
	err := c.JSON(ctx, "You help a contact-centre supervisor review a phone call. Do not score or grade anyone. "+
		"Say in one word how the caller seemed at the start and at the end (calm, worried, confused, frustrated, angry, pleased, relieved). "+
		"Then list up to six moments worth the supervisor's attention, each with the second it starts (from the [Ns] marks), a kind "+
		"(went-well: handled well; look-at: may need coaching, such as talking over the caller, a long hold, a wrong or unclear answer; "+
		"unanswered: a question the caller asked that was not answered; promise: something someone promised to do), a short plain note, "+
		"and the exact words said (a short quote copied from the transcript). Use only what the transcript says.\n"+
		`Fields: {"moodStart": "...", "moodEnd": "...", "observations": [{"second": 0, "kind": "...", "note": "...", "quote": "..."}]}`, transcript, &in)
	if err != nil {
		return in, err
	}
	in.MoodStart, in.MoodEnd = clip(strings.ToLower(in.MoodStart), 20), clip(strings.ToLower(in.MoodEnd), 20)
	kept := in.Observations[:0]
	for _, o := range in.Observations {
		switch o.Kind {
		case "went-well", "look-at", "unanswered", "promise":
		default:
			continue
		}
		if o.Note = clip(o.Note, 300); o.Note != "" && len(kept) < 6 && o.Second >= 0 {
			o.Quote = clip(o.Quote, 200)
			kept = append(kept, o)
		}
	}
	in.Observations = kept
	return in, nil
}

// Summarize gives a one-line summary of a transcript: who called, what they want, any number to call back.
func (c *Client) Summarize(ctx context.Context, transcript string) (string, error) {
	if strings.TrimSpace(transcript) == "" {
		return "", nil
	}
	return c.chat(ctx, []message{
		{Role: "system", Content: []part{{Type: "text", Text: "You summarise voicemails for a busy office in one short sentence: who called, what they need, and any call-back number. No preamble."}}},
		{Role: "user", Content: []part{{Type: "text", Text: transcript}}},
	})
}

// Text asks the model with a system prompt and returns its answer.
func (c *Client) Text(ctx context.Context, system, user string) (string, error) {
	return c.chat(ctx, []message{
		{Role: "system", Content: []part{{Type: "text", Text: system}}},
		{Role: "user", Content: []part{{Type: "text", Text: user}}},
	})
}

// JSON asks for an answer in JSON and decodes it into v. Models often wrap JSON in a code fence or a
// sentence; the first object in the answer is used. A model that answers in plain words is asked once
// more, shown what it said.
func (c *Client) JSON(ctx context.Context, system, user string, v any) error {
	msgs := []message{
		{Role: "system", Content: []part{{Type: "text", Text: system + "\nAnswer with one JSON object and nothing else."}}},
		{Role: "user", Content: []part{{Type: "text", Text: user}}},
	}
	var err error
	for try := 0; try < 2; try++ {
		var out string
		if out, err = c.chat(ctx, msgs); err != nil {
			return err
		}
		if err = decodeFirstObject(out, v); err == nil {
			return nil
		}
		msgs = append(msgs,
			message{Role: "assistant", Content: []part{{Type: "text", Text: out}}},
			message{Role: "user", Content: []part{{Type: "text", Text: "That was not the JSON object asked for. Answer again with only the JSON object, with the same meaning."}}})
	}
	return err
}

func decodeFirstObject(out string, v any) error {
	start, end := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return errors.New("ai: the model did not answer in JSON")
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), v); err != nil {
		return fmt.Errorf("ai: the model's JSON could not be read: %w", err)
	}
	return nil
}

// Triage is a voicemail's urgency and kind.
type Triage struct {
	Urgency  string `json:"urgency"`  // urgent, normal or low
	Category string `json:"category"` // sales, support, billing, appointment, personal, spam or other
	Reason   string `json:"reason"`
}

var urgencies = map[string]bool{"urgent": true, "normal": true, "low": true}
var categories = map[string]bool{"sales": true, "support": true, "billing": true, "appointment": true, "personal": true, "spam": true, "other": true}

// TriageVoicemail sorts a message by urgency and kind, from its transcript.
func (c *Client) TriageVoicemail(ctx context.Context, transcript string) (Triage, error) {
	var t Triage
	if strings.TrimSpace(transcript) == "" {
		return t, nil
	}
	err := c.JSON(ctx, "You sort voicemails for an office. Give urgency: urgent (someone is hurt, a deadline today, an outage, an angry customer), "+
		"normal, or low (marketing, no request). Give category: sales, support, billing, appointment, personal, spam or other. "+
		`Give a short reason. Fields: {"urgency":"","category":"","reason":""}`, transcript, &t)
	if err != nil {
		return Triage{}, err
	}
	t.Urgency, t.Category = strings.ToLower(t.Urgency), strings.ToLower(t.Category)
	if !urgencies[t.Urgency] {
		t.Urgency = "normal"
	}
	if !categories[t.Category] {
		t.Category = "other"
	}
	if len(t.Reason) > 200 {
		t.Reason = t.Reason[:200]
	}
	return t, nil
}

// Speak turns text into speech (an OpenAI-compatible /audio/speech service) and returns WAV audio.
// model and voice are the speech model's; the chat model's service is used.
func (c *Client) Speak(ctx context.Context, model, voice, text string) ([]byte, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if voice == "" {
		voice = "alloy"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "input": text, "voice": voice, "response_format": "wav"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai: the speech service answered %d (it needs an OpenAI-compatible /audio/speech)", resp.StatusCode)
	}
	if len(raw) < 12 || string(raw[:4]) != "RIFF" {
		return nil, errors.New("ai: the speech service did not send WAV audio")
	}
	return raw, nil
}

// Turn is the receptionist's next step in a conversation.
type Turn struct {
	Say    string `json:"say"`
	Action string `json:"action"` // answer (keep talking), transfer, message, callback, goodbye
	// Callback holds what the caller confirmed when Action is callback.
	Callback *CallbackRequest `json:"callback,omitempty"`
}

// CallbackRequest is a caller's request to be rung back, in their words.
type CallbackRequest struct {
	Name   string `json:"name"`
	Number string `json:"number"`
	When   string `json:"when"`
	Reason string `json:"reason"`
}

// Reception decides what the receptionist says and does next, given the knowledge list and the
// conversation so far (alternating caller and assistant lines, the caller's last).
func (c *Client) Reception(ctx context.Context, org, knowledge, callerNumber string, history []string) (Turn, error) {
	var t Turn
	system := "You are the automated receptionist of " + org + " on a phone call. Be brief and friendly: one or two short sentences. " +
		"Answer only from the approved information below; for anything else, offer to take a message or to transfer to a person. " +
		"Never make up facts, prices, appointments or promises. If the caller asks for a person, wants to talk to someone, or seems upset, use transfer. " +
		"If they want to leave a message, use message. " +
		"If they want someone to call them back, ask (one question at a time) for their name, the number to call (offer the number they are calling from: " + orUnknown(callerNumber) + "), a good time and what it is about; " +
		"once you have all four, use callback with those details (BitVoice then reads them back and asks the caller to confirm, so do not ask again). Never promise an exact time. " +
		"When the conversation is over, use goodbye. Otherwise use answer.\n" +
		`Fields: {"say": "...", "action": "answer|transfer|message|callback|goodbye", "callback": {"name": "...", "number": "...", "when": "...", "reason": "..."}} (callback only with the callback action)` +
		"\n\nApproved information:\n" + knowledge
	var conv strings.Builder
	for i, line := range history {
		who := "Caller"
		if i%2 == 1 {
			who = "You"
		}
		fmt.Fprintf(&conv, "%s: %s\n", who, line)
	}
	if err := c.JSON(ctx, system, conv.String(), &t); err != nil {
		return t, err
	}
	switch t.Action {
	case "answer", "transfer", "message", "goodbye":
		t.Callback = nil
	case "callback":
		if t.Callback == nil {
			t.Action = "answer"
		} else {
			cb := t.Callback
			cb.Name, cb.When, cb.Reason = clip(cb.Name, 100), clip(cb.When, 200), clip(cb.Reason, 500)
			cb.Number = phoneNumber(cb.Number)
			if cb.Number == "" {
				cb.Number = phoneNumber(callerNumber)
			}
			// Every detail is needed; the model sometimes books with one missing, so ask for it instead.
			missing := ""
			switch {
			case cb.Name == "":
				missing = "May I have your name, please?"
			case cb.Number == "":
				missing = "What number should we call you back on?"
			case cb.When == "":
				missing = "When is a good time to call you back?"
			case cb.Reason == "":
				missing = "And what is it about?"
			}
			if missing != "" {
				t.Action, t.Callback, t.Say = "answer", nil, missing
			}
		}
	default:
		t.Action, t.Callback = "answer", nil
	}
	if len(t.Say) > 400 {
		t.Say = t.Say[:400]
	}
	return t, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "not known"
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// phoneNumber keeps the digits (and a leading +) of something a caller or the model said was a number,
// or returns "" when it cannot be dialled.
func phoneNumber(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		}
	}
	n := b.String()
	if d := strings.TrimPrefix(n, "+"); len(d) < 3 || len(d) > 15 {
		return ""
	}
	return n
}
