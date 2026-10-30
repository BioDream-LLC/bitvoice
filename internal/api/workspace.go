package api

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/calendar"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/workspace"
)

// Microsoft 365 and Google Workspace: their people and shared contacts kept in the directory, and
// their calendars read for who is away (with the extension calendars, every five minutes).

// WorkspaceStatus is how the link last went, for the settings screen.
type WorkspaceStatus struct {
	Provider string `json:"provider"`
	// Contacts: the last time the directory was brought in step, and what it did.
	Synced  time.Time `json:"synced,omitzero"`
	Total   int       `json:"total"`
	Added   int       `json:"added"`
	Updated int       `json:"updated"`
	Removed int       `json:"removed"`
	Error   string    `json:"error,omitempty"`
	// Calendars: the last read, the extensions matched by email, and why some could not be read.
	CalendarsRead time.Time         `json:"calendarsRead,omitzero"`
	Matched       int               `json:"matched"`
	CalendarError string            `json:"calendarError,omitempty"`
	Problems      map[string]string `json:"problems,omitempty"` // extension -> why
}

type workspaceState struct {
	mu     sync.Mutex
	st     WorkspaceStatus
	src    workspace.Source
	srcFor config.Workspace // the settings src was made from: a new one after a change
}

// workspaceSource is the configured service (nil when none). BITVOICE_GRAPH_LOGIN,
// BITVOICE_GRAPH_URL and BITVOICE_GOOGLE_URL point it at stand-ins in tests.
func workspaceSource(w config.Workspace) workspace.Source {
	switch w.Provider {
	case "microsoft365":
		return &workspace.Microsoft{Tenant: w.TenantID, ClientID: w.ClientID, Secret: w.ClientSecret,
			Login: os.Getenv("BITVOICE_GRAPH_LOGIN"), Graph: os.Getenv("BITVOICE_GRAPH_URL")}
	case "google":
		g := os.Getenv("BITVOICE_GOOGLE_URL")
		src := &workspace.Google{Key: w.ServiceAccountKey, Admin: w.AdminEmail, PeopleURL: g, CalendarURL: g}
		if g != "" {
			src.TokenURL = g + "/token"
		}
		return src
	}
	return nil
}

// source is the service for the saved settings, made again when they changed (its token cached).
func (s *Server) source(cfg config.Config) workspace.Source {
	s.ws.mu.Lock()
	defer s.ws.mu.Unlock()
	w := cfg.System.Workspace
	if s.ws.src == nil || s.ws.srcFor != w {
		s.ws.src, s.ws.srcFor = workspaceSource(w), w
		s.ws.st = WorkspaceStatus{Provider: w.Provider}
	}
	return s.ws.src
}

// contactsFrom turns people into directory contacts: those with a phone number, the organisation's
// people under its name.
func contactsFrom(people []workspace.Person, org string) []store.DirContact {
	var out []store.DirContact
	for _, p := range people {
		if len(p.Numbers) == 0 {
			continue
		}
		c := store.DirContact{Name: oneLine(p.Name, ""), Company: p.Company, Email: p.Email, SourceID: p.ID}
		if c.Company == "" && !p.Shared {
			c.Company = org
		}
		var notes []string
		for _, x := range []string{p.Title, p.Department} {
			if x != "" {
				notes = append(notes, x)
			}
		}
		c.Notes = strings.Join(notes, ", ")
		for _, n := range p.Numbers {
			c.Numbers = append(c.Numbers, store.PhoneNumber{Kind: n.Kind, Number: n.Number})
		}
		if len(c.Name) > 80 {
			c.Name = c.Name[:80]
		}
		out = append(out, c)
	}
	return out
}

// syncContacts brings the directory in step with the service now.
func (s *Server) syncContacts(ctx context.Context) WorkspaceStatus {
	cfg := s.config()
	w := cfg.System.Workspace
	src := s.source(cfg)
	if src == nil || !w.Contacts {
		s.ws.mu.Lock()
		defer s.ws.mu.Unlock()
		return s.ws.st
	}
	people, err := src.People(ctx)
	var a, u, r, total int
	if err == nil {
		cs := contactsFrom(people, cfg.System.Name)
		total = len(cs)
		a, u, r, err = s.Store.SyncContacts(w.Provider, cs)
	}
	s.ws.mu.Lock()
	defer s.ws.mu.Unlock()
	if err != nil {
		s.ws.st.Error = err.Error()
		s.Log.Warn("directory: could not read "+src.Name(), "err", err)
	} else {
		s.ws.st.Error, s.ws.st.Synced, s.ws.st.Total, s.ws.st.Added, s.ws.st.Updated, s.ws.st.Removed = "", time.Now(), total, a, u, r
		if a+u+r > 0 {
			s.Log.Info("directory: in step with "+src.Name(), "added", a, "updated", u, "removed", r)
			if s.Bus != nil {
				s.Bus.Publish("directory.changed", nil)
			}
		}
	}
	return s.ws.st
}

