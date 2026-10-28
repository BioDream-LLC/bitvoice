package media

import (
	"net"
	"testing"
	"time"
)

// A port another program holds on 127.0.0.1 is skipped: sharing it, media sent to 127.0.0.1 would go
// to the other program (seen with NVIDIA's nvcontainer on 127.0.0.1:10010).
func TestPortHeldOnLoopbackIsSkipped(t *testing.T) {
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	held := other.LocalAddr().(*net.UDPAddr).Port &^ 1
	if held != other.LocalAddr().(*net.UDPAddr).Port {
		// The held port is the RTCP one of a pair: hold the RTP one too, so either is in the way.
		o2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: held})
		if err == nil {
			defer o2.Close()
		}
	}
	p := &Pool{Min: held, Max: held + 4}
	ep, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	if ep.Port() == held {
		t.Fatalf("got port %d, which another socket holds on 127.0.0.1", held)
	}
	s, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer s.Close()
	s.WriteToUDP([]byte("x"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ep.Port()})
	ep.rtp.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ep.rtp.ReadFromUDP(make([]byte, 8)); err != nil {
		t.Fatalf("a packet to 127.0.0.1:%d did not arrive: %v", ep.Port(), err)
	}
}
