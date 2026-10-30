package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// Signing in an AI assistant to BitVoice by OAuth 2.1, as MCP's authorization specifies: assistants
// that cannot be given a token by hand (Claude.ai, ChatGPT connectors) find this server's sign-in
// from /mcp's 401 (RFC 9728 protected resource metadata, RFC 8414 server metadata), register
// themselves (RFC 7591, public clients only), send the person to BitVoice to sign in and allow it,
// and get a token for /mcp alone (authorization code with PKCE S256, refresh tokens rotated).

const (
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour
	oauthCodeTTL    = time.Minute
)

type oauthCode struct {
	client, redirect, challenge, username, role, clientName string
	expires                                                 time.Time
}

type oauthCodes struct {
	mu    sync.Mutex
	codes map[string]oauthCode
}

func (c *oauthCodes) put(code string, v oauthCode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = map[string]oauthCode{}
	}
	for k, x := range c.codes {
		if time.Now().After(x.expires) {
			delete(c.codes, k)
		}
	}
	c.codes[code] = v
}

// take returns a code once: a second use of the same code finds nothing.
func (c *oauthCodes) take(code string) (oauthCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.codes[code]
	delete(c.codes, code)
	return v, ok && time.Now().Before(v.expires)
}

type mcpCtxKey struct{}

// viaMCP marks a request /mcp makes in-process, where a token issued to an assistant is good.
func viaMCP(ctx context.Context) bool { v, _ := ctx.Value(mcpCtxKey{}).(bool); return v }

func (s *Server) oauthRoutes(m *http.ServeMux) {
	cors := func(h http.HandlerFunc) http.HandlerFunc {
		// Assistants running in a browser read these from another origin; no cookie is ever used.
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			cfg, _ := s.Store.Config()
			if !cfg.System.MCP.Enabled {
				writeOAuthError(w, http.StatusNotFound, "temporarily_unavailable", "AI assistants (MCP) are off on this server")
				return
			}
			h(w, r)
		}
	}
	resource := cors(func(w http.ResponseWriter, r *http.Request) {
		base := s.externalBase(r)
		writeJSON(w, 200, map[string]any{
			"resource": base + "/mcp", "authorization_servers": []string{base}, "scopes_supported": []string{"mcp"},
			"bearer_methods_supported": []string{"header"}, "resource_name": "BitVoice",
		})
	})
	m.HandleFunc("/.well-known/oauth-protected-resource", resource)
	m.HandleFunc("/.well-known/oauth-protected-resource/mcp", resource)
	meta := cors(func(w http.ResponseWriter, r *http.Request) {
		base := s.externalBase(r)
		writeJSON(w, 200, map[string]any{
			"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token",
			"registration_endpoint": base + "/oauth/register", "scopes_supported": []string{"mcp"},
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	m.HandleFunc("/.well-known/oauth-authorization-server", meta)
	m.HandleFunc("/.well-known/openid-configuration", meta)
	m.HandleFunc("/oauth/register", cors(s.oauthRegister))
	m.HandleFunc("GET /oauth/authorize", s.oauthAuthorize)
	m.HandleFunc("/oauth/token", cors(s.oauthToken))
	s.mcpInfoRoutes()
	s.handle("GET /api/oauth/request", false, s.oauthRequest)
	s.handle("POST /api/oauth/approve", false, s.oauthApprove)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// goodRedirect says whether an assistant may be sent back to u: https anywhere, or http only to
// this computer (desktop apps listening on a loopback port, RFC 8252).
func goodRedirect(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Fragment != "" || p.Host == "" {
		return false
	}
	switch p.Scheme {
	case "https":
		return true
	case "http":
		h := p.Hostname()
		ip := net.ParseIP(h)
		return h == "localhost" || ip != nil && ip.IsLoopback()
	}
	return false
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST the client's metadata")
		return
	}
	if !s.allowRegistration(r) {
		writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many registrations from this address; try again in an hour")
		return
	}
	var in struct {
		Name       string   `json:"client_name"`
		Redirects  []string `json:"redirect_uris"`
		Grants     []string `json:"grant_types"`
		AuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not client metadata (JSON)")
		return
	}
	if len(in.Redirects) == 0 || len(in.Redirects) > 10 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "give one to ten redirect_uris")
		return
	}
	for _, u := range in.Redirects {
		if !goodRedirect(u) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", u+": https, or http to this computer (localhost) only")
			return
		}
	}
	if in.AuthMethod != "" && in.AuthMethod != "none" {
		// Only public clients: an assistant cannot keep a secret from the person using it, so PKCE
		// protects the code instead.
		in.AuthMethod = "none"
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "AI assistant"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	c, err := s.Store.RegisterOAuthClient(name, in.Redirects)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not record the client")
		return
	}
	s.Log.Info("MCP: an assistant registered to sign in", "client", name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": c.ID, "client_name": c.Name, "redirect_uris": c.RedirectURIs, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"client_id_issued_at": c.Created.Unix(),
	})
}

