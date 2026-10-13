package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Tenancy hosts other organisations on this BitVoice. Each tenant is a phone system of its own: its own
// data folder (or PostgreSQL schema), configuration, engine and web interface, sharing only this
// server's SIP port and media ports. A tenant's administrators sign in at /t/<id>/ (or at its own
// domain) and see nothing of the others; the main system's administrators manage them all.
type Tenancy struct {
	Root    *Server
	Switch  *pbx.Switch
	DataDir string
	// DBSpec is the main database: empty or SQLite gives each tenant a SQLite file in its folder;
	// PostgreSQL gives each tenant a schema of its own in the same database.
	DBSpec  string
	Log     *slog.Logger
	Version string
	Web     http.Handler

	ctx  context.Context
	mu   sync.Mutex
	live map[string]*hosted
}

type hosted struct {
	t      store.Tenant
	st     *store.Store
	eng    *pbx.Engine
	srv    *Server
	h      http.Handler
	cancel context.CancelFunc
}

// Start opens every tenant in the list.
func (ty *Tenancy) Start(ctx context.Context) {
	ty.ctx = ctx
	ty.live = map[string]*hosted{}
	for _, t := range ty.Root.Store.Tenants() {
		if err := ty.open(t); err != nil {
			ty.Log.Error("could not start a tenant", "tenant", t.ID, "err", err)
		}
	}
}

// Stop closes every tenant.
func (ty *Tenancy) Stop() {
	ty.mu.Lock()
	all := make([]*hosted, 0, len(ty.live))
	for _, h := range ty.live {
		all = append(all, h)
	}
	ty.live = map[string]*hosted{}
	ty.mu.Unlock()
	for _, h := range all {
		ty.close(h)
	}
}

func (ty *Tenancy) spec(id string) (string, error) {
	if !strings.HasPrefix(ty.DBSpec, "postgres://") && !strings.HasPrefix(ty.DBSpec, "postgresql://") {
		return "", nil // SQLite in the tenant's folder
	}
	schema := "tenant_" + strings.ReplaceAll(id, "-", "_")
	if _, err := ty.Root.Store.DB().Write.ExecContext(ty.ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		return "", fmt.Errorf("creating the tenant's schema: %w", err)
	}
	u, err := url.Parse(ty.DBSpec)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// open starts a tenant: its store, engine (on the shared SIP port) and web interface.
func (ty *Tenancy) open(t store.Tenant) error {
	dir := store.TenantDir(ty.DataDir, t.ID)
	spec, err := ty.spec(t.ID)
	if err != nil {
		return err
	}
	st, err := store.OpenWith(dir, spec)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ty.ctx)
	log := ty.Log.With("tenant", t.ID)
	cfg, _ := st.Config()
	// The tenant's media and SIP ports are the main system's.
	main := ty.Root.Store
	mcfg, _ := main.Config()
	cfg.System.SIPPort, cfg.System.RTPMin, cfg.System.RTPMax = mcfg.System.SIPPort, mcfg.System.RTPMin, mcfg.System.RTPMax
	bus := events.New()
	eng := pbx.New(cfg, bus, st, log)
	tt := t
	srv := &Server{Store: st, Engine: eng, Bus: bus, Log: log, Version: ty.Version, DataDir: dir, Tenant: &tt, Web: ty.Web,
		SetupCode: ty.Root.SetupCode, Certs: ty.Root.Certs}
	h := &hosted{t: t, st: st, eng: eng, srv: srv, cancel: cancel}
	ty.applyLimits(h)
	srv.WireEngine()
	eng.StartShared(ctx, ty.Switch.Default)
	ty.Switch.Add(t.ID, t.Domain, eng)
	h.h = srv.Handler()
	srv.RunBackground(ctx, false)
	ty.mu.Lock()
	ty.live[t.ID] = h
	ty.mu.Unlock()
	log.Info("tenant started", "name", t.Name)
	return nil
}

