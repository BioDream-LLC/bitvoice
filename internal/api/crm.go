package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Contact is who a number belongs to, from the CRM lookup or BitVoice's own extensions.
type Contact struct {
	Name    string `json:"name"`
	Company string `json:"company,omitempty"`
	URL     string `json:"url,omitempty"` // the contact's page in the CRM
	Notes   string `json:"notes,omitempty"`
	Source  string `json:"source"` // crm or extension
}

// crmState caches lookups and remembers how logging went, for Health.
type crmState struct {
	mu      sync.Mutex
	cache   map[string]cachedContact
	lastErr string
	lastAt  time.Time
	sent    int
}

type cachedContact struct {
	c   *Contact
	err string
	at  time.Time
}

var crmHTTP = &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// fill puts a number into a URL template, encoded so it cannot change the address's meaning.
func fill(tmpl, number string) string {
	return strings.ReplaceAll(tmpl, "{number}", url.QueryEscape(number))
}

// lookup says who a number is: an extension's name, or the CRM's answer (cached for five minutes,
// failures for one). nil means nobody knows.
func (s *Server) lookup(ctx context.Context, cfg config.Config, number string) (*Contact, error) {
	if x := cfg.FindExtension(number); x != nil {
		return &Contact{Name: x.Name, Source: "extension"}, nil
	}
	c, err := s.lookupCRM(ctx, cfg, number)
	if c == nil {
		if d := s.directoryContact(number); d != nil {
			return &Contact{Name: d.Name, Company: d.Company, Notes: d.Notes, Source: "directory"}, err
		}
	}
	return c, err
}

func (s *Server) lookupCRM(ctx context.Context, cfg config.Config, number string) (*Contact, error) {
	m := cfg.System.CRM
	if m.LookupURL == "" || number == "" {
		return nil, nil
	}
	s.crm.mu.Lock()
	if s.crm.cache == nil {
		s.crm.cache = map[string]cachedContact{}
	}
	if c, ok := s.crm.cache[number]; ok && (time.Since(c.at) < time.Minute || (c.err == "" && time.Since(c.at) < 5*time.Minute)) {
		s.crm.mu.Unlock()
		if c.err != "" {
			return nil, errors.New(c.err)
		}
		return c.c, nil
	}
	s.crm.mu.Unlock()
	c, err := s.askCRM(ctx, m, number)
	s.crm.mu.Lock()
	if len(s.crm.cache) > 5000 {
		clear(s.crm.cache)
	}
	e := ""
	if err != nil {
		e = err.Error()
	}
	s.crm.cache[number] = cachedContact{c, e, time.Now()}
	s.crm.mu.Unlock()
	return c, err
}

func (s *Server) askCRM(ctx context.Context, m config.CRM, number string) (*Contact, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fill(m.LookupURL, number), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BitVoice/"+s.Version)
	if m.LookupHeader != "" {
		req.Header.Set(m.LookupHeader, m.LookupSecret)
	}
	resp, err := crmHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the CRM did not answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the CRM answered %s", resp.Status)
	}
	var c Contact
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&c); err != nil {
		return nil, errors.New("the CRM's answer is not the JSON expected: {\"name\": ...}")
	}
	c.Name, c.Company, c.Notes = clipText(c.Name, 120), clipText(c.Company, 120), clipText(c.Notes, 500)
	if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		c.URL = "" // only web links reach the screen
	}
	if c.Name == "" && c.Company == "" {
		return nil, nil
	}
	c.Source = "crm"
	return &c, nil
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// CallerCard is what a screen shows about a caller while their call rings.
type CallerCard struct {
	Number    string           `json:"number"`
	Contact   *Contact         `json:"contact"`
	LookupErr string           `json:"lookupError,omitempty"`
	OpenURL   string           `json:"openUrl,omitempty"`
	Calls     int              `json:"calls"` // earlier calls with this number, in the last 90 days
	LastCall  *time.Time       `json:"lastCall,omitempty"`
	Recent    []store.CDR      `json:"recent,omitempty"`      // the last five (administrators and viewers)
	Callbacks []store.Callback `json:"callbacks"`             // open callbacks for this number
	Summary   string           `json:"lastSummary,omitempty"` // the last transcribed call's summary
}

// callerCard puts together what is known about number; full adds the call list and the last summary.
func (s *Server) callerCard(ctx context.Context, number string, full bool) CallerCard {
	cfg := s.config()
	card := CallerCard{Number: number, Callbacks: []store.Callback{}}
	c, err := s.lookup(ctx, cfg, number)
	card.Contact = c
	if err != nil {
		card.LookupErr = err.Error()
	}
	if cfg.System.CRM.OpenURL != "" && cfg.FindExtension(number) == nil {
		card.OpenURL = fill(cfg.System.CRM.OpenURL, number)
	}
	rs, _ := s.Store.CDRs(store.CDRQuery{Since: time.Now().AddDate(0, 0, -90), Search: number, Limit: 500})
	for _, r := range rs {
		if r.From != number && r.To != number {
			continue // the search also matches names and parts of numbers
		}
		card.Calls++
		if card.LastCall == nil {
			at := r.Start
			card.LastCall = &at
		}
		if full && len(card.Recent) < 5 {
			card.Recent = append(card.Recent, r)
		}
		if full && card.Summary == "" && r.Recording != "" {
			if t, err := s.Store.CallTranscript(r.Recording); err == nil && t.Status == "done" {
				card.Summary = t.Summary
			}
		}
	}
	digits := strings.TrimPrefix(number, "+")
	for _, cb := range s.Store.Callbacks() {
		if cb.Status == "open" && (cb.Number == number || strings.TrimPrefix(cb.Number, "+") == digits) {
			card.Callbacks = append(card.Callbacks, cb)
		}
	}
	return card
}

