package api

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/totp"
)

type meResponse struct {
	Username      string `json:"username"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	SetupComplete bool   `json:"setupComplete"`
	MFA           bool   `json:"mfa"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, ok := s.user(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "sign in first",
			"setupComplete": s.Store.State().SetupComplete})
		return
	}
	full, _ := s.Store.User(u.Username)
	writeJSON(w, 200, meResponse{u.Username, u.Name, u.Role, s.Store.State().SetupComplete, full.TOTP})
}

// throttled reports whether an address has failed to sign in too often recently.
func (s *Server) throttled(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	kept := s.logins[ip][:0]
	for _, t := range s.logins[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.logins[ip] = kept
	return len(kept) >= 10
}

func (s *Server) loginFailed(ip string) {
	s.loginMu.Lock()
	s.logins[ip] = append(s.logins[ip], time.Now())
	s.loginMu.Unlock()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if cfg, _ := s.Store.Config(); cfg.System.SSO.Enabled && !cfg.System.SSO.PasswordSignIn && !clientIP(r).IsLoopback() {
		writeError(w, http.StatusForbidden, "sign in with single sign-on; passwords work only on this computer")
		return
	}
	ip := clientIP(r).String()
	if s.throttled(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed sign-ins from this address; wait ten minutes")
		return
	}
	u, ok := s.Store.Authenticate(req.Username, req.Password)
	if !ok {
		s.loginFailed(ip)
		s.audit(r, req.Username, "login.failed", "")
		writeError(w, http.StatusUnauthorized, "that username and password do not match")
		return
	}
	if full, _ := s.Store.User(u.Username); full.TOTP {
		if strings.TrimSpace(req.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "enter the code from your authenticator app", "mfa": true})
			return
		}
		step, ok := totp.Verify(full.TOTPSecret, req.Code, time.Now())
		if !ok || !s.Store.UseTOTPStep(u.Username, step) {
			s.loginFailed(ip)
			s.audit(r, u.Username, "login.failed", "wrong two-step code")
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "that code is not right; codes change every 30 seconds", "mfa": true})
			return
		}
	}
	s.startSession(w, r, u)
	s.audit(r, u.Username, "login", "")
	full, _ := s.Store.User(u.Username)
	writeJSON(w, 200, meResponse{u.Username, u.Name, u.Role, s.Store.State().SetupComplete, full.TOTP})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, store.SessionKey(c.Value))
		s.saveSessionsLocked()
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- first-run wizard ---

type setupStatus struct {
	Complete  bool     `json:"complete"`
	Allowed   bool     `json:"allowed"` // false: the browser must enter the setup code
	Addresses []string `json:"addresses"`
	SIPPort   int      `json:"sipPort"`
	Version   string   `json:"version"`
	Suggested struct {
		Extensions []string `json:"extensions"`
	} `json:"suggested"`
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	st := setupStatus{Complete: s.Store.State().SetupComplete, Allowed: s.setupAllowed(r), Version: s.Version}
	if st.Allowed && !st.Complete {
		st.Addresses = s.Engine.PhoneAddresses(localAddresses())
		st.SIPPort = s.Engine.SIPPort()
		st.Suggested.Extensions = []string{"100", "101", "102"}
	}
	if st.Addresses == nil {
		st.Addresses = []string{}
	}
	writeJSON(w, 200, st)
}

// localAddresses lists this machine's IPv4 addresses for phones to use.
func localAddresses() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			// IPv4 only: phones are configured with these, and IPv6 privacy addresses change daily.
			if v4 := ipn.IP.To4(); v4 != nil {
				out = append(out, v4.String())
			}
		}
	}
	return out
}

