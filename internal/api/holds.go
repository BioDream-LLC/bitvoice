package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

var holdNumber = regexp.MustCompile(`^\+?[0-9*#]{2,20}$`)

type holdRequest struct {
	Name    string   `json:"name"`
	Reason  string   `json:"reason"`
	Numbers []string `json:"numbers"`
	Since   string   `json:"since"` // YYYY-MM-DD, empty for open-ended
	Until   string   `json:"until"`
}

func (in holdRequest) hold(tz *time.Location) (store.Hold, error) {
	var p config.Problems
	h := store.Hold{Name: strings.TrimSpace(in.Name), Reason: strings.TrimSpace(in.Reason), Numbers: []string{}}
	if h.Name == "" {
		p = append(p, config.Problem{Field: "name", Message: "name the hold, such as the matter or case number"})
	}
	for i, n := range in.Numbers {
		n = strings.TrimSpace(n)
		if !holdNumber.MatchString(n) {
			p = append(p, config.Problem{Field: "numbers[" + strconv.Itoa(i) + "]", Message: n + " is not an extension or phone number"})
			continue
		}
		h.Numbers = append(h.Numbers, n)
	}
	day := func(field, v string, end bool) time.Time {
		if v == "" {
			return time.Time{}
		}
		d, err := time.ParseInLocation("2006-01-02", v, tz)
		if err != nil {
			p = append(p, config.Problem{Field: field, Message: "use a date"})
			return time.Time{}
		}
		if end {
			d = d.Add(24*time.Hour - time.Nanosecond)
		}
		return d.UTC()
	}
	h.Since, h.Until = day("since", in.Since, false), day("until", in.Until, true)
	if !h.Since.IsZero() && !h.Until.IsZero() && h.Until.Before(h.Since) {
		p = append(p, config.Problem{Field: "until", Message: "the end is before the start"})
	}
	if len(p) > 0 {
		return h, p
	}
	return h, nil
}

func (s *Server) holdsRoutes() {
	tz := func() *time.Location {
		cfg, _ := s.Store.Config()
		if l, err := time.LoadLocation(cfg.System.Timezone); err == nil {
			return l
		}
		return time.UTC
	}
	s.handle("GET /api/holds", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		hs := s.Store.Holds()
		if hs == nil {
			hs = []store.Hold{}
		}
		writeJSON(w, 200, map[string]any{"items": hs})
	})
	s.handle("POST /api/holds", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in holdRequest
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		h, err := in.hold(tz())
		if err != nil {
			writeErr(w, err)
			return
		}
		h.ID, h.By = config.NewID(), u.Username
		h, err = s.Store.PutHold(h)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "hold.add", h.Name)
		writeJSON(w, 200, h)
	})
	s.handle("PUT /api/holds/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in holdRequest
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		h, err := in.hold(tz())
		if err != nil {
			writeErr(w, err)
			return
		}
		h.ID = r.PathValue("id")
		if h, err = s.Store.PutHold(h); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "hold.change", h.Name)
		writeJSON(w, 200, h)
	})
	s.handle("POST /api/holds/{id}/release", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.ReleaseHold(r.PathValue("id"), u.Username); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "no such hold")
				return
			}
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "hold.release", r.PathValue("id"))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.handle("POST /api/calls/{id}/recording", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Paused bool `json:"paused"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if !s.Engine.PauseRecording(r.PathValue("id"), in.Paused, u.Username) {
			writeError(w, http.StatusConflict, "this call is not being recorded")
			return
		}
		s.audit(r, u.Username, map[bool]string{true: "recording.pause", false: "recording.resume"}[in.Paused], r.PathValue("id"))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