// RunCRM logs calls to the CRM webhook as they end, their summaries as they are made, and callbacks
// as they are booked. It runs until ctx ends.
func (s *Server) RunCRM(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			var event string
			var body any
			switch d := ev.Data.(type) {
			case store.CDR:
				if ev.Type == "call.logged" {
					event, body = "call.ended", map[string]any{"call": d}
				}
			case store.CallTranscript:
				if ev.Type == "transcript.done" {
					event, body = "call.summarized", map[string]any{"callId": d.CallID, "summary": d.Summary, "actionItems": d.ActionItems, "transcript": d.Text()}
				}
			case store.Callback:
				if ev.Type == "callback.new" {
					event, body = "callback.new", map[string]any{"callback": d}
				}
			}
			if event == "" {
				continue
			}
			cfg := s.config()
			if cfg.System.CRM.LogURL == "" {
				continue
			}
			go s.deliver(ctx, cfg.System.CRM, event, body, []time.Duration{0, 2 * time.Second, 15 * time.Second, time.Minute})
		}
	}
}

// deliver posts one event to the log webhook, trying again after each wait in turn.
func (s *Server) deliver(ctx context.Context, m config.CRM, event string, body any, waits []time.Duration) error {
	b, err := json.Marshal(map[string]any{"event": event, "at": time.Now().UTC(), "data": body})
	if err != nil {
		return err
	}
	for _, wait := range waits {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if err = postSigned(ctx, m, event, b, s.Version); err == nil {
			break
		}
	}
	s.crm.mu.Lock()
	s.crm.lastAt = time.Now()
	if err != nil {
		s.crm.lastErr = err.Error()
		s.Log.Warn("CRM call log failed", "event", event, "err", err)
	} else {
		s.crm.lastErr = ""
		s.crm.sent++
	}
	s.crm.mu.Unlock()
	return err
}

func postSigned(ctx context.Context, m config.CRM, event string, body []byte, version string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.LogURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(m.LogSecret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BitVoice/"+version)
	req.Header.Set("X-BitVoice-Event", event)
	req.Header.Set("X-BitVoice-Timestamp", ts)
	req.Header.Set("X-BitVoice-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := crmHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("the call log address did not answer: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the call log address answered %s", resp.Status)
	}
	return nil
}

// crmProblem is the last delivery failure, for Health, or "".
func (s *Server) crmProblem() string {
	s.crm.mu.Lock()
	defer s.crm.mu.Unlock()
	return s.crm.lastErr
}

func (s *Server) crmRoutes() {
	s.handle("GET /api/callers/{number}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		writeJSON(w, 200, s.callerCard(ctx, r.PathValue("number"), true))
	})
	// Try the settings before saving them: look a number up, or send the log webhook a test event.
	s.handle("POST /api/crm/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			CRM    config.CRM `json:"crm"`
			Number string     `json:"number"`
			What   string     `json:"what"` // lookup or log
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cur, _ := s.Store.Config()
		probe := cur.Clone()
		probe.System.CRM = in.CRM
		config.KeepSecrets(&probe, cur) // a secret left empty in the form keeps the saved one
		probe = s.Resolved(probe)
		m := probe.System.CRM
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		switch in.What {
		case "lookup":
			if m.LookupURL == "" {
				writeErr(w, config.Problems{{Field: "system.crm.lookupUrl", Message: "enter the lookup address first"}})
				return
			}
			c, err := s.askCRM(ctx, m, strings.TrimSpace(in.Number))
			if err != nil {
				writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
				return
			}
			if c == nil {
				writeJSON(w, 200, map[string]any{"ok": true, "message": "the CRM does not know " + in.Number})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "message": "found " + strings.TrimSpace(c.Name+" "+c.Company), "contact": c})
		case "log":
			if m.LogURL == "" || m.LogSecret == "" {
				writeErr(w, config.Problems{{Field: "system.crm.logUrl", Message: "enter the call log address and its secret first"}})
				return
			}
			if err := postSigned(ctx, m, "test", []byte(`{"event":"test","data":{"message":"BitVoice can reach this address"}}`), s.Version); err != nil {
				writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "message": "the call log address accepted a test event"})
		default:
			writeErr(w, config.Problems{{Field: "what", Message: "choose lookup or log"}})
		}
	})
}
