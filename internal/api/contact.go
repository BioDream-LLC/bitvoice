package api

import (
	"net/http"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// Contact-centre controls: agent presence and supervision.
func (s *Server) contactRoutes() {
	s.handle("GET /api/agents", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Agents()})
	})
	s.handle("POST /api/agents/{ext}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			LoggedOut bool   `json:"loggedOut"`
			Paused    bool   `json:"paused"`
			Reason    string `json:"reason"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		ext := r.PathValue("ext")
		if cfg.FindExtension(ext) == nil {
			writeError(w, http.StatusNotFound, "no extension "+ext)
			return
		}
		reason := strings.TrimSpace(in.Reason)
		if len(reason) > 60 {
			reason = reason[:60]
		}
		p := s.Engine.SetAgent(ext, in.LoggedOut, in.Paused, reason)
		what := "available"
		switch {
		case p.LoggedOut:
			what = "logged out"
		case p.Paused:
			what = "paused: " + p.Reason
		}
		s.audit(r, u.Username, "agent.state", ext+" "+what)
		writeJSON(w, 200, p)
	})
	s.handle("POST /api/calls/{id}/supervise", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Supervisor string `json:"supervisor"`
			Mode       string `json:"mode"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.Supervise(r.PathValue("id"), in.Supervisor, in.Mode); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.audit(r, u.Username, "call.supervise", in.Mode+" by "+in.Supervisor)
		writeJSON(w, 200, map[string]string{"status": "ringing " + in.Supervisor})
	})
	s.handle("PUT /api/calls/{id}/supervise", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Mode string `json:"mode"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.SetSupervision(r.PathValue("id"), in.Mode); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.audit(r, u.Username, "call.supervise", in.Mode)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
