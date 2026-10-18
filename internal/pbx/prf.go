package pbx

import (
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The ESRP's policy routing function with stored i3 policies (NENA-STA-010.3 section 3.3.3): the
// INVITE's facts are kept with the call when it arrives, and the stored rulesets are run on them
// when the call is routed. Without a stored policy the Core services page's rules decide, as before.

type policyFacts struct {
	headers    map[string][]string
	requestURI string
	source     string
	bodyTypes  []string
	additional map[string]map[string]string
	sdp        i3.SDPFacts
}

// Dequeuer is a position or PSAP registered to take a queue's calls.
type Dequeuer struct {
	URI        string
	Preference int // 1 to 5, as registered
}

var firstInt = regexp.MustCompile(`-?\d+`)

// readPolicyFacts takes what a routing policy may test from an ESInet INVITE.
func readPolicyFacts(m *sip.Message, offer *sdp.Session) *policyFacts {
	f := &policyFacts{headers: map[string][]string{}, requestURI: m.RequestURI, additional: map[string]map[string]string{}}
	for _, h := range m.Headers {
		k := strings.ToLower(h.Name)
		f.headers[k] = append(f.headers[k], h.Value)
	}
	// The call's source: the network that sent it into the ESInet, the bottom Via's host (the first
	// hop), else the caller's own domain.
	if vias := m.GetAll("Via"); len(vias) > 0 {
		parts := strings.Split(vias[len(vias)-1], ",")
		if fs := strings.Fields(parts[len(parts)-1]); len(fs) >= 2 {
			host, _, _ := strings.Cut(fs[1], ";")
			if h, _, ok := strings.Cut(host, ":"); ok && !strings.Contains(host, "[") {
				host = h
			}
			f.source = host
		}
	}
	if from, err := sip.ParseAddress(m.Get("From")); err == nil && (f.source == "" || isIP(f.source)) && from.URI.Host != "" {
		f.source = from.URI.Host
	}
	ct := m.Get("Content-Type")
	f.bodyTypes, f.additional = i3.BodyParts(ct, m.Body)
	if offer != nil {
		f.sdp.Offered = true
		for _, md := range offer.Media {
			if md.Port == 0 {
				continue
			}
			var langs []string
			for _, a := range []string{"hlang-send", "hlang-recv"} {
				if v, ok := md.Attr(a); ok {
					for _, l := range strings.Fields(v) {
						langs = appendNew(langs, strings.TrimSuffix(l, "*"))
					}
				}
			}
			switch {
			case md.Type == "audio":
				f.sdp.Audio, f.sdp.LangAudio = true, append(f.sdp.LangAudio, langs...)
			case md.Type == "video":
				f.sdp.Video, f.sdp.LangVideo = true, append(f.sdp.LangVideo, langs...)
			case md.Type == "text":
				f.sdp.RTT, f.sdp.LangRTT = true, append(f.sdp.LangRTT, langs...)
			case md.Type == "message":
				f.sdp.IM, f.sdp.LangIM = true, append(f.sdp.LangIM, langs...)
			}
		}
	}
	return f
}

func appendNew(l []string, s string) []string {
	for _, x := range l {
		if strings.EqualFold(x, s) {
			return l
		}
	}
	return append(l, s)
}

func isIP(h string) bool {
	return strings.Trim(h, "[]") != "" && strings.Count(h, ".") == 3 || strings.Contains(h, ":")
}

// callSuspicion reads Call-Info purpose=emergency-CallSuspicion (an integer score; 0 when none).
func callSuspicion(m *sip.Message) int {
	for _, v := range m.GetAll("Call-Info") {
		for _, part := range strings.Split(v, ",") {
			if !strings.Contains(strings.ToLower(part), "purpose=emergency-callsuspicion") {
				continue
			}
			uri := part
			if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i >= 0 && j > i {
				uri = part[i+1 : j]
			}
			if u, err := url.PathUnescape(uri); err == nil {
				uri = u
			}
			if n, err := strconv.Atoi(firstInt.FindString(uri)); err == nil {
				return n
			}
		}
	}
	return 0
}

func (e *Engine) queueURI(cfg *config.Config) string {
	if q := cfg.System.Core.QueueURI; q != "" {
		return q
	}
	host := cfg.System.Core.Source
	if host == "" {
		host = e.PublicAddress(*cfg)
	}
	if host == "" {
		host = "esrp.invalid"
	}
	return "sip:sos@" + host
}

// policyOutcomes runs the stored policies for a call: the incoming queue's, else (once the ECRF has
// chosen) the chosen PSAP's next-hop policy. ok is false when no stored policy applies.
func (e *Engine) policyOutcomes(cfg *config.Config, c *Call, service string, loc *i3.Location, chosen string) ([]i3.Outcome, string, bool) {
	if e.Policies == nil || !cfg.System.Core.PolicyStore {
		return nil, "", false
	}
	facts := i3.CallFacts{Now: time.Now(), RequestURI: service, IncomingQueue: e.queueURI(cfg), Suspicion: c.suspicion, Location: loc,
		QueueState: func(uri string) string { return e.queueState(cfg, uri) }}
	if c.suspicious && facts.Suspicion == 0 {
		facts.Suspicion = 50 // marked by the BCF with no score of its own
	}
	if f := c.facts; f != nil {
		facts.RequestURI = f.requestURI
		facts.CallSource = f.source
		facts.BodyTypes = f.bodyTypes
		facts.Additional = f.additional
		facts.SDP = f.sdp
		facts.Header = func(n string) []string { return f.headers[strings.ToLower(sip.CanonicalName(n))] }
	}
	if e.FindService != nil && loc != nil {
		facts.Lost = func(urn string) (string, bool) {
			u, _ := e.FindService(urn, loc)
			return u, u != ""
		}
	}
	fetch := func(pt, q, id string) (i3.Policy, bool) { return e.Policies(pt, q, id) }
	if p, ok := e.Policies("OriginationRoutePolicy", facts.IncomingQueue, ""); ok {
		return i3.Evaluate(p, facts, fetch), "OriginationRoutePolicy for " + facts.IncomingQueue, true
	}
	if chosen != "" {
		if p, ok := e.Policies("NormalNextHopRoutePolicy", chosen, ""); ok {
			return i3.Evaluate(p, facts, fetch), "NormalNextHopRoutePolicy for " + chosen, true
		}
	}
	return nil, "", false
}

// queueState is what this ESRP knows of a queue: a PSAP it routes to is Active while its trunk is
// enabled and registered or reachable, else Unreachable; others are not known.
func (e *Engine) queueState(cfg *config.Config, uri string) string {
	for _, ps := range cfg.System.Core.PSAPs {
		if strings.EqualFold(ps.URI, uri) {
			if t := cfg.FindTrunk(ps.Trunk); t != nil && t.Enabled {
				return "Active"
			}
			return "Unreachable"
		}
	}
	return ""
}

// psapStage is the stage that sends a call to a PSAP's URI over its trunk, or to the takers
// registered for that queue.
func (e *Engine) psapStage(cfg *config.Config, ps *config.CorePSAP, to string, timeout time.Duration) (stage, bool) {
	t := cfg.FindTrunk(ps.Trunk)
	if t == nil || !t.Enabled {
		return stage{}, false
	}
	tc := *t
	if e.Dequeuers != nil {
		if ds := e.Dequeuers(ps.URI); len(ds) > 0 {
			var targets []target
			for _, d := range ds {
				targets = append(targets, target{trunk: &tc, number: uriUser(d.URI, to), name: ps.Name})
			}
			return stage{targets: targets, timeout: timeout, failover: true}, true
		}
	}
	return stage{targets: []target{{trunk: &tc, number: uriUser(ps.URI, to), name: ps.Name}}, timeout: timeout, failover: true}, true
}

// uriUser is the user part a PSAP's or a taker's URI names (sos, 911, a position), else to.
func uriUser(uri, to string) string {
	if u := strings.TrimPrefix(strings.TrimPrefix(uri, "sips:"), "sip:"); u != uri {
		if user, _, ok := strings.Cut(u, "@"); ok && user != "" {
			return user
		}
	} else if strings.HasPrefix(uri, "tel:") {
		return strings.TrimPrefix(uri, "tel:")
	}
	return to
}

// policyPlan turns a stored policy's outcomes into the call's stages: each route in turn (the next
// tried when one fails), a busy ending the list with 600 Busy Everywhere.
func (e *Engine) policyPlan(cfg *config.Config, c *Call, service string, outs []i3.Outcome, which, why string) (plan, bool) {
	find := func(u string) *config.CorePSAP {
		for i := range cfg.System.Core.PSAPs {
			if strings.EqualFold(cfg.System.Core.PSAPs[i].URI, u) {
				return &cfg.System.Core.PSAPs[i]
			}
		}
		return nil
	}
	p := plan{direction: "inbound"}
	var tried []string
	first := ""
	for i, o := range outs {
		if i == 0 {
			for _, n := range o.Notify {
				e.publish("esrp.notify", map[string]any{"call": c.ID, "rule": o.Rule, "eventCode": n["eventCode"], "urgency": n["urgency"], "comment": n["comment"]})
			}
			for _, l := range o.Log {
				e.publish("esrp.rulelog", map[string]any{"call": c.ID, "policy": o.Policy, "rule": o.Rule, "priority": o.Priority, "message": l})
			}
		}
		switch o.Kind {
		case "busy":
			if len(p.stages) == 0 {
				e.publish("esrp.route", Route{Call: c.ID, Service: service, Why: why + "; " + which + " rule " + o.Rule + ": busy", At: time.Now()})
				return plan{reject: 600, reason: "Busy Everywhere", direction: "inbound"}, true
			}
			tried = append(tried, "then busy")
		case "route":
			ps := find(o.URI)
			if ps == nil {
				tried = append(tried, o.URI+" (not a PSAP listed here)")
				continue
			}
			timeout := 20 * time.Second // the standard's suggested ring-no-answer default
			if o.RNATimer > 0 {
				timeout = time.Duration(o.RNATimer) * time.Second
			}
			st, ok := e.psapStage(cfg, ps, c.to, timeout)
			if !ok {
				tried = append(tried, o.URI+" (its trunk is off)")
				continue
			}
			if first == "" {
				first, p.toName, p.trunk = ps.URI, ps.Name, ps.Name
			}
			p.stages = append(p.stages, st)
			tried = append(tried, "rule "+o.Rule+" to "+o.URI)
		}
		if o.Kind == "busy" {
			break
		}
	}
	if len(p.stages) == 0 {
		return plan{}, false
	}
	e.publish("esrp.route", Route{Call: c.ID, Service: service, PSAP: first, Why: why + "; " + which + ": " + strings.Join(tried, ", "), At: time.Now()})
	return p, true
}

// mediaType is a body part's type without parameters.
func mediaType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	}
	return mt
}
