package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Configuration as code: API tokens for Terraform and scripts, and a configuration kept in Git.

type gitState struct {
	mu      sync.Mutex
	Last    time.Time       `json:"last,omitzero"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Changes []config.Change `json:"changes"`
	Applied int             `json:"appliedRev,omitempty"`
	next    time.Time
}

func (g *gitState) snapshot() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch := g.Changes
	if ch == nil {
		ch = []config.Change{}
	}
	return map[string]any{"last": g.Last, "ok": g.OK, "error": g.Error, "changes": ch, "appliedRev": g.Applied}
}

// syncGit reads the file and reports, or applies, what it would change.
func (s *Server) syncGit(ctx context.Context) {
	cfg := s.config() // vault references resolved
	g := cfg.System.GitOps
	if g.URL == "" {
		return
	}
	set := func(ok bool, err string, changes []config.Change, rev int) {
		s.git.mu.Lock()
		s.git.Last, s.git.OK, s.git.Error, s.git.Changes = time.Now().UTC(), ok, err, changes
		if rev > 0 {
			s.git.Applied = rev
		}
		s.git.next = time.Now().Add(time.Duration(g.EveryMinutes) * time.Minute)
		s.git.mu.Unlock()
		if s.Bus != nil {
			s.Bus.Publish("gitops.synced", map[string]any{"ok": ok})
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		set(false, err.Error(), nil, 0)
		return
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		set(false, "could not read the file: "+err.Error(), nil, 0)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		set(false, fmt.Sprintf("the repository answered %s", resp.Status), nil, 0)
		return
	}
	var in config.Config
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		set(false, "the file is not a BitVoice configuration: "+err.Error(), nil, 0)
		return
	}
	cur, _ := s.Store.Config()
	config.KeepSecrets(&in, cur)
	in.System.GitOps = cur.System.GitOps // the file never changes where it is read from
	check := in.Clone()
	if err := check.Validate(); err != nil {
		var p config.Problems
		msg := err.Error()
		if errors.As(err, &p) && len(p) > 0 {
			msg = fmt.Sprintf("the file has %d problem(s), first: %s: %s", len(p), p[0].Field, p[0].Message)
		}
		set(false, msg, nil, 0)
		return
	}
	changes := config.Diff(cur, check, 300)
	if len(changes) == 0 || !g.Apply {
		set(true, "", changes, 0)
		return
	}
	_, rev, err := s.Store.Update("git", "Configuration from Git", 0, func(c *config.Config) error {
		in.System.GitOps = c.System.GitOps
		*c = in
		return nil
	})
	if err != nil {
		set(false, err.Error(), changes, 0)
		return
	}
	s.Store.Audit(store.AuditEntry{Actor: "git", Action: "config.git", Detail: fmt.Sprintf("%d change(s) from %s", len(changes), g.URL)})
	set(true, "", changes, rev)
}

// RunGitOps checks the repository on its schedule.
func (s *Server) RunGitOps(ctx context.Context) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.git.mu.Lock()
		due := time.Now().After(s.git.next)
		s.git.mu.Unlock()
		if due {
			s.syncGit(ctx)
		}
	}
}

func (s *Server) automationRoutes() {
	notToken := func(w http.ResponseWriter, u store.User) bool {
		if strings.HasPrefix(u.Username, "token:") {
			writeError(w, http.StatusForbidden, "API tokens cannot manage API tokens; sign in instead")
			return false
		}
		return true
	}
	s.handle("GET /api/tokens", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Store.Tokens()})
	})
	s.handle("POST /api/tokens", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !notToken(w, u) {
			return
		}
		var in struct {
			Name string `json:"name"`
			Role string `json:"role"`
			Days int    `json:"days"`
			// OnBehalf: the program names the person it acts for (X-BitVoice-On-Behalf-Of).
			OnBehalf bool `json:"onBehalf"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		var p config.Problems
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 60 {
			p = append(p, config.Problem{Field: "name", Message: "name the token after what uses it, such as Terraform"})
		}
		if in.Role != "admin" && in.Role != "viewer" {
			p = append(p, config.Problem{Field: "role", Message: "choose administrator or viewer"})
		}
		if in.Days < 0 || in.Days > 3650 {
			p = append(p, config.Problem{Field: "days", Message: "0 for no expiry, or up to 3650 days"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		var exp time.Time
		if in.Days > 0 {
			exp = time.Now().AddDate(0, 0, in.Days).UTC()
		}
		t, secret, err := s.Store.AddToken(in.Name, in.Role, u.Username, exp, in.OnBehalf)
		if err != nil {
			writeErr(w, err)
			return
		}
		detail := t.Name + " (" + t.Role + ")"
		if t.OnBehalf {
			detail += ", names the person it acts for"
		}
		s.audit(r, u.Username, "token.add", detail)
		writeJSON(w, 200, map[string]any{"token": t, "secret": secret})
	})
	s.handle("DELETE /api/tokens/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !notToken(w, u) {
			return
		}
		if err := s.Store.DeleteToken(r.PathValue("id")); err != nil {
			writeError(w, http.StatusNotFound, "no such token")
			return
		}
		s.audit(r, u.Username, "token.revoke", r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("GET /api/gitops", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, s.git.snapshot())
	})
	s.handle("POST /api/gitops/sync", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.syncGit(r.Context())
		writeJSON(w, 200, s.git.snapshot())
	})
}
