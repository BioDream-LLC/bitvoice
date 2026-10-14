package api

import (
	"net/http"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) flowRoutes() {
	// Which toggles are switched over, for the Toggles page and the dashboard.
	s.handle("GET /api/toggles/state", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		on := []string{}
		if s.Engine != nil {
			on = append(on, s.Engine.TogglesOn()...)
		}
		writeJSON(w, 200, map[string]any{"override": on})
	})
	// Switch a toggle over or back: anyone signed in to the web interface may, as from a phone.
	s.handle("POST /api/toggles/{id}/state", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var body struct {
			Override bool `json:"override"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if s.Engine == nil || !s.Engine.SetToggle(r.PathValue("id"), body.Override, "user "+u.Name) {
			writeError(w, 404, "no such toggle")
			return
		}
		writeJSON(w, 200, map[string]any{"override": body.Override})
	})
	// Try a caller ID lookup with a number, as saved: for the Test button.
	s.handle("POST /api/lookups/{id}/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var body struct {
			Number string `json:"number"`
		}
		if err := readJSON(r, &body); err != nil || body.Number == "" {
			writeError(w, 400, "enter a number to look up")
			return
		}
		cfg := s.config()
		l := cfg.FindLookup(r.PathValue("id"))
		if l == nil || s.Engine == nil {
			writeError(w, 404, "no such lookup: save it first")
			return
		}
		name, err := s.Engine.TestLookup(r.Context(), l, body.Number)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "name": name})
	})
}

func registerFlow(s *Server) {
	register(s, collection[config.AudioStream]{path: "audio-streams", noun: "audio stream",
		list:  func(c *config.Config) *[]config.AudioStream { return &c.AudioStreams },
		id:    func(x *config.AudioStream) *string { return &x.ID },
		label: func(x *config.AudioStream) string { return x.Name }})
	register(s, collection[config.Toggle]{path: "toggles", noun: "toggle",
		list:  func(c *config.Config) *[]config.Toggle { return &c.Toggles },
		id:    func(x *config.Toggle) *string { return &x.ID },
		label: func(x *config.Toggle) string { return x.Name }})
	register(s, collection[config.PINSet]{path: "pin-sets", noun: "PIN set",
		list:  func(c *config.Config) *[]config.PINSet { return &c.PINSets },
		id:    func(x *config.PINSet) *string { return &x.ID },
		label: func(x *config.PINSet) string { return x.Name },
		redact: func(x *config.PINSet) {
			hidden := make([]string, len(x.PINs)) // a new list: never write over the stored one
			for i := range hidden {
				hidden[i] = "••••"
			}
			x.PINs = hidden
		}})
	register(s, collection[config.CallerIDLookup]{path: "lookups", noun: "caller ID lookup",
		list:  func(c *config.Config) *[]config.CallerIDLookup { return &c.Lookups },
		id:    func(x *config.CallerIDLookup) *string { return &x.ID },
		label: func(x *config.CallerIDLookup) string { return x.Name },
		redact: func(x *config.CallerIDLookup) {
			if x.Password != "" {
				x.Password = "••••"
			}
		},
		keep: func(old, x *config.CallerIDLookup) {
			if x.Password == "••••" || (x.Password == "" && x.Username == old.Username) {
				x.Password = old.Password
			}
		}})
}
