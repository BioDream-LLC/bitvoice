package pbx

import (
	"slices"
	"strconv"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// peerHops is the most phone systems a call passed on between sites may still go through: a loop of
// routes (Texas sends 3xxx to the hub, the hub back to Texas) ends after this many, not after 70.
const peerHops = 10

// peerPlan plans a call that another of the organization's phone systems sent on a peer trunk, for a
// number dialled there: one of this server's own numbers (an extension, ring group, queue, conference
// and so on, as if dialled here, with no feature codes or voicemail access), or, for a hub ("onward"),
// a number this server's outbound routes take. An emergency number is always passed on: an emergency
// call is never refused here. false: the number is none of those, and the incoming routes decide.
func (e *Engine) peerPlan(cfg *config.Config, t *config.Trunk, m *sip.Message, did string) (plan, bool) {
	did = cleanNumber(did)
	if did == "" {
		return plan{}, false
	}
	emergency := slices.Contains(cfg.System.EmergencyNumbers, did)
	p := e.routeCall(cfg, nil, did, true)
	switch {
	case p.direction == "internal" || p.direction == "test":
		if p.reject != 0 {
			return plan{}, false
		}
		p.why = append([]string{t.Name + " is another of your phone systems, and " + did + " is a number here."}, p.why...)
		return p, true
	case p.direction == "outbound" && t.Peer == "onward" && p.reject == 0, emergency:
	default:
		return plan{}, false
	}
	hops := peerHops
	if mf, err := strconv.Atoi(m.Get("Max-Forwards")); err == nil {
		if mf <= 1 && !emergency {
			return plan{reject: 483, reason: "Too Many Hops", why: []string{t.Name + " passed on " + did +
				" after it had been through too many phone systems: the sites' outbound routes send it round in a loop."}}, true
		}
		hops = min(max(mf-1, 1), peerHops)
	}
	p.hops = hops
	why := t.Name + " is a hub trunk, so " + did + " went on through this server's outbound routes."
	if emergency {
		why = t.Name + " passed on an emergency call to " + did + ": it goes out from here, never refused."
	}
	p.why = append([]string{why}, p.why...)
	return p, true
}
