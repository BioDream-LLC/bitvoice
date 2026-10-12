package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/calendar"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

type calState struct {
	mu   sync.Mutex
	errs map[string]string // extension -> why its calendar could not be read
	at   map[string]time.Time
}

var calClient = &http.Client{Timeout: 15 * time.Second}

// fetchCalendar reads one private calendar address.
func fetchCalendar(ctx context.Context, addr string) (string, error) {
	addr = strings.Replace(addr, "webcal://", "https://", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return "", err
	}
	resp, err := calClient.Do(req)
	if err != nil {
		return "", errors.New("the calendar could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("the calendar answered " + resp.Status + ": check its private address")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(b[:min(len(b), 4096)]), "BEGIN:VCALENDAR") {
		return "", errors.New("that address does not give a calendar (.ics)")
	}
	return string(b), nil
}

// RunCalendars reads each extension's calendar every five minutes and marks it away during
// meetings (when it counts them) and time off.
func (s *Server) RunCalendars(ctx context.Context) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		s.readCalendars(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) readCalendars(ctx context.Context) {
	cfg := s.config()
	loc := cfg.System.Location()
	now := time.Now()
	for _, x := range cfg.Extensions {
		if x.Calendar.Secret == "" {
			if s.Engine != nil {
				s.Engine.ClearAway(x.Number, "calendar")
			}
			continue
		}
		body, err := fetchCalendar(ctx, x.Calendar.Secret)
		s.cal.mu.Lock()
		if s.cal.errs == nil {
			s.cal.errs, s.cal.at = map[string]string{}, map[string]time.Time{}
		}
		if err != nil {
			s.cal.errs[x.Number] = err.Error()
		} else {
			delete(s.cal.errs, x.Number)
			s.cal.at[x.Number] = now
		}
		s.cal.mu.Unlock()
		if err != nil || s.Engine == nil {
			continue
		}
		s.applyCalendar(x, calendar.Parse(body, now.Add(-24*time.Hour), now.Add(24*time.Hour), loc), now)
	}
}

func (s *Server) applyCalendar(x config.Extension, busy []calendar.Busy, now time.Time) {
	var keep []calendar.Busy
	for _, b := range busy {
		if b.Away || !x.Calendar.IgnoreMeetings {
			keep = append(keep, b)
		}
	}
	if b, ok := calendar.Now(keep, now); ok {
		reason := b.Summary
		if reason == "" {
			reason = map[bool]string{true: "Time off", false: "In a meeting"}[b.Away]
		}
		s.Engine.SetAway(pbx.Away{Ext: x.Number, Reason: reason, Until: b.End, Source: "calendar", OffSite: b.Away})
		return
	}
	s.Engine.ClearAway(x.Number, "calendar")
}

func (s *Server) calendarProblems() map[string]string {
	s.cal.mu.Lock()
	defer s.cal.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.cal.errs {
		out[k] = v
	}
	return out
}

func (s *Server) awayRoutes() {
	s.handle("GET /api/away", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Aways(), "problems": s.calendarProblems()})
	})
	// Do not disturb by hand: on (until a time, or until switched off) or off.
	s.handle("PUT /api/away/{ext}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			On     bool      `json:"on"`
			Reason string    `json:"reason"`
			Until  time.Time `json:"until"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		ext := r.PathValue("ext")
		cfg := s.config()
		if cfg.FindExtension(ext) == nil {
			writeError(w, 404, "no extension "+ext)
			return
		}
		if !in.On {
			s.Engine.ClearAway(ext, "manual")
			s.readCalendarOf(r.Context(), ext)
		} else {
			reason := strings.TrimSpace(in.Reason)
			if reason == "" {
				reason = "Do not disturb"
			}
			s.Engine.SetAway(pbx.Away{Ext: ext, Reason: oneLine(reason, ""), Until: in.Until, Source: "manual"})
		}
		s.audit(r, u.Username, "away.set", ext)
		w.WriteHeader(204)
	})
	// Check a calendar address now, before saving it.
	s.handle("POST /api/away/check", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Secret         string `json:"secret"`
			IgnoreMeetings bool   `json:"ignoreMeetings"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		body, err := fetchCalendar(ctx, in.Secret)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		cfg := s.config()
		loc := cfg.System.Location()
		now := time.Now()
		busy := calendar.Parse(body, now.Add(-12*time.Hour), now.Add(7*24*time.Hour), loc) // a meeting on now counts
		type item struct {
			Start, End time.Time
			Summary    string
			Away       bool
		}
		next := []item{}
		for _, b := range busy {
			if (b.Away || !in.IgnoreMeetings) && len(next) < 5 {
				next = append(next, item{b.Start, b.End, b.Summary, b.Away})
			}
		}
		writeJSON(w, 200, map[string]any{"events": len(busy), "next": next})
	})
}

// readCalendarOf reads one extension's calendar again (after do not disturb is switched off).
func (s *Server) readCalendarOf(ctx context.Context, ext string) {
	cfg := s.config()
	x := cfg.FindExtension(ext)
	if x == nil || x.Calendar.Secret == "" {
		return
	}
	if body, err := fetchCalendar(ctx, x.Calendar.Secret); err == nil {
		now := time.Now()
		s.applyCalendar(*x, calendar.Parse(body, now.Add(-24*time.Hour), now.Add(24*time.Hour), cfg.System.Location()), now)
	}
}
