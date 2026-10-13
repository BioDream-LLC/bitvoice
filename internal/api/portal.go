package api

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The staff portal: people sign in with their extension number and voicemail PIN and manage their own
// calls (forwarding, do not disturb, follow-me, speed dials), voicemail, greeting and call history.
// A portal session reaches only the /api/me routes; every other route refuses it.

const portalRole = "extension"

// portalUser is the session user for an extension.
func portalUser(x *config.Extension) store.User {
	return store.User{Username: "ext:" + x.Number, Name: x.Name, Role: portalRole}
}

// handlePortal registers a route for a portal session; ext is the signed-in extension (it still exists
// and is enabled, checked on every request).
func (s *Server) handlePortal(pattern string, f func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension)) {
	s.noteRoute(pattern, false, false)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.user(r)
		if !ok || u.Role != portalRole {
			writeError(w, http.StatusUnauthorized, "sign in to the staff portal first")
			return
		}
		cfg, _ := s.Store.Config()
		x := cfg.FindExtension(strings.TrimPrefix(u.Username, "ext:"))
		if x == nil || !x.Enabled {
			writeError(w, http.StatusUnauthorized, "this extension is no longer in use")
			return
		}
		f(w, r, u, *x)
	})
}

func (s *Server) portalRoutes() {
	s.mux.HandleFunc("POST /api/portal/login", s.portalLogin)
	s.noteRoute("POST /api/portal/login", false, true)

	s.handlePortal("GET /api/me", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		cfg, _ := s.Store.Config()
		fresh, old := s.Store.VoicemailCounts(x.Number)
		codes := cfg.System.Codes
		writeJSON(w, 200, map[string]any{
			"number": x.Number, "name": x.Name, "email": x.Email, "voicemail": x.Voicemail.Enabled,
			"followMe": x.FollowMe, "speedDials": nonNil(x.SpeedDials), "companySpeedDials": nonNil(cfg.System.SpeedDials),
			"pickupGroup": x.PickupGroup, "noIntercom": x.NoIntercom, "outbound": x.Outbound,
			"divert": s.Engine.DivertOf(x.Number), "dnd": s.Engine.DND(x.Number), "followMeOn": s.Engine.FollowMeOn(&x),
			"messages": map[string]int{"new": fresh, "old": old}, "codes": codes,
			"voicemailNumber": cfg.System.VoicemailNumber,
		})
	})
	s.handlePortal("PUT /api/me/divert", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		s.putDivert(w, r, u, x.Number)
	})
	// Follow-me and the person's own speed dials are part of the extension's configuration: a change
	// is a revision, made by the person.
	s.handlePortal("PUT /api/me/settings", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		var in struct {
			FollowMe   *config.FollowMe   `json:"followMe"`
			SpeedDials *[]config.SpeedDial `json:"speedDials"`
			NoIntercom *bool              `json:"noIntercom"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		prefix := ""
		_, rev, err := s.Store.Update(u.Username, "Own settings of extension "+x.Number, expectRev(r), func(c *config.Config) error {
			e := c.FindExtension(x.Number)
			for i := range c.Extensions {
				if c.Extensions[i].Number == x.Number {
					prefix = fmt.Sprintf("extensions[%d]", i)
				}
			}
			if e == nil {
				return errors.New("no such extension")
			}
			if in.FollowMe != nil {
				e.FollowMe = *in.FollowMe
			}
			if in.SpeedDials != nil {
				e.SpeedDials = *in.SpeedDials
			}
			if in.NoIntercom != nil {
				e.NoIntercom = *in.NoIntercom
			}
			return nil
		})
		if err != nil {
			writeErr(w, scopeProblems(err, prefix))
			return
		}
		writeJSON(w, 200, map[string]int{"rev": rev})
	})
	s.handlePortal("PUT /api/me/pin", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		var in struct {
			Old string `json:"old"`
			New string `json:"new"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if subtle.ConstantTimeCompare([]byte(in.Old), []byte(x.Voicemail.PIN)) != 1 {
			writeError(w, 403, "the current PIN is not right")
			return
		}
		if !pinOK(in.New) {
			writeJSON(w, 422, map[string]any{"message": "the new PIN is 4 to 10 digits", "problems": config.Problems{{Field: "new", Message: "the new PIN is 4 to 10 digits"}}})
			return
		}
		_, _, err := s.Store.Update(u.Username, "PIN of extension "+x.Number, 0, func(c *config.Config) error {
			e := c.FindExtension(x.Number)
			if e == nil {
				return errors.New("no such extension")
			}
			e.Voicemail.PIN = in.New
			return nil
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "portal.pin", x.Number)
		w.WriteHeader(204)
	})
	s.handlePortal("GET /api/me/voicemail", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		list, err := s.Store.Voicemails(x.Number)
		if err != nil {
			writeErr(w, err)
			return
		}
		_, custom := s.Store.Greeting(x.Number)
		writeJSON(w, 200, map[string]any{"items": nonNil(list), "greeting": custom == nil})
	})
	s.handlePortal("GET /api/me/voicemail/{id}/audio", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		b, err := s.Store.VoicemailAudio(x.Number, r.PathValue("id"))
		if err != nil {
			writeError(w, 404, "no such message")
			return
		}
		serveWAV(w, r, "voicemail-"+r.PathValue("id")+".wav", b)
	})
	s.handlePortal("PATCH /api/me/voicemail/{id}", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		var body struct {
			Heard bool `json:"heard"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		v, err := s.Store.SetHeard(x.Number, r.PathValue("id"), body.Heard)
		if err != nil {
			writeError(w, 404, "no such message")
			return
		}
		s.Engine.MailboxChanged(x.Number)
		writeJSON(w, 200, v)
	})
	s.handlePortal("DELETE /api/me/voicemail/{id}", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		if err := s.Store.DeleteVoicemail(x.Number, r.PathValue("id")); err != nil {
			if errors.Is(err, store.ErrHeld) {
				writeErr(w, err)
				return
			}
			writeError(w, 404, "no such message")
			return
		}
		s.audit(r, u.Username, "voicemail.delete", "mailbox "+x.Number)
		s.Engine.MailboxChanged(x.Number)
		w.WriteHeader(204)
	})
	s.handlePortal("GET /api/me/greeting", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		b, err := s.Store.Greeting(x.Number)
		if err != nil {
			writeError(w, 404, "your mailbox uses the default greeting")
			return
		}
		serveWAV(w, r, "greeting.wav", b)
	})
	s.handlePortal("PUT /api/me/greeting", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		s.putAudio(w, r, u, x.Number, "voicemail.greeting")
	})
	s.handlePortal("DELETE /api/me/greeting", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		if err := s.Store.SetGreeting(x.Number, nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
	s.handlePortal("GET /api/me/calls", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		list, err := s.Store.CDRsFor(x.Number, 200)
		if err != nil {
			writeErr(w, err)
			return
		}
		// Only the person's own calls, and never the recording (that is for administrators).
		out := make([]store.CDR, 0, len(list))
		for _, c := range list {
			if c.Ext == x.Number || c.AnsweredExt == x.Number || c.From == x.Number || c.To == x.Number {
				c.Recording = ""
				out = append(out, c)
			}
		}
		slices.SortFunc(out, func(a, b store.CDR) int { return b.Start.Compare(a.Start) })
		writeJSON(w, 200, map[string]any{"items": out})
	})
}

// portalLogin signs in to the staff portal with an extension number and its voicemail PIN.
func (s *Server) portalLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Extension string `json:"extension"`
		PIN       string `json:"pin"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	ip := clientIP(r).String()
	if s.throttled(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed sign-ins from this address; wait ten minutes")
		return
	}
	cfg, _ := s.Store.Config()
	x := cfg.FindExtension(strings.TrimSpace(in.Extension))
	// The same answer and the same work whichever part is wrong, so the portal does not say which
	// extensions exist.
	pin := ""
	if x != nil && x.Enabled {
		pin = x.Voicemail.PIN
	}
	good := subtle.ConstantTimeCompare([]byte(in.PIN), []byte(pin)) == 1 && pin != ""
	if !good {
		s.loginFailed(ip)
		time.Sleep(200 * time.Millisecond)
		s.audit(r, "ext:"+in.Extension, "portal.login.failed", "")
		writeError(w, http.StatusUnauthorized, "that extension and PIN do not match (the PIN is the voicemail PIN)")
		return
	}
	u := portalUser(x)
	s.startSession(w, r, u)
	s.audit(r, u.Username, "portal.login", "")
	writeJSON(w, 200, meResponse{u.Username, u.Name, u.Role, true, false})
}

func pinOK(p string) bool {
	if len(p) < 4 || len(p) > 10 {
		return false
	}
	return strings.Trim(p, "0123456789") == ""
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

var _ = pbx.Divert{}
