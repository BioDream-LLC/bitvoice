package api

import (
	"net"
	"net/http"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// ICEServer is one entry of a browser's RTCPeerConnection iceServers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

func (s *Server) iceRoutes() {
	// The TURN servers a browser phone or call link may use: short-lived credentials, for relaying to
	// this server only. Call links have no sign-in, so this needs none either.
	s.noteRoute("GET /api/ice", false, true)
	s.mux.HandleFunc("GET /api/ice", func(w http.ResponseWriter, r *http.Request) {
		out := []ICEServer{}
		if s.ICEServers != nil {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			out = append(out, s.ICEServers(host)...)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"iceServers": out})
	})
	s.handle("GET /api/turn", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		st := map[string]any{"enabled": false, "running": false}
		if s.TURNStatus != nil {
			st = s.TURNStatus()
		}
		writeJSON(w, 200, st)
	})
}
