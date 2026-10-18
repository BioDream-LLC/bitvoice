package sip

import (
	"net/netip"
	"testing"
)

// With a public address set, peers on the internet are given it; peers on a private network beside
// this server are given the local address that routes to them (they may not reach the public one).
func TestLocalHostPublicOnlyForPublicPeers(t *testing.T) {
	s := &Stack{Host: "203.0.113.7"}
	if h := s.LocalHost(Peer{Addr: netip.MustParseAddrPort("198.51.100.20:5060")}); h != "203.0.113.7" {
		t.Fatalf("internet peer got %s", h)
	}
	for _, p := range []string{"10.1.2.3:5060", "192.168.1.20:5060", "172.31.5.6:5070", "100.64.1.1:5060", "[fd00::5]:5060"} {
		if h := s.LocalHost(Peer{Addr: netip.MustParseAddrPort(p)}); h == "203.0.113.7" {
			t.Fatalf("private peer %s was given the public address", p)
		}
	}
	if h := s.LocalHost(Peer{Addr: netip.MustParseAddrPort("127.0.0.1:5060")}); h != "127.0.0.1" {
		t.Fatalf("loopback peer got %s", h)
	}
	if h := s.LocalHost(Peer{Addr: netip.MustParseAddrPort("10.1.2.3:5060"), Local: netip.MustParseAddr("10.0.0.50")}); h != "10.0.0.50" {
		t.Fatalf("peer through the shared address got %s", h)
	}
}
