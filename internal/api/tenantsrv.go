package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/whisper"
)

// errAILimit is a tenant's AI requests for the day used up.
var errAILimit = errors.New("this organisation's AI requests for today are used up")

// aiClient is the AI client for the settings, with the tenant's daily allowance applied.
func (s *Server) aiClient(a config.AI) *ai.Client {
	var speech *whisper.Manager
	if s.Engine != nil {
		speech = s.Engine.Speech
	}
	return speech.Client(a, s.AIAllow)
}

// AIAllow counts an AI request against a hosted tenant's daily allowance, refusing it when used up.
func (s *Server) AIAllow() error {
	if s.Tenant == nil || s.Tenant.Limits.AIPerDay <= 0 {
		return nil
	}
	day := time.Now().UTC().Format("2006-01-02")
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	if s.aiDay != day {
		s.aiDay, s.aiCount = day, 0
	}
	if s.aiCount >= s.Tenant.Limits.AIPerDay {
		return errAILimit
	}
	s.aiCount++
	return nil
}

// cookieName is the session cookie: each hosted tenant has its own, so signing in to one never signs
// anyone out of another or of the main system.
func (s *Server) cookieName() string {
	if s.Tenant != nil {
		return cookieName + "_" + strings.ReplaceAll(s.Tenant.ID, "-", "_")
	}
	return cookieName
}

// cookiePath limits a tenant's cookie to its own address (/t/<id>/), or / on its own domain.
func (s *Server) cookiePath(r *http.Request) string {
	if p, _ := r.Context().Value(basePathKey{}).(string); p != "" {
		return p + "/"
	}
	return "/"
}

// basePathKey carries the path a tenant's pages are served under (/t/<id>), set by Tenancy.
type basePathKey struct{}

// systemOnly is what a hosted tenant's administrators never reach: the server itself (upgrades, the
// database, the other servers, the certificate), the load generator, the SIP trace (which shows every
// tenant's messages) and the list of tenants.
func systemOnly(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	for _, p := range []string{"/api/system/upgrade", "/api/database", "/api/servers", "/api/certificate", "/api/loadtest", "/api/sip-trace", "/api/tenants"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
