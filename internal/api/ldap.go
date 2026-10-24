package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/ldap"
	"github.com/biodream-llc/bitvoice/internal/ldapsync"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// ldapState is the last directory sync's report.
type ldapState struct {
	mu   sync.Mutex
	last *ldapsync.Report
	wake chan struct{}
}

func (s *Server) ldapSt() *ldapState {
	s.ldapOnce.Do(func() { s.ldap = &ldapState{wake: make(chan struct{}, 1)} })
	return s.ldap
}

// syncLDAP reads the directory and saves what changed as one revision.
func (s *Server) syncLDAP(ctx context.Context, by string) ldapsync.Report {
	cfg := s.config() // vault references resolved, for the reading account's password
	rep := ldapsync.NewReport()
	if !cfg.System.LDAP.Enabled {
		rep.Error = "the company directory is switched off"
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		people, groups, err := ldapsync.Fetch(ctx, cfg.System.LDAP, &rep)
		if err != nil {
			rep.Error = err.Error()
		} else {
			// What was read is applied to the configuration as it is now, as one revision.
			_, _, err := s.Store.Update(by, "Company directory sync", 0, func(c *config.Config) error {
				r := ldapsync.NewReport()
				r.Problems = rep.Problems
				ldapsync.Apply(c, people, groups, &r)
				rep = r
				if !r.Changed {
					return errNothingToSave
				}
				return nil
			})
			if err != nil && !errors.Is(err, errNothingToSave) {
				rep.Error = "saving: " + err.Error()
			}
		}
	}
	st := s.ldapSt()
	st.mu.Lock()
	st.last = &rep
	st.mu.Unlock()
	if rep.Error != "" {
		s.Log.Warn("company directory sync failed", "err", rep.Error)
	} else if rep.Changed {
		s.Log.Info("company directory synced", "created", len(rep.Created), "updated", len(rep.Updated), "disabled", len(rep.Disabled))
	}
	s.Bus.Publish("ldap.synced", rep)
	return rep
}

// errNothingToSave ends an update that changed nothing, so no empty revision is kept.
var errNothingToSave = errors.New("nothing to save")

// RunLDAP syncs the company directory every SyncMinutes, and at once when asked.
func (s *Server) RunLDAP(ctx context.Context) {
	st := s.ldapSt()
	for {
		cfg, _ := s.Store.Config()
		d := cfg.System.LDAP
		wait := time.Duration(max(d.SyncMinutes, 5)) * time.Minute
		if d.Enabled && (d.SyncExtensions || len(d.RingGroups) > 0) {
			s.syncLDAP(ctx, "directory")
		} else {
			wait = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-st.wake:
		}
	}
}

// ldapLogin signs someone in with their directory password; ok false when the directory does not
// know them, refuses the password, or puts them in no allowed group (why says which, for the log).
func (s *Server) ldapLogin(ctx context.Context, login, password string) (u store.User, ok bool, why string) {
	cfg := s.config()
	d := cfg.System.LDAP
	if !d.Enabled || !d.SignIn {
		return store.User{}, false, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	p, role, err := ldapsync.SignIn(ctx, d, login, password)
	switch {
	case errors.Is(err, ldapsync.ErrWrongPassword):
		return store.User{}, false, ""
	case errors.Is(err, ldapsync.ErrNotAllowed):
		return store.User{}, false, "company directory: " + login + " is not in an allowed group"
	case err != nil:
		s.Log.Warn("company directory sign-in could not be checked", "err", err)
		return store.User{}, false, "company directory: " + err.Error()
	}
	link := "ldap " + p.DN
	u, found := s.Store.SSOUser(link)
	if !found {
		u = store.User{Username: s.freeUsername(p.Login), SSO: link}
	}
	u.Name, u.Role = p.Name, role
	if err := s.Store.PutUser(u, ""); err != nil {
		return store.User{}, false, "company directory: the account could not be saved: " + err.Error()
	}
	u, _ = s.Store.User(u.Username)
	s.Store.TouchLogin(u.Username)
	return u, true, ""
}

func (s *Server) ldapRoutes() {
	s.handle("GET /api/ldap/status", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		st := s.ldapSt()
		st.mu.Lock()
		last := st.last
		st.mu.Unlock()
		writeJSON(w, 200, map[string]any{"last": last})
	})
	s.handle("POST /api/ldap/sync", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		rep := s.syncLDAP(r.Context(), u.Username)
		s.audit(r, u.Username, "ldap.sync", rep.Error)
		writeJSON(w, 200, rep)
	})
	// test connects with the settings given (the saved password when the box is empty) and reads a
	// few people, changing nothing.
	s.handle("POST /api/ldap/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in config.LDAP
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		saved := s.config().System.LDAP
		if in.BindPassword == "" && in.BindDN == saved.BindDN {
			in.BindPassword = saved.BindPassword
		}
		probe := config.Default()
		probe.System.LDAP = in
		probe.System.LDAP.Enabled = true
		probe.System.LDAP.RingGroups = nil // checked on save, against the ring groups there
		if err := probe.Validate(); err != nil {
			var pr config.Problems
			if errors.As(err, &pr) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "fix the settings first", "problems": pr})
				return
			}
			writeErr(w, err)
			return
		}
		d := probe.System.LDAP
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		started := time.Now()
		c, err := ldapsync.Dial(ctx, d)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		defer c.Close()
		people, err := ldapsync.People(c, d)
		if err != nil {
			msg := err.Error()
			if ldap.IsCode(err, ldap.NoSuchObject) {
				msg = "there is nothing at " + d.BaseDN + ": check the base DN"
			}
			writeJSON(w, 200, map[string]any{"ok": false, "message": msg})
			return
		}
		withNumber, sample := 0, []ldapsync.Person{}
		for _, p := range people {
			if p.Number != "" {
				withNumber++
				if len(sample) < 8 {
					sample = append(sample, p)
				}
			}
		}
		s.audit(r, u.Username, "ldap.test", d.URL)
		writeJSON(w, 200, map[string]any{"ok": true, "secure": c.Secure(), "people": len(people), "withNumber": withNumber, "sample": sample,
			"millis": time.Since(started).Milliseconds()})
	})
}
