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
	_ = c.SetReadDeadline(time.Now().Add(patient(d)))
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
	portA, portB := freeUDP(t), freeUDP(t) // two even ports, free with the odd ones after them
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
	e.Patch("a", "b", false)

	// Simulselect: one transmission on both channels at once.
	if err := e.SimulSelect(call, []string{"a", "b"}, true); err != nil {
		t.Fatal(err)
	}
	if !receivedLoud(gwA, 2*time.Second) || !receivedLoud(gwB, 2*time.Second) {
		t.Fatal("simulselect did not reach both gateways")
	}
	if st := e.Console(); st.Channels[0].TxBy != "100" || st.Channels[1].TxBy != "100" {
		t.Fatalf("simulselect not shown: %+v", st.Channels)
	}
	if err := e.SimulSelect(call, []string{"a", "b"}, false); err != nil {
		t.Fatal(err)
	}
	// All or nothing: with one channel in another dispatcher's hands, neither is keyed.
	r := e.radioSys()
	r.mu.Lock()
	r.channels["a"].tx = "another-dispatcher"
	r.mu.Unlock()
	if err := e.SimulSelect(call, []string{"b", "a"}, true); err == nil {
		t.Fatal("simulselect keyed a channel in use")
	}
	if st := e.Console(); st.Channels[1].TxBy != "" {
		t.Fatalf("Police 1 was keyed although Fire dispatch was busy: %+v", st.Channels[1])
	}
}
