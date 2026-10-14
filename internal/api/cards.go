package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// cardsState is what bitvoice-dahdi last reported.
type cardsState struct {
	mu       sync.Mutex
	channels map[string]string
	seen     time.Time
	version  string
}

// cardsAuth admits bitvoice-dahdi: from this computer only, with the token set in Settings.
func (s *Server) cardsAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.Tenant != nil {
		writeError(w, http.StatusNotFound, "telephony cards belong to the hosting server")
		return false
	}
	if !clientIP(r).IsLoopback() {
		writeError(w, http.StatusForbidden, "bitvoice-dahdi must run on this computer")
		return false
	}
	k := s.config().System.Cards
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !k.Enabled || k.Token == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(k.Token)) != 1 {
		writeError(w, http.StatusUnauthorized, "telephony cards are off, or the token is wrong")
		return false
	}
	return true
}

func (s *Server) cardsRoutes() {
	// bitvoice-dahdi learns its ports, the extensions' passwords and the SIP port.
	s.noteRoute("GET /api/cards/bridge", false, true)
	s.mux.HandleFunc("GET /api/cards/bridge", func(w http.ResponseWriter, r *http.Request) {
		if !s.cardsAuth(w, r) {
			return
		}
		c := s.config()
		k := c.System.Cards
		out := dahdi.Config{SIPPort: s.Engine.SIPPort(), LinePort: k.LinePort, ALaw: k.ALaw, Channels: []dahdi.ChannelConf{}}
		for _, ch := range k.Channels {
			cc := dahdi.ChannelConf{Channel: ch.Channel, Kind: ch.Kind, Number: ch.Number}
			if ch.Kind == "phone" {
				x := c.FindExtension(ch.Extension)
				if x == nil || !x.Enabled {
					continue
				}
				cc.Extension, cc.Secret = x.Number, x.Secret
			}
			out.Channels = append(out.Channels, cc)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	s.noteRoute("POST /api/cards/status", false, true)
	s.mux.HandleFunc("POST /api/cards/status", func(w http.ResponseWriter, r *http.Request) {
		if !s.cardsAuth(w, r) {
			return
		}
		var in struct {
			Channels map[string]string `json:"channels"`
			Version  string            `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			writeError(w, 400, "send the channels' states as JSON")
			return
		}
		s.cards.mu.Lock()
		s.cards.channels, s.cards.seen, s.cards.version = in.Channels, time.Now(), in.Version
		s.cards.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// The Telephony cards page: whether bitvoice-dahdi is running, and each port's state.
	s.handle("GET /api/cards", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.cards.mu.Lock()
		defer s.cards.mu.Unlock()
		running := !s.cards.seen.IsZero() && time.Since(s.cards.seen) < 45*time.Second
		chans := map[string]string{}
		if running {
			chans = s.cards.channels
		}
		writeJSON(w, 200, map[string]any{"running": running, "seen": s.cards.seen, "version": s.cards.version, "channels": chans})
	})
}
