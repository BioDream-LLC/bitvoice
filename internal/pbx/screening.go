package pbx

import (
	"crypto/x509"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/screen"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// applyScreening keeps a certificate checker for the STI roots configured (a new one when they change).
func (e *Engine) applyScreening(cfg config.Config) {
	roots := cfg.System.Screening.STIRoots
	if e.checker.Load() != nil && roots == e.checkerRoots {
		return
	}
	k := &screen.Checker{}
	if roots != "" {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM([]byte(roots)) {
			k.Roots = pool
		}
	}
	e.checkerRoots = roots
	e.checker.Store(k)
}

func labelOf(cfg *config.Config) string {
	if l := cfg.System.Screening.Label; l != "" {
		return l
	}
	return "Spam?"
}

func anonymous(n string) bool {
	switch strings.ToLower(n) {
	case "", "anonymous", "restricted", "unknown", "unavailable", "private", "withheld":
		return true
	}
	return false
}

// screenCall judges an incoming call from a trunk: it records the caller ID's verification, labels
// the call, or returns why it is refused. Within an hour of an emergency call nothing is refused
// (the call may be the emergency service calling back), and allowed numbers are never touched.
func (e *Engine) screenCall(cfg *config.Config, c *Call, m *sip.Message, trunk *config.Trunk, did string) string {
	sc := cfg.System.Screening
	var v screen.Verdict
	if id := m.Get("Identity"); id != "" && e.checker.Load() != nil {
		v = e.checker.Load().Check(id, c.from, did)
	} else {
		v = screen.FromHeaders(m.Get("From"), m.Get("P-Asserted-Identity"), m.Get("P-Attestation-Indicator"))
	}
	if v.Attest != "" || v.Verified != "" {
		c.screen = &v
	}
	for _, a := range sc.Allowed {
		if a.Matches(c.from) {
			return ""
		}
	}
	callBack := time.Since(time.Unix(e.lastEmergency.Load(), 0)) < time.Hour
	act, why := "", ""
	switch {
	case anonymous(c.from):
		act, why = sc.Anonymous, "the number is withheld"
	case blockedBy(sc.Blocked, c.from) != "":
		act, why = "block", "the number is on the block list"
	case v.Verified == "failed":
		act, why = sc.Failed, v.Why
	case v.Verified == "" && (v.Attest == "" || v.Attest == "C") || v.Attest == "C":
		act, why = sc.Unverified, "the caller ID is not verified"
	}
	if act == "block" && callBack {
		act = "label" // never refuse what may be the emergency service calling back
	}
	switch act {
	case "block":
		e.Log.Info("incoming call refused by screening", "from", c.from, "trunk", trunk.Name, "why", why)
		if e.Recorder != nil {
			now := time.Now().UTC()
			r := store.CDR{ID: c.ID, Start: now, End: now, Direction: "inbound", From: c.from, FromName: c.fromName, To: did,
				Trunk: trunk.Name, Disposition: "blocked", Cause: why, Screened: "blocked"}
			if c.screen != nil {
				r.Attest, r.Verified = c.screen.Attest, c.screen.Verified
			}
			if err := e.Recorder.AddCDR(r); err == nil {
				e.publish("call.logged", r)
			}
		}
		e.publish("call.blocked", map[string]string{"from": c.from, "why": why})
		return "Declined"
	case "label":
		c.screened = "labelled"
	}
	return ""
}

func blockedBy(list []config.ScreenNumber, n string) string {
	for _, b := range list {
		if b.Matches(n) {
			return b.Number
		}
	}
	return ""
}
