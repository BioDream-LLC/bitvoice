package pbx

import (
	"net/netip"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Microsoft Teams Direct Routing: BitVoice acts as the session border controller (SBC). Microsoft's
// side (the SIP proxy) lives at three names, tried in this order, and signals from these ranges.
var (
	TeamsHubs   = []string{"sip.pstnhub.microsoft.com", "sip2.pstnhub.microsoft.com", "sip3.pstnhub.microsoft.com"}
	TeamsRanges = []string{"52.112.0.0/14", "52.120.0.0/14"}
)

// teamsTrunkFor finds the Teams trunk a peer belongs to, if any.
func (e *Engine) teamsTrunkFor(p sip.Peer) *config.Trunk {
	cfg := e.Config()
	ip := p.Addr.Addr().Unmap()
	for i := range cfg.Trunks {
		t := &cfg.Trunks[i]
		if !t.Enabled || !t.Teams {
			continue
		}
		if strings.EqualFold(p.Host, t.Host) || slicesContainsFold(TeamsHubs, p.Host) {
			return t
		}
		e.mu.Lock()
		st := e.trunks[t.ID]
		var addrs []netip.Addr
		if st != nil {
			addrs = st.addrs
		}
		e.mu.Unlock()
		for _, a := range addrs {
			if a == ip {
				return t
			}
		}
		for _, r := range append(append([]string{}, TeamsRanges...), t.AllowFrom...) {
			if pr, err := netip.ParsePrefix(r); err == nil && pr.Contains(ip) {
				return t
			}
		}
	}
	return nil
}

// teamsContact is the Contact Microsoft requires: the SBC's registered name, on the TLS port.
func (e *Engine) teamsContact(t *config.Trunk, user string) string {
	port := e.stack.LocalPort("TLS")
	if port == 0 {
		port = 5061
	}
	u := &sip.URI{Scheme: "sip", User: user, Host: t.SBCName, Port: port}
	u.Params.Set("transport", "tls")
	return "<" + u.String() + ">"
}

// teamsHeaders marks a request to Teams as coming from this SBC.
func teamsHeaders(m *sip.Message) {
	m.Set("X-MS-SBC", "BioDream/BitVoice/1")
}

func slicesContainsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
