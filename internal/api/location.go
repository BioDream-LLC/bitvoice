package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Emergency location: remote workers confirm where they are from their phone page, and emergency calls
// are emailed to the on-site list.

func checkAddress(a *config.Address) config.Problems {
	var p config.Problems
	a.Country = strings.ToUpper(strings.TrimSpace(a.Country))
	for _, f := range []struct{ name, v, msg string }{
		{"street", a.Street, "enter the street"}, {"number", a.Number, "enter the house or building number"},
		{"city", a.City, "enter the city"}, {"country", a.Country, "enter the two-letter country code"},
	} {
		if strings.TrimSpace(f.v) == "" {
			p = append(p, config.Problem{Field: f.name, Message: f.msg})
		}
	}
	if a.Country != "" && len(a.Country) != 2 {
		p = append(p, config.Problem{Field: "country", Message: "use the two-letter country code, such as US"})
	}
	return p
}

func (s *Server) locationRoutes() {
	// The phone page: is this phone away from its site, and where did its person say they are?
	s.mux.HandleFunc("GET /api/phone/{id}/location", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		a, at, ok := s.Store.RemoteAddress(bp.ext)
		out := map[string]any{"remote": s.Engine.RemoteFor(bp.ext, clientIP(r))}
		if ok {
			out["address"], out["confirmed"] = a, at
		}
		writeJSON(w, 200, out)
	})
	s.mux.HandleFunc("PUT /api/phone/{id}/location", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		var a config.Address
		if err := readJSON(r, &a); err != nil {
			writeErr(w, err)
			return
		}
		if p := checkAddress(&a); len(p) > 0 {
			writeErr(w, p)
			return
		}
		if err := s.Store.SetRemoteAddress(bp.ext, a); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, "extension "+bp.ext, "location.remote", strings.TrimSpace(a.Number+" "+a.Street+", "+a.City))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.handle("GET /api/extensions/{number}/remote", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		a, at, ok := s.Store.RemoteAddress(r.PathValue("number"))
		if !ok {
			writeJSON(w, 200, map[string]any{})
			return
		}
		writeJSON(w, 200, map[string]any{"address": a, "confirmed": at})
	})
}

// RunEmergencyEmail emails the on-site list when anyone dials an emergency number.
func (s *Server) RunEmergencyEmail(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if ev.Type != "emergency.call" {
				continue
			}
			d, ok := ev.Data.(map[string]any)
			if !ok {
				continue
			}
			cfg := s.config() // vault references resolved
			if len(cfg.System.EmergencyEmail) == 0 || cfg.System.Email.Host == "" {
				continue
			}
			subject := fmt.Sprintf("EMERGENCY: %v dialled from extension %v %v", d["number"], d["extension"], d["name"])
			body := fmt.Sprintf("%v dialled %v from extension %v at %s.\n\nLocation: %v\n\nOpen BitVoice to see the call live.\n",
				d["name"], d["number"], d["extension"], time.Now().In(cfg.System.Location()).Format("Mon 2 Jan 15:04:05"), d["location"])
			for _, to := range cfg.System.EmergencyEmail {
				go func(to string) {
					msg, err := buildMail(cfg.System.Email.From, to, subject, body, "", "", nil)
					if err == nil {
						err = sendMail(cfg.System.Email, to, msg)
					}
					if err != nil {
						s.Log.Warn("emergency email failed", "to", to, "err", err)
					}
				}(to)
			}
		}
	}
}
