package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Ask in plain words: an administrator describes a change ("forward sales to my mobile after 5 pm"),
// the model proposes new versions of the parts of the configuration it touches, and BitVoice checks the
// proposal and shows exactly what would change. Nothing changes until the administrator applies it.

// askable are the parts of the configuration the model may propose new versions of.
var askable = []string{"extensions", "ringGroups", "queues", "menus", "schedules", "scripts", "inboundRoutes", "outboundRoutes", "pageGroups"}

type proposal struct {
	cfg  config.Config
	rev  int
	at   time.Time
	text string
}

type askState struct {
	mu   sync.Mutex
	open map[string]proposal
}

const askSystem = `You change the configuration of BitVoice, an office phone system, as the administrator asks.
You are given the current configuration as JSON (passwords removed) and the request.
Answer with {"explanation": "...", "changes": {...}} where changes holds the complete new value of each
top-level list you change (only these may change: %s), keeping every item and field you do not mean to
change exactly as it was, ids included. New items get no id. Destinations are {"type": ..., "target": ...}
with type extension, ringgroup, queue, menu, schedule, script, voicemail, echo or hangup. If the request is
unclear or impossible, answer with no changes and say why in explanation.`

func (s *Server) propose(ctx context.Context, request string) (string, []config.Change, string, error) {
	cfg, rev := s.Store.Config()
	a := s.config().System.AI
	if !a.Enabled {
		return "", nil, "", errors.New("switch on AI in Settings first")
	}
	view := cfg.Clone()
	config.StripSecrets(&view)
	parts := map[string]any{}
	raw, _ := json.Marshal(view)
	var whole map[string]json.RawMessage
	_ = json.Unmarshal(raw, &whole)
	for _, k := range askable {
		parts[k] = whole[k]
	}
	cur, _ := json.Marshal(parts)
	var out struct {
		Explanation string                     `json:"explanation"`
		Changes     map[string]json.RawMessage `json:"changes"`
	}
	c := s.aiClient(a)
	prompt := fmt.Sprintf("Current configuration:\n%s\n\nRequest: %s", cur, request)
	if err := c.JSON(ctx, fmt.Sprintf(askSystem, strings.Join(askable, ", ")), prompt, &out); err != nil {
		return "", nil, "", err
	}
	if len(out.Changes) == 0 {
		return "", nil, out.Explanation, nil
	}
	for k, v := range out.Changes {
		allowed := false
		for _, a := range askable {
			allowed = allowed || a == k
		}
		if !allowed {
			return "", nil, out.Explanation, fmt.Errorf("the proposal changes %s, which is not allowed here", k)
		}
		whole[k] = v
	}
	merged, _ := json.Marshal(whole)
	var next config.Config
	dec := json.NewDecoder(strings.NewReader(string(merged)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return "", nil, out.Explanation, fmt.Errorf("the proposal is not a configuration BitVoice understands: %w", err)
	}
	config.KeepSecrets(&next, cfg)
	check := next.Clone()
	if err := check.Validate(); err != nil {
		var p config.Problems
		if errors.As(err, &p) && len(p) > 0 {
			return "", nil, out.Explanation, fmt.Errorf("the proposal would not be valid: %s: %s", p[0].Field, p[0].Message)
		}
		return "", nil, out.Explanation, err
	}
	changes := config.Diff(cfg, check, 200)
	if len(changes) == 0 {
		return "", nil, out.Explanation, nil
	}
	id := token(8)
	s.ask.mu.Lock()
	if s.ask.open == nil {
		s.ask.open = map[string]proposal{}
	}
	for k, p := range s.ask.open {
		if time.Since(p.at) > 15*time.Minute {
			delete(s.ask.open, k)
		}
	}
	s.ask.open[id] = proposal{cfg: next, rev: rev, at: time.Now(), text: request}
	s.ask.mu.Unlock()
	return id, changes, out.Explanation, nil
}

func (s *Server) askRoutes() {
	s.handle("POST /api/ask", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Request string `json:"request"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Request = strings.TrimSpace(in.Request)
		if in.Request == "" || len(in.Request) > 1000 {
			writeErr(w, config.Problems{{Field: "request", Message: "say what you want changed, in up to 1000 characters"}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		id, changes, why, err := s.propose(ctx, in.Request)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": err.Error(), "explanation": why})
			return
		}
		if changes == nil {
			changes = []config.Change{}
		}
		s.audit(r, u.Username, "ask", in.Request)
		writeJSON(w, 200, map[string]any{"id": id, "explanation": why, "changes": changes})
	})
	s.handle("POST /api/ask/{id}/apply", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.ask.mu.Lock()
		p, ok := s.ask.open[r.PathValue("id")]
		delete(s.ask.open, r.PathValue("id"))
		s.ask.mu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "that proposal has expired; ask again")
			return
		}
		_, rev, err := s.Store.Update(u.Username, "Asked: "+p.text, p.rev, func(c *config.Config) error {
			*c = p.cfg
			return nil
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "ask.apply", p.text)
		writeJSON(w, 200, map[string]int{"rev": rev})
	})
}
