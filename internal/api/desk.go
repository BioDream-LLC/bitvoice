package api

import (
	"net/http"

	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) deskRoutes() {
	// Each Skinny phone's and MGCP line's state, for the Desk phones page.
	s.handle("GET /api/desk", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := map[string]map[string]string{"sccp": {}, "mgcp": {}}
		if s.DeskStatus != nil {
			out = s.DeskStatus()
		}
		writeJSON(w, 200, out)
	})
}
