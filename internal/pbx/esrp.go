package pbx

import (
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
)

// ESRP (NG911 Core Services): a 911 call sent to the "esrp" destination is routed to the PSAP that
// serves the caller's location, as the ECRF answers, over the trunk that reaches that PSAP; when
// that PSAP does not take the call, to its overflow PSAP; without a location or an answer, to the
// default PSAP. The decision is logged with the call (RouteLogEvent).

// FindService asks the ECRF which PSAP URI serves a location for a service ("" when none does).
// main sets it to this server's own LoST boundaries, or to an outside ECRF.
type FindService func(service string, loc *i3.Location) (uri, why string)

// Route is an ESRP routing decision, for the i3 log.
type Route struct {
	Call    string    `json:"call"`
	Service string    `json:"service"`
	PSAP    string    `json:"psap"`
	Why     string    `json:"why"` // location, default (no location), default (not covered), ...
	At      time.Time `json:"at"`
}

// esrpPlan is where an emergency call goes: the PSAP for its location, then the overflow chain.
func (e *Engine) esrpPlan(cfg *config.Config, c *Call, to string) plan {
	core := cfg.System.Core
	service := "urn:service:sos"
	var loc *i3.Location
	if c.i3 != nil {
		loc = c.i3.Location
		if c.i3.Service != "" {
			service = c.i3.Service
		}
	}
	uri, why := "", ""
	switch {
	case loc == nil:
		why = "default: the call brought no location"
	case e.FindService == nil:
		why = "default: no ECRF is set up"
	default:
		uri, why = e.FindService(service, loc)
		if uri == "" {
			why = "default: " + why
		} else {
			why = "by location"
		}
	}
	if uri == "" {
		uri = core.Default
	}
	// Stored i3 policies (the Policy Store) decide when there are any for this call.
	if outs, which, ok := e.policyOutcomes(cfg, c, service, loc, uri); ok {
		if p, ok := e.policyPlan(cfg, c, service, outs, which, why); ok {
			return p
		}
		why += "; " + which + " gave no route it could use, so the rules below"
	}
	// The policy rules: the first that matches decides where the call goes instead.
	for _, ru := range core.Rules {
		if !e.ruleMatches(cfg, ru, c, service, uri, loc == nil) {
			continue
		}
		if ru.Action == "reject" {
			e.publish("esrp.route", Route{Call: c.ID, Service: service, PSAP: uri, Why: "refused by rule " + ru.Name, At: time.Now()})
			return plan{reject: 486, reason: "Busy Here", direction: "inbound"}
		}
		why += "; rule " + ru.Name + " sent it to " + ru.To
		uri = ru.To
		break
	}
	find := func(u string) *config.CorePSAP {
		for i := range core.PSAPs {
			if strings.EqualFold(core.PSAPs[i].URI, u) {
				return &core.PSAPs[i]
			}
		}
		return nil
	}
	first := find(uri)
	if first == nil {
		if uri != "" && core.Default != "" && uri != core.Default {
			why += "; " + uri + " is not a PSAP listed here, so the default"
			first = find(core.Default)
		}
	}
	if first == nil {
		e.publish("esrp.route", Route{Call: c.ID, Service: service, Why: "no PSAP to send it to: " + why, At: time.Now()})
		return plan{reject: 503, reason: "No PSAP Available", direction: "inbound"}
	}
	// The PSAP and its overflows in turn; one whose own state says it cannot take calls now
	// (NENA queue and service state, when subscribed to) is passed over, unless none is left.
	var chain []*config.CorePSAP
	seen := map[string]bool{}
	for ps := first; ps != nil && !seen[ps.URI]; ps = find(ps.Overflow) {
		seen[ps.URI] = true
		chain = append(chain, ps)
	}
	var able []*config.CorePSAP
	var passed []string
	for _, ps := range chain {
		if why := e.cannotTake(cfg, ps); why != "" {
			passed = append(passed, ps.Name+" ("+why+")")
			continue
		}
		able = append(able, ps)
	}
	switch {
	case len(passed) > 0 && len(able) > 0:
		why += "; passed over " + strings.Join(passed, ", ")
		c.note("The ESRP passed over " + strings.Join(passed, ", ") + ".")
	case len(passed) > 0:
		able = chain // never leave a 911 call with nowhere to go
		why += "; every PSAP says it cannot take calls (" + strings.Join(passed, ", ") + "), so sent as listed"
		c.note("Every PSAP said it could not take calls; the call was sent as listed.")
	}
	if len(able) > 0 {
		first = able[0]
	}
	p := plan{direction: "inbound", toName: first.Name, trunk: first.Name}
	for _, ps := range able {
		if st, ok := e.psapStage(cfg, ps, to, 30*time.Second); ok {
			p.stages = append(p.stages, st)
		}
	}
	if len(p.stages) == 0 {
		p.reject, p.reason = 503, "No PSAP Trunk"
	}
	e.publish("esrp.route", Route{Call: c.ID, Service: service, PSAP: first.URI, Why: why, At: time.Now()})
	return p
}

// ruleMatches says whether every condition of a policy rule holds for a call.
func (e *Engine) ruleMatches(cfg *config.Config, ru config.CoreRule, c *Call, service, psap string, noLocation bool) bool {
	if len(ru.Services) > 0 {
		ok := false
		for _, s := range ru.Services {
			ok = ok || strings.EqualFold(s, service) || strings.HasPrefix(strings.ToLower(service), strings.ToLower(s)+".")
		}
		if !ok {
			return false
		}
	}
	if len(ru.PSAPs) > 0 && !slices.ContainsFunc(ru.PSAPs, func(u string) bool { return strings.EqualFold(u, psap) }) {
		return false
	}
	if ru.Schedule != "" {
		s := cfg.FindSchedule(ru.Schedule)
		if s == nil || s.IsOpen(time.Now().In(cfg.System.Location())) != ru.WhenOpen {
			return false
		}
	}
	if ru.NoLocation && !noLocation {
		return false
	}
	if ru.Suspicious && !c.suspicious {
		return false
	}
	if len(ru.Trunks) > 0 && !slices.Contains(ru.Trunks, c.inTrunkID) {
		return false
	}
	return true
}