type setupRequest struct {
	Organisation string `json:"organisation"`
	Purpose      string `json:"purpose"`
	Country      string `json:"country"`
	Timezone     string `json:"timezone"`
	Admin        struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	} `json:"admin"`
	PublicAddress string `json:"publicAddress"`
	Extensions    []struct {
		Number   string `json:"number"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Location string `json:"location"`
	} `json:"extensions"`
	Trunk *config.Trunk `json:"trunk"`
	// Inbound is where calls from the trunk go: an extension number, or "all" for a ring group of everyone.
	Inbound string `json:"inbound"`
}

// setupComplete builds the whole first configuration in one revision, creates the administrator and signs
// them in. Nothing is saved unless all of it is valid.
func (s *Server) setupComplete(w http.ResponseWriter, r *http.Request) {
	if s.Store.State().SetupComplete {
		writeError(w, http.StatusConflict, "setup is already complete; sign in instead")
		return
	}
	if !s.setupAllowed(r) {
		writeError(w, http.StatusForbidden, "enter the setup code shown where BitVoice was started")
		return
	}
	var req setupRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if len(req.Extensions) == 0 {
		writeErr(w, config.Problems{{Field: "extensions", Message: "add at least one person"}})
		return
	}
	if err := store.CheckPassword(req.Admin.Password); err != nil {
		writeErr(w, err)
		return
	}
	admin := store.User{Username: req.Admin.Username, Name: req.Admin.Name, Role: "admin"}
	if strings.TrimSpace(admin.Username) == "" {
		writeErr(w, config.Problems{{Field: "username", Message: "choose a username"}})
		return
	}
	_, _, err := s.Store.Update(admin.Username, "First-run setup", 0, func(c *config.Config) error {
		c.System.Name = strings.TrimSpace(req.Organisation)
		if req.Purpose != "" {
			c.System.Purpose = req.Purpose
		}
		if req.Country != "" {
			c.System.Country = strings.ToUpper(req.Country)
			c.System.EmergencyNumbers = config.EmergencyNumbersFor(c.System.Country)
		}
		c.System.Timezone = req.Timezone
		c.System.PublicAddress = strings.TrimSpace(req.PublicAddress)
		c.Extensions = nil
		var numbers []string
		for _, x := range req.Extensions {
			c.Extensions = append(c.Extensions, config.Extension{Number: strings.TrimSpace(x.Number),
				Name: strings.TrimSpace(x.Name), Email: strings.TrimSpace(x.Email), Location: x.Location,
				Enabled: true, Outbound: true, Voicemail: config.Voicemail{Enabled: true},
				NoAnswer: &config.Destination{Type: "voicemail", Target: strings.TrimSpace(x.Number)}})
			numbers = append(numbers, strings.TrimSpace(x.Number))
		}
		// Everyone is told when someone dials 911 until the administrator chooses who.
		c.System.EmergencyNotify = numbers
		if req.Trunk != nil && req.Trunk.Host != "" {
			t := *req.Trunk
			t.ID, t.Enabled = config.NewID(), true
			if t.Name == "" {
				t.Name = "Phone line"
			}
			c.Trunks = []config.Trunk{t}
			c.OutboundRoutes = []config.OutboundRoute{
				{Name: "Emergency", Patterns: slices.Clone(c.System.EmergencyNumbers), Trunks: []string{t.ID}},
				{Name: "Local and national", Patterns: config.NationalPatterns(c.System.Country), Trunks: []string{t.ID}},
			}
			dest := config.Destination{Type: "extension", Target: numbers[0]}
			if req.Inbound == "all" && len(numbers) > 1 {
				c.RingGroups = []config.RingGroup{{Number: nextFree(numbers, 600), Name: "Everyone", Members: numbers}}
				dest = config.Destination{Type: "ringgroup", Target: c.RingGroups[0].Number}
			} else if req.Inbound != "" {
				dest.Target = req.Inbound
			}
			c.InboundRoutes = []config.InboundRoute{{Name: "All calls", Destination: dest}}
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Store.PutUser(admin, req.Admin.Password); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Store.CompleteSetup(); err != nil {
		writeErr(w, err)
		return
	}
	u, _ := s.Store.User(strings.ToLower(admin.Username))
	s.startSession(w, r, u)
	s.audit(r, u.Username, "setup.complete", fmt.Sprintf("%d extensions", len(req.Extensions)))
	writeJSON(w, 200, meResponse{u.Username, u.Name, u.Role, true, false})
}

func nextFree(used []string, from int) string {
	for n := from; ; n++ {
		s := fmt.Sprint(n)
		if !slices.Contains(used, s) {
			return s
		}
	}
}

// Two-step sign-in for the signed-in user.
func (s *Server) mfaRoutes() {
	s.handle("POST /api/auth/mfa/start", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		full, _ := s.Store.User(u.Username)
		if full.TOTP {
			writeError(w, http.StatusConflict, "two-step sign-in is already on")
			return
		}
		secret := totp.NewSecret()
		if err := s.Store.SetTOTP(u.Username, secret, false); err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		issuer := "BitVoice"
		if cfg.System.Name != "" {
			issuer = "BitVoice " + cfg.System.Name
		}
		writeJSON(w, 200, map[string]string{"secret": secret, "uri": totp.URI(issuer, u.Username, secret)})
	})
	s.handle("POST /api/auth/mfa/confirm", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Code string `json:"code"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		full, _ := s.Store.User(u.Username)
		if full.TOTPSecret == "" {
			writeError(w, http.StatusBadRequest, "start setting up two-step sign-in first")
			return
		}
		step, ok := totp.Verify(full.TOTPSecret, in.Code, time.Now())
		if !ok {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Message: "that code is not right", Problems: config.Problems{{Field: "code", Message: "that code is not right; check the phone's clock"}}})
			return
		}
		if err := s.Store.SetTOTP(u.Username, full.TOTPSecret, true); err != nil {
			writeErr(w, err)
			return
		}
		s.Store.UseTOTPStep(u.Username, step)
		s.audit(r, u.Username, "mfa.on", "")
		writeJSON(w, 200, map[string]bool{"mfa": true})
	})
	s.handle("POST /api/auth/mfa/off", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Password string `json:"password"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if _, ok := s.Store.Authenticate(u.Username, in.Password); !ok {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Message: "that password is not right", Problems: config.Problems{{Field: "password", Message: "enter your current password"}}})
			return
		}
		if err := s.Store.SetTOTP(u.Username, "", false); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "mfa.off", "")
		writeJSON(w, 200, map[string]bool{"mfa": false})
	})
}
