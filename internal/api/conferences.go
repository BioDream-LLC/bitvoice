package api

import (
	"errors"
	"net/http"

	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// conferenceRoutes serve the conference rooms in use: who is in, and a leader's controls.
func (s *Server) conferenceRoutes() {
	s.handle("GET /api/conferences/live", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Conferences()})
	})
	roomErr := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, pbx.ErrNoRoom), errors.Is(err, pbx.ErrNoMember):
			writeError(w, 404, err.Error())
		default:
			writeError(w, 400, err.Error())
		}
	}
	s.handle("PUT /api/conferences/live/{number}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Locked bool `json:"locked"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.LockConference(r.PathValue("number"), in.Locked); err != nil {
			roomErr(w, err)
			return
		}
		s.audit(r, u.Username, "conference.lock", r.PathValue("number"))
		w.WriteHeader(204)
	})
	s.handle("PUT /api/conferences/live/{number}/members/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Muted bool `json:"muted"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.MuteConferenceMember(r.PathValue("number"), r.PathValue("id"), in.Muted); err != nil {
			roomErr(w, err)
			return
		}
		s.audit(r, u.Username, "conference.mute", r.PathValue("number")+" "+r.PathValue("id"))
		w.WriteHeader(204)
	})
	s.handle("DELETE /api/conferences/live/{number}/members/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Engine.RemoveConferenceMember(r.PathValue("number"), r.PathValue("id")); err != nil {
			roomErr(w, err)
			return
		}
		s.audit(r, u.Username, "conference.remove", r.PathValue("number")+" "+r.PathValue("id"))
		w.WriteHeader(204)
	})
}

// divertRoutes serve call forwarding, do not disturb and the follow-me switch, per extension.
func (s *Server) divertRoutes() {
	type state struct {
		pbx.Divert
		DND      bool `json:"dnd"`
		FollowOn bool `json:"followMeOn"` // follow-me rings now (configured, or switched by the person)
	}
	view := func(ext string) state {
		d := s.Engine.DivertOf(ext)
		st := state{Divert: d, DND: s.Engine.DND(ext)}
		cfg := s.config()
		if x := cfg.FindExtension(ext); x != nil {
			st.FollowOn = s.Engine.FollowMeOn(x)
		}
		return st
	}
	s.handle("GET /api/diverts", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []state{}
		for _, x := range s.config().Extensions {
			st := view(x.Number)
			if st.All != "" || st.Busy != "" || st.NoAnswer != "" || st.FollowMe != nil || st.DND {
				out = append(out, st)
			}
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("GET /api/diverts/{ext}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg := s.config()
		if cfg.FindExtension(r.PathValue("ext")) == nil {
			writeError(w, 404, "no extension "+r.PathValue("ext"))
			return
		}
		writeJSON(w, 200, view(r.PathValue("ext")))
	})
	s.handle("PUT /api/diverts/{ext}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.putDivert(w, r, u, r.PathValue("ext"))
	})
}

// putDivert sets an extension's forwarding, do not disturb and follow-me switch.
func (s *Server) putDivert(w http.ResponseWriter, r *http.Request, u store.User, ext string) {
	var in struct {
		pbx.Divert
		DND *bool `json:"dnd"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.Ext = ext
	if err := s.Engine.SetDivert(in.Divert); err != nil {
		if errors.Is(err, pbx.ErrNoExtension) {
			writeError(w, 404, err.Error())
			return
		}
		writeError(w, 422, err.Error())
		return
	}
	if in.DND != nil {
		s.Engine.SetDND(ext, *in.DND)
	}
	s.audit(r, u.Username, "divert.set", ext)
	w.WriteHeader(204)
}
