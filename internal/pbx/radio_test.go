package pbx

import (
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// tonePacket is 20 ms of a loud 1 kHz tone, G.711 µ-law.
func tonePacket(seq uint16) []byte {
	pay := make([]byte, 160)
	for i := range pay {
		pay[i] = media.LinearToUlaw(int16(8000 * math.Sin(2*math.Pi*1000*float64(int(seq)*160+i)/8000)))
	}
	return rtpPacket(seq, pay)
}

// loud says whether a µ-law packet carries more than silence.
func loud(pkt []byte) bool {
	if len(pkt) < 172 {
		return false
	}
	var sum int
	for _, b := range pkt[12:] {
		v := int(media.UlawToLinear(b))
		if v < 0 {
			v = -v
		}
		sum += v
	}
	return sum/160 > 500
}

// receivedLoud reads a socket for a while and says whether a loud packet came.
func receivedLoud(c *net.UDPConn, d time.Duration) bool {
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(d))
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return false
		}
		if loud(buf[:n]) {
			return true
		}
	}
}

// The console: a dispatcher hears a channel's traffic; push-to-talk sends their voice to that
// channel's gateway only; a patch carries one channel to the other.
func TestRadioConsole(t *testing.T) {
	gwA, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	gwB, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer gwA.Close()
	defer gwB.Close()
	// Two free even ports (and the odd ones after them) for the channels.
	free := func() int {
		for {
			c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			p := c.LocalAddr().(*net.UDPAddr).Port &^ 1
			c.Close()
			if p > 40000 && p < 64000 {
				a, e1 := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p})
				b, e2 := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p + 1})
				if a != nil {
					a.Close()
				}
				if b != nil {
					b.Close()
				}
				if e1 == nil && e2 == nil {
					return p
				}
			}
		}
	}
	portA, portB := free(), 0
	for portB = free(); portB == portA; portB = free() {
	}
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Console: "800", Channels: []config.RadioChannel{
		{ID: "a", Name: "Fire dispatch", Port: portA, Gateway: gwA.LocalAddr().String()},
		{ID: "b", Name: "Police 1", Port: portB, Gateway: gwB.LocalAddr().String()},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	disp := registered(t, e, "100")[0]
	d, code := disp.dial("800")
	if code != 200 {
		t.Fatalf("console answered %d", code)
	}
	var call string
	for i := 0; i < 20 && call == ""; i++ {
		if ps := e.Console().Positions; len(ps) == 1 {
			call = ps[0].Call
		}
		time.Sleep(50 * time.Millisecond)
	}
	if call == "" {
		t.Fatal("the dispatcher is not on the console")
	}
	chA, _ := net.ResolveUDPAddr("udp", "127.0.0.1:"+strconv.Itoa(portA))
	stop := make(chan struct{})
	defer close(stop)
	talk := func(from *net.UDPConn, to *net.UDPAddr) {
		for seq := uint16(0); ; seq++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			from.WriteToUDP(tonePacket(seq), to)
		}
	}
	go talk(gwA, chA) // traffic on Fire dispatch
	if !receivedLoud(disp.rtp, 2*time.Second) {
		t.Fatal("the dispatcher does not hear the channel")
	}
	if st := e.Console(); !st.Channels[0].Receive {
		t.Fatalf("receive not shown: %+v", st.Channels)
	}
	// The dispatcher talks; nothing goes to the radio until push-to-talk.
	go talk(disp.rtp, net.UDPAddrFromAddrPort(d.media))
	if receivedLoud(gwB, 500*time.Millisecond) {
		t.Fatal("Police 1 got audio without push-to-talk")
	}
	if err := e.PTT(call, "b", true); err != nil {
		t.Fatal(err)
	}
	if !receivedLoud(gwB, 2*time.Second) {
		t.Fatal("push-to-talk did not reach Police 1")
	}
	if st := e.Console(); st.Channels[1].TxBy != "100" {
		t.Fatalf("transmit not shown: %+v", st.Channels[1])
	}
	e.PTT(call, "b", false)
	time.Sleep(100 * time.Millisecond)
	// Patch: Fire dispatch's traffic goes out on Police 1 with nobody keying.
	if err := e.Patch("a", "b", true); err != nil {
		t.Fatal(err)
	}
	if !receivedLoud(gwB, 2*time.Second) {
		t.Fatal("the patch did not carry Fire dispatch to Police 1")
	}
}
