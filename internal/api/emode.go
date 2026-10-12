package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// SetEmergencyMode switches emergency mode on or off, from the screen or a phone's feature code.
func (s *Server) SetEmergencyMode(on bool, by string) error {
	_, _, err := s.Store.Update(by, map[bool]string{true: "Emergency mode on", false: "Emergency mode off"}[on], 0, func(c *config.Config) error {
		em := &c.System.EmergencyMode
		if on && em.Destination.Type == "" {
			return errors.New("choose where calls go in emergency mode first (Settings, Emergency mode)")
		}
		em.On = on
		em.By, em.Since = by, time.Time{}
		if on {
			em.Since = time.Now().UTC()
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Store.Audit(store.AuditEntry{Actor: by, Action: map[bool]string{true: "emergency.mode.on", false: "emergency.mode.off"}[on]})
	if s.Bus != nil {
		s.Bus.Publish("emergency.mode", map[string]any{"on": on, "by": by})
		s.Bus.Publish("audit.added", map[string]string{"action": "emergency.mode"})
	}
	return nil
}

func (s *Server) emergencyModeRoutes() {
	s.handle("POST /api/emergency-mode", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			On bool `json:"on"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.SetEmergencyMode(in.On, u.Username); err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		writeJSON(w, 200, cfg.System.EmergencyMode)
	})
}