// applyLimits puts a tenant's allowances in force.
func (ty *Tenancy) applyLimits(h *hosted) {
	l := h.t.Limits
	h.st.SetLimit(func(c *config.Config) config.Problems {
		if l.Extensions > 0 && len(c.Extensions) > l.Extensions {
			return config.Problems{{Field: "extensions", Message: fmt.Sprintf("this organisation may have %d extensions; ask your provider for more", l.Extensions)}}
		}
		return nil
	})
	h.st.SetQuota(int64(l.StorageMB) << 20)
	h.eng.MaxCalls.Store(int32(l.Calls))
	h.eng.Suspended.Store(h.t.Suspended)
	tt := h.t
	h.srv.Tenant = &tt
}

func (ty *Tenancy) close(h *hosted) {
	ty.Switch.Remove(h.t.ID)
	h.cancel()
	h.eng.Stop()
	h.st.Close()
}

func (ty *Tenancy) get(id string) *hosted {
	ty.mu.Lock()
	defer ty.mu.Unlock()
	return ty.live[id]
}

// Wrap sends a tenant's requests (its own domain, or /t/<id>/) to its web interface, and the rest to next.
func (ty *Tenancy) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ty.mu.Lock()
		var by *hosted
		for _, h := range ty.live {
			if h.t.Domain != "" && h.t.Domain == host {
				by = h
			}
		}
		ty.mu.Unlock()
		if by != nil {
			by.h.ServeHTTP(w, r)
			return
		}
		if rest, ok := strings.CutPrefix(r.URL.Path, "/t/"); ok {
			id, _, _ := strings.Cut(rest, "/")
			h := ty.get(id)
			if h == nil {
				writeError(w, http.StatusNotFound, "no organisation "+id+" on this server")
				return
			}
			if r.URL.Path == "/t/"+id {
				http.Redirect(w, r, "/t/"+id+"/", http.StatusFound)
				return
			}
			base := "/t/" + id
			r2 := r.Clone(context.WithValue(r.Context(), basePathKey{}, base))
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, base)
			r2.URL.RawPath = ""
			h.h.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TenantView is a tenant with what it uses now.
type TenantView struct {
	store.Tenant
	Running      bool   `json:"running"`
	Extensions   int    `json:"extensions"`
	Phones       int    `json:"phones"`
	Calls        int    `json:"calls"`
	StorageBytes int64  `json:"storageBytes"`
	URL          string `json:"url"`
	SIPUser      string `json:"sipUser"` // how phones without the domain sign in: acme.100
	Admins       int    `json:"admins"`
}

func (ty *Tenancy) view(t store.Tenant) TenantView {
	v := TenantView{Tenant: t, URL: "/t/" + t.ID + "/", SIPUser: t.ID + ".<extension>"}
	if h := ty.get(t.ID); h != nil {
		cfg, _ := h.st.Config()
		v.Running = true
		v.Extensions = len(cfg.Extensions)
		v.Phones = len(h.eng.Registrations())
		v.Calls = len(h.eng.Calls())
		v.StorageBytes = h.st.StorageUsed()
		v.Admins = len(h.st.Users())
	}
	return v
}

