package pbx

import (
	"net/netip"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A provider sends calls over TLS or TCP on a connection it opens itself, from a port its system
// chooses: the trunk is known by its address then. Over UDP, and for a trunk on another transport,
// the port must still match.
func TestTrunkForStreamPorts(t *testing.T) {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{
		{ID: "tls", Name: "TLS provider", Enabled: true, Host: "192.0.2.10", Port: 5061, Transport: "tls"},
		{ID: "udp", Name: "UDP provider", Enabled: true, Host: "192.0.2.20", Port: 5060, Transport: "udp"},
	}
	e := New(cfg, events.New(), &memRecorder{}, nil)
	e.syncTrunks(cfg)
	e.trunks["tls"].addrs = []netip.Addr{netip.MustParseAddr("192.0.2.10")}
	e.trunks["udp"].addrs = []netip.Addr{netip.MustParseAddr("192.0.2.20")}
	for _, c := range []struct {
		transport, from, want string
	}{
		{"TLS", "192.0.2.10:5061", "tls"},
		{"TLS", "192.0.2.10:36627", "tls"}, // a connection the provider opened
		{"TCP", "192.0.2.10:36627", ""},    // the trunk is TLS: not on plain TCP
		{"UDP", "192.0.2.10:36627", ""},
		{"UDP", "192.0.2.20:5060", "udp"},
		{"UDP", "192.0.2.20:5070", ""},  // a phone behind the provider's address
		{"TCP", "192.0.2.20:40000", ""}, // the trunk is UDP
	} {
		got := ""
		if tr := e.trunkFor(&cfg, sip.Peer{Transport: c.transport, Addr: netip.MustParseAddrPort(c.from)}); tr != nil {
			got = tr.ID
		}
		if got != c.want {
			t.Errorf("%s from %s: trunk %q, want %q", c.transport, c.from, got, c.want)
		}
	}
}
