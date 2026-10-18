package api

import (
	"net/http"
	"strconv"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/reason"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// newFindings are the call-plan checker's findings for a proposed configuration that the configuration in
// force does not have: what a change would break.
func (s *Server) newFindings(proposed *config.Config) []pbx.PlanFinding {
	if s.Engine == nil {
		return []pbx.PlanFinding{}
	}
	key := func(f pbx.PlanFinding) string {
		return f.Kind + "\x00" + f.Where.Kind + "\x00" + f.Where.Target + "\x00" + f.Says
	}
	had := map[string]bool{}
	for _, f := range s.Engine.CheckPlan().Findings {
		had[key(f)] = true
	}
	out := []pbx.PlanFinding{}
	for _, f := range s.Engine.CheckPlanOf(proposed).Findings {
		if !had[key(f)] && f.Severity != "info" {
			out = append(out, f)
		}
	}
	return out
}

func (s *Server) planCheckRoutes() {
	// Every situation the call plan can be in, tried: calls that end nowhere, loops, and the rest.
	s.handle("GET /api/plan-check", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, s.Engine.CheckPlan())
	})
	// What restoring a revision would break that works now.
	s.handle("GET /api/history/{rev}/check", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		rev, _ := strconv.Atoi(r.PathValue("rev"))
		old, err := s.Store.Revision(rev)
		if err != nil {
			writeError(w, 404, "no revision "+r.PathValue("rev"))
			return
		}
		cfg := old.Config
		if err := cfg.Validate(); err != nil {
			writeJSON(w, 200, map[string]any{"items": []pbx.PlanFinding{}})
			return
		}
		writeJSON(w, 200, map[string]any{"items": s.newFindings(&cfg)})
	})
	// The calls out of the country the toll-fraud guard judged lately, with its reasons.
	s.handle("GET /api/security/fraud", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.FraudDecisions()})
	})
	// The judges' rulebooks, each with what checking it in every combination of its facts found.
	s.handle("GET /api/rulebooks", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		type book struct {
			*reason.Book
			Check reason.Report `json:"check"`
		}
		out := []book{}
		for _, b := range reason.Books {
			out = append(out, book{b, b.Check()})
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
}
