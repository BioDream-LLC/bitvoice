package api

import (
	"net/http"
	"net/netip"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/wsgw"
)

// wsRoutes serves SIP over WebSocket at /ws (wss:// on the web interface's port), for WebRTC
// softphones. The WebSocket itself needs no sign-in: the softphone authenticates in SIP, as any phone.
func (s *Server) wsRoutes(m *http.ServeMux) {
	var once sync.Once
	var g *wsgw.Gateway
	gw := func() *wsgw.Gateway {
		once.Do(func() {
			if s.Engine == nil {
				return
			}
			g = &wsgw.Gateway{Media: s.Engine, Log: s.Log,
				EnginePort: s.Engine.SIPPort,
				Enabled:    func() bool { return s.config().System.WebSocket.Enabled },
				Banned:     s.Engine.IsBanned,
				MediaHost: func(r *http.Request) string {
					if pa := s.config().System.PublicAddress; pa != "" {
						if a, err := netip.ParseAddr(pa); err == nil && !clientIP(r).IsPrivate() && !clientIP(r).IsLoopback() {
							return a.String() // from outside: the address the router forwards
						}
					}
					return browserHost(r)
				}}
		})
		return g
	}
	s.noteRoute("GET /ws", false, true)
	m.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		if g := gw(); g != nil {
			g.ServeHTTP(w, r)
			return
		}
		http.Error(w, "no call engine", http.StatusServiceUnavailable)
	})
	s.handle("GET /api/websocket", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		n := 0
		if g := gw(); g != nil {
			n = g.Count()
		}
		writeJSON(w, 200, map[string]any{"enabled": s.config().System.WebSocket.Enabled, "connected": n})
	})
}