// RunWorkspace keeps the directory in step with Microsoft 365 or Google Workspace every half hour,
// and at once (contacts and calendars) when its settings change.
func (s *Server) RunWorkspace(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	var last config.Workspace
	var synced time.Time
	first := true
	for {
		w := s.config().System.Workspace
		changed := w != last
		if changed || time.Since(synced) >= 30*time.Minute {
			c, cancel := context.WithTimeout(ctx, 5*time.Minute)
			s.syncContacts(c)
			if changed && !first {
				s.readCalendars(c) // at start-up RunCalendars reads them
			}
			cancel()
			last, synced, first = w, time.Now(), false
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// workspaceBusy reads the busy times of the extensions whose person is in the service (by email),
// for readCalendars: extension -> busy. nil when calendars are not read from a service.
func (s *Server) workspaceBusy(ctx context.Context, cfg config.Config, now time.Time) map[string][]calendar.Busy {
	w := cfg.System.Workspace
	src := s.source(cfg)
	if src == nil || !w.Calendars {
		return nil
	}
	byEmail := map[string][]string{}
	var emails []string
	for _, x := range cfg.Extensions {
		e := strings.ToLower(strings.TrimSpace(x.Email))
		if e == "" || !x.Enabled || x.Calendar.Secret != "" {
			continue // a calendar address of its own wins
		}
		if _, ok := byEmail[e]; !ok {
			emails = append(emails, e)
		}
		byEmail[e] = append(byEmail[e], x.Number)
	}
	slices.Sort(emails)
	busy, problems, err := src.Busy(ctx, emails, now.Add(-12*time.Hour), now.Add(24*time.Hour))
	s.ws.mu.Lock()
	defer s.ws.mu.Unlock()
	s.ws.st.CalendarsRead, s.ws.st.Matched, s.ws.st.Problems = now, 0, map[string]string{}
	if err != nil {
		s.ws.st.CalendarError = err.Error()
		return map[string][]calendar.Busy{} // unknown: nobody marked away by a service that could not be read
	}
	s.ws.st.CalendarError = ""
	out := map[string][]calendar.Busy{}
	for e, exts := range byEmail {
		for _, n := range exts {
			if why, bad := problems[e]; bad {
				s.ws.st.Problems[n] = why
				continue
			}
			s.ws.st.Matched++
			out[n] = busy[e]
		}
	}
	return out
}

func (s *Server) workspaceRoutes() {
	s.handle("GET /api/workspace", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg := s.config()
		s.source(cfg)
		s.ws.mu.Lock()
		st := s.ws.st
		s.ws.mu.Unlock()
		writeJSON(w, 200, st)
	})
	// Bring the directory in step now.
	s.handle("POST /api/workspace/sync", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		st := s.syncContacts(ctx)
		s.audit(r, u.Username, "workspace.sync", st.Provider)
		writeJSON(w, 200, st)
	})
	// Try settings before saving them: sign in, count the people, read the matched calendars.
	s.handle("POST /api/workspace/check", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in config.Workspace
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cfg := s.config()
		cur := cfg.System.Workspace
		if in.ClientSecret == "" && in.ClientID == cur.ClientID {
			in.ClientSecret = cur.ClientSecret
		}
		if in.ServiceAccountKey == "" && in.AdminEmail == cur.AdminEmail {
			in.ServiceAccountKey = cur.ServiceAccountKey
		}
		test := cfg
		test.System.Workspace = in
		if p := test.ValidateWorkspace(); len(p) > 0 {
			writeErr(w, p)
			return
		}
		src := workspaceSource(in)
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		people, err := src.People(ctx)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		out := map[string]any{"people": 0, "shared": 0, "withNumbers": len(contactsFrom(people, ""))}
		emails := map[string]bool{}
		for _, x := range people {
			if x.Shared {
				out["shared"] = out["shared"].(int) + 1
			} else {
				out["people"] = out["people"].(int) + 1
			}
			emails[strings.ToLower(x.Email)] = true
		}
		var matched, unmatched []string
		var ask []string
		for _, x := range cfg.Extensions {
			e := strings.ToLower(strings.TrimSpace(x.Email))
			switch {
			case e == "":
			case emails[e]:
				matched = append(matched, x.Number)
				ask = append(ask, e)
			default:
				unmatched = append(unmatched, x.Number)
			}
		}
		out["matched"], out["unmatched"] = append([]string{}, matched...), append([]string{}, unmatched...)
		if len(ask) > 0 {
			now := time.Now()
			busy, problems, err := src.Busy(ctx, ask[:min(len(ask), 50)], now, now.Add(24*time.Hour))
			if err != nil {
				out["calendarError"] = err.Error()
			} else {
				n := 0
				for _, b := range busy {
					n += len(b)
				}
				out["busyTimes"], out["calendarProblems"] = n, problems
			}
		}
		writeJSON(w, 200, out)
	})
}
