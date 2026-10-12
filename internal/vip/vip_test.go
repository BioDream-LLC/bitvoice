package vip

import (
	"net"
	"net/netip"
	"testing"
)

func TestFrame(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	b := frame(mac, netip.MustParseAddr("192.0.2.50"), 2)
	if len(b) != 42 || b[12] != 0x08 || b[13] != 0x06 || b[21] != 2 || b[28] != 192 || b[41] != 50 || b[6] != 2 {
		t.Fatalf("frame % x", b)
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse("192.0.2.50", "eth0"); err == nil {
		t.Fatal("no prefix accepted")
	}
	a, err := Parse("192.0.2.50/24", "eth0")
	if err != nil || a.Interface != "eth0" {
		t.Fatalf("%+v %v", a, err)
	}
}
