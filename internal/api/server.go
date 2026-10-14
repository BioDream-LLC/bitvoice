// Package api is BitVoice's HTTP API. The web interface uses nothing else, so everything the interface can
// do, a script or another program can do the same way (design rule 8).
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/biodream-llc/bitvoice/internal/acme"
	"github.com/biodream-llc/bitvoice/internal/certs"
	"github.com/biodream-llc/bitvoice/internal/cluster"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

// Server serves the API and the embedded web interface.
type Server struct {
	hotel  hotelLink
	snmp   snmpState
	cards  cardsState
	cdrOut cdrOut
	Store  *store.Store
	Engine *pbx.Engine

	faxOnce        sync.Once
	faxOut         *faxOutbox
	faxMu          sync.Mutex
	faxMailChecked time.Time
	faxMailErr     string
	i3Mu           sync.Mutex
	core           boundaryStore
	lvf            addressStore
	Bus            *events.Bus
	Log            *slog.Logger
	Version        string
	// Web serves the front end; nil serves a plain notice.
	Web http.Handler
	// ARI serves /ari (Asterisk compatibility); AMIAddr reports where AMI listens. Both optional.
	ARI     http.Handler
	AMIAddr func() string
	// ICEServers are the STUN and TURN servers browser phones are given, with fresh credentials, for
	// the host name they reached this server by (set by main; nil for none).
	ICEServers func(host string) []ICEServer
	// TURNStatus says how the TURN server runs (set by main).
	TURNStatus func() map[string]any
	// ACMEStatus and ACMEWake are the Let's Encrypt manager's state and its "look again now" (set by main).
	ACMEStatus func() acme.Status
	ACMEWake   func()
	// DeskStatus says how Skinny phones and MGCP lines stand (set by main).
	DeskStatus func() map[string]map[string]string
	// DataDir, ReleaseKey and Restart make upgrades possible: where versions are kept, the key releases are
	// signed with (empty in development builds), and how to start again.
	DataDir    string
	ReleaseKey string
	Restart    func()
	// SetupCode lets someone on another machine run the first-run wizard.
	SetupCode string
	// Trace holds recent SIP messages for the trace screen.
	Trace *TraceBuffer
	// Tenant is set on the server of a hosted tenant: its own sessions and cookie, no system screens.
	Tenant *store.Tenant
	// Tenancy hosts other organisations on this server (the main server only).
	Tenancy *Tenancy
	// aiToday counts AI requests today, for a tenant's daily allowance.
	aiDay   string
	aiCount int
	aiMu    sync.Mutex
	// HTTPSPort is where the HTTPS listener ended up (0 when there is none), for provisioning addresses.
	HTTPSPort atomic.Int32

	mu       sync.Mutex
	sessions map[string]*session
	started  time.Time

	metrics metrics
	costs   costWatch
	agents  lastAgents
	briefs  briefings
	cal     calState
	// Cluster is this server's place among those sharing its database; nil when it runs alone.
	Cluster *cluster.Cluster
	// DatabaseFixed is true when the database was named at startup (-database or BITVOICE_DATABASE),
	// so Settings cannot move it.
	DatabaseFixed bool
	// Certs holds the TLS certificate in use (nil in tests that need none).
	Certs     *certs.Holder
	rep       repCache
	srv       srvCache
	shared    sharedState
	metricsMu sync.Mutex
	otlpErr   string // the last push to the OpenTelemetry collector that failed
	mux       *http.ServeMux
	loginMu   sync.Mutex
	logins    map[string][]time.Time // failed logins per address, for throttling
	sso       ssoState
	upWait    upgradeWait
	load      loadState
	mobile    mobileState
	docs      apiDocs
	git       gitState
	vaults    vaultState
	ask       askState
	crm       crmState
	arOnce    sync.Once
	ar        *archiver
	gwOnce    sync.Once
	gw        *webphone.Gateway
	gwErr     error
}

type session struct {
	user    store.User
	expires time.Time
}

const (
	cookieName  = "bitvoice_session"
	csrfHeader  = "X-BitVoice"
	sessionLife = 12 * time.Hour
)