// allowRegistration keeps one address to 20 registrations an hour.
func (s *Server) allowRegistration(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registrations == nil {
		s.registrations = map[string][]time.Time{}
	}
	keep := s.registrations[ip][:0]
	for _, t := range s.registrations[ip] {
		if time.Since(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 20 {
		s.registrations[ip] = keep
		return false
	}
	s.registrations[ip] = append(keep, time.Now())
	return true
}

// oauthAuthorize checks the request and sends the person to the web interface, which signs them in
// (password, two-step, single sign-on, as always) and asks them.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.Store.Config()
	if !cfg.System.MCP.Enabled {
		http.Error(w, "AI assistants (MCP) are off on this server: an administrator switches them on in Settings.", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	c, ok := s.Store.FindOAuthClient(q.Get("client_id"))
	if !ok || !contains(c.RedirectURIs, q.Get("redirect_uri")) {
		// Never redirect to an address the client did not register: say so here instead.
		http.Error(w, "This sign-in link is not valid: the assistant is unknown to BitVoice, or its return address is not one it registered.", http.StatusBadRequest)
		return
	}
	back := func(code, desc string) {
		u, _ := url.Parse(q.Get("redirect_uri"))
		v := u.Query()
		v.Set("error", code)
		v.Set("error_description", desc)
		if st := q.Get("state"); st != "" {
			v.Set("state", st)
		}
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		back("unsupported_response_type", "only response_type=code")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		back("invalid_request", "PKCE with S256 is required")
		return
	}
	if res := q.Get("resource"); res != "" && !s.ourResource(r, res) {
		back("invalid_target", "this server's resource is "+s.externalBase(r)+"/mcp")
		return
	}
	http.Redirect(w, r, s.cookiePath(r)+"#/connect-assistant?"+q.Encode(), http.StatusFound)
}

func (s *Server) ourResource(r *http.Request, res string) bool {
	res = strings.TrimRight(res, "/")
	base := s.externalBase(r)
	return strings.EqualFold(res, base+"/mcp") || strings.EqualFold(res, base)
}

// oauthRequest describes a sign-in request for the page that asks the person.
func (s *Server) oauthRequest(w http.ResponseWriter, r *http.Request, u store.User) {
	cfg, _ := s.Store.Config()
	c, ok := s.Store.FindOAuthClient(r.URL.Query().Get("client_id"))
	if !ok || !contains(c.RedirectURIs, r.URL.Query().Get("redirect_uri")) {
		writeError(w, http.StatusNotFound, "this sign-in request is not valid: the assistant is unknown, or its return address was not registered")
		return
	}
	ru, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
	writeJSON(w, 200, map[string]any{"client": c.Name, "returnsTo": ru.Host, "role": u.Role, "act": cfg.System.MCP.Act,
		"enabled": cfg.System.MCP.Enabled})
}

// oauthApprove is the person's answer: a code for the assistant, sent back to it.
func (s *Server) oauthApprove(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		ClientID      string `json:"client_id"`
		RedirectURI   string `json:"redirect_uri"`
		State         string `json:"state"`
		CodeChallenge string `json:"code_challenge"`
		Allow         bool   `json:"allow"`
		ReadOnly      bool   `json:"readOnly"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if strings.HasPrefix(u.Username, "token:") {
		writeError(w, http.StatusForbidden, "a person allows an assistant, not a token")
		return
	}
	c, ok := s.Store.FindOAuthClient(in.ClientID)
	if !ok || !contains(c.RedirectURIs, in.RedirectURI) {
		writeError(w, http.StatusNotFound, "this sign-in request is not valid")
		return
	}
	ru, _ := url.Parse(in.RedirectURI)
	v := ru.Query()
	if in.State != "" {
		v.Set("state", in.State)
	}
	if !in.Allow {
		v.Set("error", "access_denied")
		v.Set("error_description", "the person did not allow it")
		ru.RawQuery = v.Encode()
		s.audit(r, u.Username, "mcp.refused", c.Name)
		writeJSON(w, 200, map[string]string{"redirect": ru.String()})
		return
	}
	if in.CodeChallenge == "" {
		writeError(w, http.StatusUnprocessableEntity, "the request has no PKCE code challenge")
		return
	}
	role := u.Role
	if in.ReadOnly || role != "admin" {
		role = "viewer"
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	code := hex.EncodeToString(b)
	s.oauth.put(code, oauthCode{client: c.ID, clientName: c.Name, redirect: in.RedirectURI, challenge: in.CodeChallenge,
		username: u.Username, role: role, expires: time.Now().Add(oauthCodeTTL)})
	v.Set("code", code)
	v.Set("iss", s.externalBase(r))
	ru.RawQuery = v.Encode()
	s.audit(r, u.Username, "mcp.allowed", c.Name+" ("+role+")")
	writeJSON(w, 200, map[string]string{"redirect": ru.String()})
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST the grant")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the body is not a form")
		return
	}
	f := r.PostForm
	client := f.Get("client_id")
	if res := f.Get("resource"); res != "" && !s.ourResource(r, res) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "this server's resource is "+s.externalBase(r)+"/mcp")
		return
	}
	answer := func(t store.Token, access, refresh string) {
		writeJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(oauthAccessTTL.Seconds()),
			"refresh_token": refresh, "scope": "mcp"})
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		c, ok := s.oauth.take(f.Get("code"))
		if !ok || c.client != client || c.redirect != f.Get("redirect_uri") {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the code is unknown, used already, expired, or was for another client")
			return
		}
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the code_verifier does not match the code challenge")
			return
		}
		t, access, refresh, err := s.Store.AddOAuthToken(c.clientName+" (signed in by "+c.username+")", c.role, c.username, c.client, oauthAccessTTL, oauthRefreshTTL)
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not make the token")
			return
		}
		s.audit(r, c.username, "mcp.token", t.Name)
		answer(t, access, refresh)
	case "refresh_token":
		t, access, refresh, err := s.Store.RefreshOAuthToken(client, f.Get("refresh_token"), oauthAccessTTL, oauthRefreshTTL)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is unknown, expired or revoked: sign in again")
			return
		}
		answer(t, access, refresh)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "authorization_code or refresh_token")
	}
}
