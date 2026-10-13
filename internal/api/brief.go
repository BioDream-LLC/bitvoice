package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Briefing is a short summary for the person a call was passed to.
type Briefing struct {
	CallID   string    `json:"callId"`
	Target   string    `json:"target"`
	From     string    `json:"from"`
	FromName string    `json:"fromName,omitempty"`
	By       string    `json:"by,omitempty"`
	Summary  string    `json:"summary"`
	At       time.Time `json:"at"`
}

type briefings struct {
	mu sync.Mutex
	m  map[string]Briefing // by call ID, for an hour
}

const briefPrompt = `You brief a colleague who is about to take over a phone call. From the conversation so far, write at most two short sentences: who is calling, what they want, and anything already done or promised. Plain words, no greeting, no preamble. If nothing useful was said, answer exactly: Nothing useful was said yet.`

// Brief writes the briefing for a call being passed on: transcribes what was said (or takes the
// receptionist's text), summarises it, shows it on the browser phone and sends it to the desk phone.
func (s *Server) Brief(b pbx.BriefRequest) {
	cfg := s.config()
	a := cfg.System.AI
	if !a.Enabled || !a.Briefings {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c := s.aiClient(a)
	text := b.Text
	if text == "" {
		lines, _, _, err := transcribePCM(ctx, c, b.Caller, b.Callee, media.Rate, 40)
		if err != nil && len(lines) == 0 {
			s.Log.Warn("briefing: the call could not be transcribed", "call", b.CallID, "err", err)
			return
		}
		t := store.CallTranscript{Lines: lines}
		text = t.Text()
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	sum, err := c.Text(ctx, briefPrompt, text)
	if err != nil {
		s.Log.Warn("briefing: no summary", "call", b.CallID, "err", err)
		return
	}
	sum = strings.TrimSpace(sum)
	if sum == "" || strings.HasPrefix(sum, "Nothing useful was said") {
		return
	}
	br := Briefing{CallID: b.CallID, Target: b.Target, From: b.From, FromName: b.FromName, By: b.By, Summary: sum, At: time.Now().UTC()}
	s.briefs.mu.Lock()
	if s.briefs.m == nil {
		s.briefs.m = map[string]Briefing{}
	}
	for k, v := range s.briefs.m {
		if time.Since(v.At) > time.Hour {
			delete(s.briefs.m, k)
		}
	}
	s.briefs.m[b.CallID] = br
	s.briefs.mu.Unlock()
	s.Bus.Publish("call.briefing", br)
	if s.Engine != nil && cfg.FindExtension(b.Target) != nil {
		who := firstNonEmpty(b.FromName, b.From)
		s.Engine.SendText(b.Target, "Briefing", "Call from "+who+": "+sum)
	}
}

func (s *Server) briefingFor(callID string) (Briefing, bool) {
	s.briefs.mu.Lock()
	defer s.briefs.mu.Unlock()
	b, ok := s.briefs.m[callID]
	return b, ok
}

func (s *Server) briefRoutes() {
	s.handle("GET /api/calls/{id}/briefing", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, ok := s.briefingFor(r.PathValue("id"))
		if !ok {
			writeError(w, 404, "no briefing for this call")
			return
		}
		writeJSON(w, 200, b)
	})
}