// Handler builds the HTTP handler.
func (s *Server) Handler() http.Handler {
	s.sessions = map[string]*session{}
	cfg0, _ := s.Store.Config()
	for k, v := range s.Store.LoadSessions() {
		if u, ok := s.Store.User(v.Username); ok {
			s.sessions[k] = &session{user: u, expires: v.Expires}
		} else if x := cfg0.FindExtension(strings.TrimPrefix(v.Username, "ext:")); strings.HasPrefix(v.Username, "ext:") && x != nil {
			s.sessions[k] = &session{user: portalUser(x), expires: v.Expires}
		}
	}
	s.logins = map[string][]time.Time{}
	s.started = time.Now()
	s.metrics.init()
	if s.Log == nil {
		s.Log = slog.Default()
	}
	m := http.NewServeMux()
	s.mux = m

	// Open to everyone.
	m.HandleFunc("GET /api/openapi.json", s.openAPI)
	for _, open := range []string{"GET /api/health/live", "GET /api/setup/status", "POST /api/setup/complete", "POST /api/auth/login",
		"POST /api/auth/logout", "GET /api/auth/me", "GET /api/auth/options", "GET /api/openapi.json", "POST /api/mobile/pair",
		"POST /api/mobile/token", "POST /api/mobile/unpair", "POST /api/phone"} {
		s.noteRoute(open, false, true)
	}
	m.HandleFunc("GET /api/health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /api/setup/status", s.setupStatus)
	m.HandleFunc("POST /api/setup/complete", s.setupComplete)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.logout)
	m.HandleFunc("GET /api/auth/me", s.me)
	m.HandleFunc("GET /api/auth/options", s.authOptions)
	m.HandleFunc("GET /api/auth/sso/start", s.ssoStart)
	m.HandleFunc("GET /api/auth/sso/callback", s.ssoCallback)
	m.HandleFunc("GET /provision/{file}", s.provisionFile)
	// A trunk's STI certificate, public by nature: verifiers of the calls it signs fetch it here.
	m.HandleFunc("GET /stir/{file}", s.stirCert)

	// Signed in.
	s.routes()
	s.portalRoutes()
	s.tenantRoutes()

	if s.ARI != nil {
		m.Handle("/ari/", s.ARI)
	}
	s.wsRoutes(m)

	// An unknown API path is a JSON 404, never the web page (which a script would fail to parse).
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such API endpoint: "+r.Method+" "+r.URL.Path)
	})
	if s.Web != nil {
		m.Handle("/", s.Web)
	} else {
		m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "BitVoice: the web interface was not built into this binary", http.StatusNotFound)
		})
	}
	return s.middleware(m)
}

// middleware adds security headers and rejects cross-site state changes.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if r.TLS != nil && s.Certs != nil && s.Certs.Installed() {
			// With a certificate from an authority, browsers keep to HTTPS for this name. Never with the
			// self-signed one: HSTS would turn its one-time warning into a page that cannot be opened.
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self'; media-src 'self' blob:; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			// Every state-changing request must carry a header a cross-site form cannot set. With the
			// SameSite=Strict cookie, that is complete protection against cross-site request forgery.
			// A bearer token cannot be sent by a cross-site form, so token requests need no header.
			bearer := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" && !bearer {
				writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handle registers a route that needs a signed-in user; admin routes also need the admin role.
func (s *Server) handle(pattern string, admin bool, f func(w http.ResponseWriter, r *http.Request, u store.User)) {
	s.noteRoute(pattern, admin, false)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.user(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if u.Role == portalRole {
			writeError(w, http.StatusForbidden, "the staff portal reaches only your own settings")
			return
		}
		if s.Tenant != nil {
			if s.Tenant.Suspended {
				writeError(w, http.StatusForbidden, "this organisation's account is suspended; ask your provider")
				return
			}
			if systemOnly(pattern) {
				writeError(w, http.StatusForbidden, "this belongs to the provider hosting your phone system")
				return
			}
		}
		if admin && u.Role != "admin" {
			writeError(w, http.StatusForbidden, "this needs an administrator")
			return
		}
		f(w, r, u)
	})
}

