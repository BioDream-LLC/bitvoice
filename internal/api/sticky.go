package api

import (
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// lastAgents remembers who last answered each outside caller, for sticky queues.
type lastAgents struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]seenBy // by the number's last ten digits
}

type seenBy struct {
	ext string
	at  time.Time
}

func numberKey(n string) string {
	d := digitsOf(n)
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return d
}

// noteAnswered records an answered inbound call.
func (s *Server) noteAnswered(r store.CDR) {
	if r.Direction != "inbound" || r.AnsweredExt == "" || r.Answer.IsZero() {
		return
	}
	k := numberKey(r.From)
	if k == "" {
		return
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	if s.agents.m == nil {
		s.agents.m = map[string]seenBy{}
	}
	if cur, ok := s.agents.m[k]; !ok || r.Answer.After(cur.at) {
		s.agents.m[k] = seenBy{r.AnsweredExt, r.Answer}
	}
}

// LastAgent is the extension that last answered this caller after since, or "".
func (s *Server) LastAgent(number string, since time.Time) string {
	s.agents.mu.Lock()
	loaded := s.agents.loaded
	s.agents.loaded = true
	s.agents.mu.Unlock()
	if !loaded { // the first ask reads the last 90 days of call records
		rs, _ := s.Store.CDRs(store.CDRQuery{Since: time.Now().AddDate(0, 0, -90), Direction: "inbound", Limit: 5000})
		for _, r := range rs {
			s.noteAnswered(r)
		}
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	if v, ok := s.agents.m[numberKey(number)]; ok && v.at.After(since) {
		return v.ext
	}
	return ""
}
