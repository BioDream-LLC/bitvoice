package api

import (
	"errors"
	"net/http"

	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) hotDeskRoutes() {
	s.handle("GET /api/hotdesks", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg := s.config()
		type desk struct {
			Number string       `json:"number"`
			Name   string       `json:"name"`
			In     *pbx.HotDesk `json:"in"`
		}
		in := map[string]pbx.HotDesk{}
		for _, h := range s.Engine.HotDesks() {
			in[h.Desk] = h
		}
		out := []desk{}
		for _, x := range cfg.Extensions {
			if x.HotDesk {
				d := desk{Number: x.Number, Name: x.Name}
				if h, ok := in[x.Number]; ok {
					d.In = &h
				}
				out = append(out, d)
			}
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	// An administrator signs someone in at a desk without their PIN.
	s.handle("POST /api/hotdesks/{desk}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			User string `json:"user"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		h, err := s.Engine.SignIn(r.PathValue("desk"), in.User, "", true)
		switch {
		case errors.Is(err, pbx.ErrNotHotDesk):
			writeError(w, 404, err.Error())
			return
		case err != nil:
			writeError(w, 400, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "hotdesk.signin", Detail: h.User + " at " + h.Desk, From: clientIP(r).String()})
		writeJSON(w, 200, h)
	})
	s.handle("DELETE /api/hotdesks/{desk}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !s.Engine.SignOut(r.PathValue("desk")) {
			writeError(w, 404, "nobody is signed in at that desk")
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "hotdesk.signout", Detail: r.PathValue("desk"), From: clientIP(r).String()})
		w.WriteHeader(204)
	})
}
