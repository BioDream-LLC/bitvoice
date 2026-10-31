package pbx

import (
	"net"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// A browser's video endpoint binds to the address it is told to reach only when that address is this
// computer's: a cloud server's public address (one-to-one NAT) is not, and a socket bound to it (Linux
// allows that) would never see the packets, which arrive on the private address.
func TestOwnAddress(t *testing.T) {
	if !ownAddress(net.ParseIP("127.0.0.1")) {
		t.Error("loopback is this computer's")
	}
	if ownAddress(net.ParseIP("203.0.113.9")) {
		t.Error("a documentation address is not this computer's")
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !ownAddress(n.IP) {
			t.Errorf("%s is on an interface but not seen as this computer's", n.IP)
		}
	}
	ep, err := (&Engine{pool: &media.Pool{Min: 35600, Max: 35700}}).browserEndpoint("203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	if ip := ep.LocalAddr(); ip.IsValid() && !ip.IsUnspecified() {
		t.Errorf("an endpoint for a NAT's public address is bound to %s, not every address", ip)
	}
}
