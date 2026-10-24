package api

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
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
	// probe gathers candidates with the settings given, as a call would, and changes nothing: what
	// BitVoice would offer a browser, so the settings can be checked before a call.
	s.handle("POST /api/ice/probe", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in config.ICE
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		saved := s.config().System.ICE
		for i, t := range in.TURN {
			for _, old := range saved.TURN {
				if t.Password == "" && old.Server == t.Server && old.Username == t.Username {
					in.TURN[i].Password = old.Password
				}
			}
		}
		probe := config.Default()
		probe.System.ICE = in
		probe.System.ICE.Full = true
		if err := probe.Validate(); err != nil {
			var pr config.Problems
			if errors.As(err, &pr) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "fix the settings first", "problems": pr})
				return
			}
			writeErr(w, err)
			return
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no UDP port free")
			return
		}
		var once sync.Once
		cands := webrtc.Probe(probe.System.ICE, browserHost(r), func() (func([]byte, netip.AddrPort), func(func([]byte, netip.AddrPort)), int, func()) {
			send := func(b []byte, to netip.AddrPort) { conn.WriteToUDPAddrPort(b, to) }
			recv := func(f func([]byte, netip.AddrPort)) {
				go func() {
					buf := make([]byte, 2048)
					for {
						n, from, err := conn.ReadFromUDPAddrPort(buf)
						if err != nil {
							return
						}
						f(append([]byte(nil), buf[:n]...), netip.AddrPortFrom(from.Addr().Unmap(), from.Port()))
					}
				}()
			}
			return send, recv, conn.LocalAddr().(*net.UDPAddr).Port, func() { once.Do(func() { conn.Close() }) }
		})
		type cand struct {
			Type    string `json:"type"`
			Address string `json:"address"`
			Base    string `json:"base,omitempty"`
		}
		out := []cand{}
		kinds := map[string]bool{}
		for _, c := range cands {
			x := cand{Type: string(c.Type), Address: c.Addr.String()}
			if c.Related.IsValid() && !c.Related.Addr().IsUnspecified() {
				x.Base = c.Related.String()
			}
			out = append(out, x)
			kinds[string(c.Type)] = true
		}
		s.audit(r, u.Username, "ice.probe", "")
		writeJSON(w, 200, map[string]any{"candidates": out, "srflx": kinds["srflx"], "relay": kinds["relay"]})
	})
	s.handle("GET /api/turn", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		st := map[string]any{"enabled": false, "running": false}
		if s.TURNStatus != nil {
			st = s.TURNStatus()
		}
		writeJSON(w, 200, st)
	})
}
