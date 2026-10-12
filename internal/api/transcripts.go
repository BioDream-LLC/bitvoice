package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) transcriptRoutes() {
	// A recorded call's transcript, summary and action items. Reading one is audited like playing it.
	s.handle("GET /api/recordings/{month}/{name}/transcript", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		t, err := s.Store.CallTranscript(name)
		if err != nil {
			writeError(w, http.StatusNotFound, "this call has no transcript")
			return
		}
		s.audit(r, u.Username, "transcript.read", name)
		writeJSON(w, 200, t)
	})
	// Make (or make again) a recorded call's transcript, even when automatic transcripts are off.
	s.handle("POST /api/recordings/{month}/{name}/transcript", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		a := s.config().System.AI
		if !a.Enabled {
			writeErr(w, config.Problems{{Field: "ai", Message: "switch on AI in Settings first"}})
			return
		}
		cdr, ok := s.cdrForRecording(name)
		if !ok {
			writeError(w, http.StatusNotFound, "no such recording")
			return
		}
		if t, err := s.Store.CallTranscript(name); err == nil && t.Status == "pending" {
			writeJSON(w, 409, apiError{Message: "this call is being transcribed already"})
			return
		}
		s.audit(r, u.Username, "transcript.make", name)
		go s.transcribeCall(context.Background(), cdr, true)
		writeJSON(w, 202, map[string]string{"status": "pending"})
	})
	// Search recorded calls by what was said.
	s.handle("GET /api/transcripts", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) < 2 {
			writeErr(w, config.Problems{{Field: "q", Message: "type at least two letters to search for"}})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		ts := s.Store.SearchTranscripts(q, limit)
		if ts == nil {
			ts = []store.CallTranscript{}
		}
		s.audit(r, u.Username, "transcript.search", q)
		writeJSON(w, 200, map[string]any{"items": ts})
	})
}

// cdrForRecording finds the call record a recording belongs to.
func (s *Server) cdrForRecording(name string) (store.CDR, bool) {
	q := store.CDRQuery{Limit: 5000}
	if m, err := time.Parse("2006-01", strings.SplitN(name, "/", 2)[0]); err == nil {
		q.Since, q.Until = m.AddDate(0, 0, -1), m.AddDate(0, 1, 1) // the recording is named after its month
	}
	rs, err := s.Store.CDRs(q)
	if err != nil {
		return store.CDR{}, false
	}
	for _, c := range rs {
		if c.Recording == name {
			return c, true
		}
	}
	return store.CDR{}, false
}