func (s *Server) user(r *http.Request) (store.User, bool) {
	// An API token (Terraform, scripts): Authorization: Bearer bv_...
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		t, ok := s.Store.CheckToken(strings.TrimPrefix(h, "Bearer "))
		if !ok {
			return store.User{}, false
		}
		return store.User{Username: "token:" + t.Name, Name: t.Name, Role: t.Role}, true
	}
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return store.User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := store.SessionKey(c.Value)
	sess := s.sessions[key]
	if sess == nil || time.Now().After(sess.expires) {
		if sess != nil {
			delete(s.sessions, key)
			s.saveSessionsLocked()
		}
		return store.User{}, false
	}
	// Sliding expiry; written out when it has moved on by a good part of the session's life, not on
	// every request.
	next := time.Now().Add(sessionLife)
	if next.Sub(sess.expires) > sessionLife/12 {
		sess.expires = next
		s.saveSessionsLocked()
	}
	return sess.user, true
}

// saveSessionsLocked writes the signed-in browsers to disk so a restart or an upgrade does not sign
// everybody out. s.mu must be held.
func (s *Server) saveSessionsLocked() {
	all := make(map[string]store.SavedSession, len(s.sessions))
	for k, v := range s.sessions {
		all[k] = store.SavedSession{Username: v.user.Username, Expires: v.expires}
	}
	if err := s.Store.SaveSessions(all); err != nil {
		s.Log.Warn("saving sessions", "err", err)
	}
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[store.SessionKey(token)] = &session{user: u, expires: time.Now().Add(sessionLife)}
	s.saveSessionsLocked()
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: token, Path: s.cookiePath(r), HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionLife.Seconds())})
}

// refreshSessionsFor updates the account details held by a user's sessions (a new display name).
func (s *Server) refreshSessionsFor(u store.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.sessions {
		if v.user.Username == u.Username {
			v.user = u
		}
	}
}

// endSessionsFor signs a user out everywhere (after deletion or a role change).
func (s *Server) endSessionsFor(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.sessions {
		if v.user.Username == username {
			delete(s.sessions, k)
		}
	}
	s.saveSessionsLocked()
}

// clientIP is the address of the browser.
func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// setupAllowed reports whether a request may use the first-run wizard: only from this computer, or with
// the one-time code printed when BitVoice started.
func (s *Server) setupAllowed(r *http.Request) bool {
	if clientIP(r).IsLoopback() {
		return true
	}
	code := r.Header.Get("X-Setup-Code")
	return s.SetupCode != "" && code != "" && subtle.ConstantTimeCompare([]byte(code), []byte(s.SetupCode)) == 1
}

func (s *Server) audit(r *http.Request, actor, action, detail string) {
	s.Store.Audit(store.AuditEntry{Actor: actor, Action: action, Detail: detail, From: clientIP(r).String()})
	if s.Bus != nil {
		s.Bus.Publish("audit.added", map[string]string{"action": action})
	}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Message  string           `json:"message"`
	Problems []config.Problem `json:"problems,omitempty"`
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiError{Message: msg})
}

// writeErr turns an error from the store or validation into the right status and body.
func writeErr(w http.ResponseWriter, err error) {
	var p config.Problems
	switch {
	case errors.As(err, &p):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Message: "please fix the highlighted fields", Problems: p})
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "someone else changed the configuration since this page loaded: load the new version and make your change again")
	case errors.Is(err, store.ErrHeld):
		writeError(w, http.StatusConflict, "this is under a legal hold, so it cannot be deleted until the hold is released (System, Retention)")
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

var errNotFound = errors.New("not found")

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("the request body is not valid JSON for this endpoint: " + err.Error())
	}
	return nil
}

func (s *Server) amiAddr() string {
	if s.AMIAddr == nil {
		return ""
	}
	return s.AMIAddr()
}

// AuditSystem records an action taken through another interface (AMI, ARI) with no web request.
func (s *Server) AuditSystem(actor, action, detail string) {
	s.Store.Audit(store.AuditEntry{Actor: actor, Action: action, Detail: detail, From: "compat"})
	if s.Bus != nil {
		s.Bus.Publish("audit.added", map[string]string{"action": action})
	}
}
