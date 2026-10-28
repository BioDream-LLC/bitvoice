package sip

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// A SIP port another program holds on one address is refused, not silently shared.
func TestSIPPortHeldElsewhereIsRefused(t *testing.T) {
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	port := other.LocalAddr().(*net.UDPAddr).Port
	s := &Stack{}
	err = s.Listen(net.JoinHostPort("", strconv.Itoa(port)))
	if err == nil {
		s.udp.Close()
		s.tcp.Close()
		t.Fatalf("port %d was taken although another socket holds it on 127.0.0.1", port)
	}
	if !addrInUse(err) || !strings.Contains(err.Error(), "listen udp") {
		t.Fatalf("error %v", err)
	}
}