// tenantRoutes are the main system's tenant management.
func (s *Server) tenantRoutes() {
	if s.Tenancy == nil || s.Tenant != nil {
		return
	}
	ty := s.Tenancy
	s.handle("GET /api/tenants", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []TenantView{}
		for _, t := range s.Store.Tenants() {
			out = append(out, ty.view(t))
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("POST /api/tenants", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			store.Tenant
			Admin struct {
				Username string `json:"username"`
				Name     string `json:"name"`
				Password string `json:"password"`
			} `json:"admin"`
			Country string `json:"country"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		p := store.CheckTenant(&in.Tenant)
		if strings.TrimSpace(in.Admin.Username) == "" {
			p = append(p, config.Problem{Field: "admin.username", Message: "name the organisation's first administrator"})
		}
		if len(in.Admin.Password) < 12 {
			p = append(p, config.Problem{Field: "admin.password", Message: "at least 12 characters"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		if _, err := os.Stat(store.TenantDir(ty.DataDir, in.ID)); err == nil {
			writeErr(w, config.Problems{{Field: "id", Message: "a folder for " + in.ID + " is still there from before; choose another short name"}})
			return
		}
		t, err := s.Store.AddTenant(in.Tenant)
		if errors.Is(err, store.ErrTenantExists) {
			writeErr(w, config.Problems{{Field: "id", Message: "that short name or domain is already used"}})
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := ty.create(t, in.Admin.Username, in.Admin.Name, in.Admin.Password, in.Country); err != nil {
			_ = s.Store.DeleteTenant(t.ID)
			_ = os.RemoveAll(store.TenantDir(ty.DataDir, t.ID))
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "tenant.create", t.ID+" "+t.Name)
		writeJSON(w, http.StatusCreated, ty.view(t))
	})
	s.handle("PUT /api/tenants/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in store.Tenant
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.ID = r.PathValue("id")
		if !slices.ContainsFunc(s.Store.Tenants(), func(t store.Tenant) bool { return t.ID == in.ID }) {
			writeError(w, 404, "no tenant "+in.ID)
			return
		}
		t, err := s.Store.PutTenant(in)
		if errors.Is(err, store.ErrTenantExists) {
			writeErr(w, config.Problems{{Field: "domain", Message: "another organisation uses that domain"}})
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		if h := ty.get(t.ID); h != nil {
			h.t = t
			ty.applyLimits(h)
			ty.Switch.Add(t.ID, t.Domain, h.eng)
		}
		s.audit(r, u.Username, "tenant.update", t.ID)
		writeJSON(w, 200, ty.view(t))
	})
	// Removing a tenant stops it and moves its folder aside (tenants/.removed-<id>-<time>), so a
	// mistake can be undone by hand; nothing is deleted.
	s.handle("DELETE /api/tenants/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		if err := s.Store.DeleteTenant(id); err != nil {
			writeError(w, 404, "no tenant "+id)
			return
		}
		ty.mu.Lock()
		h := ty.live[id]
		delete(ty.live, id)
		ty.mu.Unlock()
		if h != nil {
			ty.close(h)
		}
		dir := store.TenantDir(ty.DataDir, id)
		aside := filepath.Join(filepath.Dir(dir), ".removed-"+id+"-"+time.Now().UTC().Format("20060102T150405"))
		if err := os.Rename(dir, aside); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.Log.Error("could not move a removed tenant's folder aside", "tenant", id, "err", err)
		}
		s.audit(r, u.Username, "tenant.delete", id)
		w.WriteHeader(204)
	})
	// Open a tenant's web interface as a main-system administrator, without the tenant's own account.
	s.handle("POST /api/tenants/{id}/open", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		h := ty.get(r.PathValue("id"))
		if h == nil {
			writeError(w, 404, "no tenant "+r.PathValue("id"))
			return
		}
		base := "/t/" + h.t.ID
		r2 := r.Clone(context.WithValue(r.Context(), basePathKey{}, base))
		h.srv.startSession(w, r2, store.User{Username: "provider:" + u.Username, Name: u.Name + " (provider)", Role: "admin"})
		h.srv.audit(r, "provider:"+u.Username, "provider.open", "")
		writeJSON(w, 200, map[string]string{"url": base + "/"})
	})
}

// create sets up a new tenant's folder: its first administrator and a finished setup, then starts it.
func (ty *Tenancy) create(t store.Tenant, username, name, password, country string) error {
	dir := store.TenantDir(ty.DataDir, t.ID)
	spec, err := ty.spec(t.ID)
	if err != nil {
		return err
	}
	st, err := store.OpenWith(dir, spec)
	if err != nil {
		return err
	}
	if err := st.PutUser(store.User{Username: username, Name: name, Role: "admin"}, password); err != nil {
		st.Close()
		return err
	}
	if _, _, err := st.Update("provider", "New organisation "+t.Name, 0, func(c *config.Config) error {
		c.System.Name = t.Name
		if country != "" {
			c.System.Country = strings.ToLower(country)
		}
		return nil
	}); err != nil {
		st.Close()
		return err
	}
	if err := st.CompleteSetup(); err != nil {
		st.Close()
		return err
	}
	st.Close()
	return ty.open(t)
}
