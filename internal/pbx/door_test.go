package pbx

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// keyPacket is an RFC 4733 end-of-event packet for a key.
func keyPacket(ev byte, ts uint32, seq uint16) []byte {
	p := make([]byte, 16)
	p[0], p[1] = 0x80, 101
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], ts)
	p[12], p[13] = ev, 0x80|10
	binary.BigEndian.PutUint16(p[14:], 800)
	return p
}

func TestDoorStation(t *testing.T) {
	var opened atomic.Int32
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u == "admin" && p == "door-pass" && r.URL.Path == "/api/switch/ctrl" {
			opened.Add(1)
		}
	}))
	defer ctrl.Close()
	cfg := testConfig()
	cfg.Extensions[0].Kind = "door"
	cfg.Extensions[0].Door = config.Door{Code: "*1", URL: ctrl.URL + "/api/switch/ctrl", Username: "admin", Password: "door-pass"}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	door, b := ps[0], ps[1]
	done := make(chan *dialog, 1)
	go func() { d, _ := door.dial("101"); done <- d }()
	bd := b.answer(b.waitRing(2 * time.Second))
	if <-done == nil {
		t.Fatal("not answered")
	}
	// The person answering presses * then 1 (each end packet three times, as phones send them). Under
	// load the first presses can arrive before the call's media is joined up: press again, as a person
	// would, until the door opens.
	deadline := time.Now().Add(15 * time.Second)
	for try := 0; opened.Load() == 0 && time.Now().Before(deadline); try++ {
		for i, ev := range []byte{10, 1} {
			for j := 0; j < 3; j++ {
				_, _ = b.rtp.WriteToUDPAddrPort(keyPacket(ev, uint32(1000*(2*try+i+1)), uint16(6*try+3*i+j)), bd.media)
			}
			time.Sleep(20 * time.Millisecond)
		}
		for k := 0; k < 25 && opened.Load() == 0; k++ {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if opened.Load() != 1 {
		t.Fatalf("door opened %d times by keys", opened.Load())
	}
	// The button on the screen.
	if err := e.OpenDoor(e.Calls()[0].ID, "admin"); err != nil || opened.Load() != 2 {
		t.Fatalf("button: %v, opened %d", err, opened.Load())
	}
}

// Without a controller address the button sends the code to the door station as key presses.
func TestDoorStationOpensItself(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].Kind = "door"
	cfg.Extensions[1].Door = config.Door{Code: "#"}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	a, door := ps[0], ps[1]
	go a.dial("101")
	door.answer(door.waitRing(2 * time.Second))
	time.Sleep(100 * time.Millisecond)
	door.drainRTP()
	if err := e.OpenDoor(e.Calls()[0].ID, "admin"); err != nil {
		t.Fatal(err)
	}
	_ = door.rtp.SetReadDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 64)
	n, _, err := door.rtp.ReadFromUDP(buf)
	if err != nil || n != 16 || buf[1]&0x7f != 101 || buf[12] != 11 {
		t.Fatalf("door station got %v %x", err, buf[:n])
	}
}

func TestSpeakerAndAnalogExtensions(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].Kind = "speaker"
	cfg.Extensions[2].Kind = "analog"
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	a, spk, ata := ps[0], ps[1], ps[2]
	a.codecs = []sdp.Codec{{PT: 9, Name: "G722", ClockRate: 8000}, {PT: 0, Name: "PCMU", ClockRate: 8000}}
	go a.dial("101")
	ring := spk.waitRing(2 * time.Second)
	if ring == nil || !strings.Contains(ring.Req.Get("Call-Info"), "answer-after=0") {
		t.Fatalf("speaker not asked to answer at once: %v", ring)
	}
	go a.dial("102")
	ring = ata.waitRing(2 * time.Second)
	s, _ := sdp.Parse(ring.Req.Body)
	for _, c := range s.Audio().Codecs() {
		if c.Name == "G722" {
			t.Fatal("analog port offered G.722")
		}
	}
}

// A page also goes out to a multicast group, as G.711 µ-law, even when no phone is in the group.
func TestMulticastPaging(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(239, 255, 77, 77), Port: 35077}
	lis, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		t.Skip("multicast not available here:", err)
	}
	defer lis.Close()
	cfg := testConfig()
	cfg.PageGroups = []config.PageGroup{{Number: "800", Name: "All", Multicast: addr.String()}}
	e, _ := startEngine(t, cfg)
	a := registered(t, e, "100")[0]
	a.codecs = []sdp.Codec{{PT: 8, Name: "PCMA", ClockRate: 8000}}
	d, code := a.dial("800")
	if d == nil {
		t.Fatalf("page refused: %d", code)
	}
	got := make(chan byte, 1)
	go func() {
		buf := make([]byte, 2048)
		_ = lis.SetReadDeadline(time.Now().Add(3 * time.Second))
		if n, _, err := lis.ReadFromUDP(buf); err == nil && n >= 12 {
			got <- buf[1] & 0x7f
		}
		close(got)
	}()
	for i := 0; i < 40; i++ {
		pkt := make([]byte, 172)
		pkt[0], pkt[1] = 0x80, 8
		binary.BigEndian.PutUint16(pkt[2:], uint16(i))
		_, _ = a.rtp.WriteToUDPAddrPort(pkt, d.media)
		time.Sleep(20 * time.Millisecond)
	}
	pt, ok := <-got
	if !ok {
		t.Skip("multicast loopback not delivered on this host")
	}
	if pt != 0 {
		t.Fatalf("multicast payload type %d, want PCMU", pt)
	}
}
