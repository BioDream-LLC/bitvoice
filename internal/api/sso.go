package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/oidc"
	"github.com/biodream-llc/bitvoice/internal/store"
)

const ssoCookie = "bitvoice_sso"

// ssoState holds sign-ins in progress and the discovered provider.
type ssoState struct {
	mu       sync.Mutex
	flows    map[string]ssoFlow
	provider *oidc.Provider
	issuer   string
	found    time.Time
}

type ssoFlow struct {
	oidc.Flow
	redirect string
}

// provider discovers the identity provider's endpoints, keeping them for an hour.
func (s *Server) ssoProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	st := &s.sso
	st.mu.Lock()
	if st.provider != nil && st.issuer == issuer && time.Since(st.found) < time.Hour {
		p := st.provider
		st.mu.Unlock()
		return p, nil
	}
	st.mu.Unlock()
	p, err := oidc.Discover(ctx, nil, issuer)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.provider, st.issuer, st.found = p, issuer, time.Now()
	st.mu.Unlock()
	return p, nil
}

// ssoRedirect is the address the provider sends the browser back to.
func ssoRedirect(cfg config.SSO, r *http.Request) string {
	if cfg.RedirectURL != "" {
		return cfg.RedirectURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/auth/sso/callback"
}

// authOptions tells the sign-in page which ways of signing in are offered.
func (s *Server) authOptions(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.Store.Config()
	sso := cfg.System.SSO
	writeJSON(w, 200, map[string]any{
		"password": !sso.Enabled || sso.PasswordSignIn || clientIP(r).IsLoopback(),
		"sso":      map[string]any{"enabled": sso.Enabled, "label": sso.Label},
	})
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.Store.Config()
	sso := cfg.System.SSO
	if !sso.Enabled {
		ssoFail(w, r, "single sign-on is switched off")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	p, err := s.ssoProvider(ctx, sso.Issuer)
	if err != nil {
		s.Log.Warn("SSO provider unreachable", "err", err)
		ssoFail(w, r, "the sign-in service could not be reached: "+err.Error())
		return
	}
	redirect := ssoRedirect(sso, r)
	flow, to := p.Start(sso.ClientID, redirect, []string{"openid", "profile", "email"})
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	st := &s.sso
	st.mu.Lock()
	if st.flows == nil {
		st.flows = map[string]ssoFlow{}
	}
	for k, f := range st.flows {
		if time.Since(f.Started) > 10*time.Minute {
			delete(st.flows, k)
		}
	}
	if len(st.flows) >= 1000 {
		st.mu.Unlock()
		ssoFail(w, r, "too many sign-ins in progress; try again in a few minutes")
		return
	}
	st.flows[id] = ssoFlow{flow, redirect}
	st.mu.Unlock()
	// Lax, not Strict: the provider's redirect back is a cross-site navigation that must carry it.
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: id, Path: "/api/auth/sso", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 600})
	http.Redirect(w, r, to, http.StatusFound)
}

// ssoFail sends the browser back to the sign-in page with a message it shows.
func ssoFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(msg)+"#/", http.StatusFound)
}

var nameClean = regexp.MustCompile(`[^a-z0-9._-]+`)

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := r.Cookie(ssoCookie)
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/auth/sso", MaxAge: -1, HttpOnly: true})
	if e := q.Get("error"); e != "" {
		ssoFail(w, r, "the sign-in service said: "+e+" "+q.Get("error_description"))
		return
	}
	if err != nil {
		ssoFail(w, r, "the sign-in took too long or started in another browser; try again")
		return
	}
	st := &s.sso
	st.mu.Lock()
	flow, ok := st.flows[c.Value]
	delete(st.flows, c.Value)
	st.mu.Unlock()
	if !ok || time.Since(flow.Started) > 10*time.Minute || q.Get("state") != flow.State || q.Get("code") == "" {
		ssoFail(w, r, "the sign-in took too long or started in another browser; try again")
		return
	}
	cfg := s.config() // vault references resolved
	sso := cfg.System.SSO
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	p, err := s.ssoProvider(ctx, sso.Issuer)
	if err != nil {
		ssoFail(w, r, "the sign-in service could not be reached")
		return
	}
	claims, err := p.Exchange(ctx, flow.Flow, q.Get("code"), sso.ClientID, sso.ClientSecret, flow.redirect)
	if err != nil {
		s.Log.Warn("SSO sign-in refused", "err", err)
		s.audit(r, "", "login.failed", "single sign-on: "+err.Error())
		ssoFail(w, r, err.Error())
		return
	}
	who := claims.String("email")
	if who == "" {
		who = claims.String("preferred_username")
	}
	groups := claims.Strings(sso.GroupsClaim)
	role := ""
	switch {
	case overlaps(groups, sso.AdminGroups):
		role = "admin"
	case len(sso.ViewerGroups) == 0 || overlaps(groups, sso.ViewerGroups):
		role = "viewer"
	}
	if role == "" {
		s.audit(r, who, "login.failed", "single sign-on: not in an allowed group")
		ssoFail(w, r, who+" is not in a group allowed to use BitVoice; ask an administrator")
		return
	}
	link := p.Issuer + " " + claims.String("sub")
	name := claims.String("name")
	if name == "" {
		name = who
	}
	u, found := s.Store.SSOUser(link)
	if !found {
		u = store.User{Username: s.freeUsername(who), SSO: link}
	}
	u.Name, u.Role = name, role
	if err := s.Store.PutUser(u, ""); err != nil {
		ssoFail(w, r, "the account could not be saved: "+err.Error())
		return
	}
	u, _ = s.Store.User(u.Username)
	s.Store.TouchLogin(u.Username)
	s.endSessionsFor(u.Username) // a role from the provider replaces whatever the old sessions had
	s.startSession(w, r, u)
	s.audit(r, u.Username, "login", "single sign-on as "+role)
	http.Redirect(w, r, "/#/", http.StatusFound)
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.ContainsFunc(b, func(y string) bool { return strings.EqualFold(strings.TrimSpace(y), x) }) {
			return true
		}
	}
	return false
}

// freeUsername makes a BitVoice username from an email or login name that no account has yet. It never
// reuses an existing account: linking a provider identity to a password account would let whoever
// controls the identity take it over.
func (s *Server) freeUsername(who string) string {
	base := strings.ToLower(who)
	if i := strings.IndexByte(base, '@'); i > 0 {
		base = base[:i]
	}
	base = strings.Trim(nameClean.ReplaceAllString(base, "."), "._-")
	if base == "" {
		base = "sso"
	}
	if len(base) > 50 {
		base = base[:50]
	}
	name := base
	for n := 2; ; n++ {
		if _, taken := s.Store.User(name); !taken {
			return name
		}
		name = base + "-" + strconv.Itoa(n)
	}
}
