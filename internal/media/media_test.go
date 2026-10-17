package media

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestG711RoundTrip(t *testing.T) {
	for _, s := range []int16{0, 1, -1, 100, -100, 1000, -1000, 8000, -8000, 32000, -32000, 32767, -32768} {
		for name, pair := range map[string][2]func(int16) int16{
			"ulaw": {func(x int16) int16 { return UlawToLinear(LinearToUlaw(x)) }},
			"alaw": {func(x int16) int16 { return AlawToLinear(LinearToAlaw(x)) }},
		} {
			got := pair[0](s)
			diff := int(got) - int(s)
			if diff < 0 {
				diff = -diff
			}
			// Companding error grows with amplitude: at most about 1/16 of the value plus a small floor.
			limit := int(abs(s))/16 + 16
			if diff > limit {
				t.Errorf("%s %d -> %d (error %d > %d)", name, s, got, diff, limit)
			}
		}
	}
	if LinearToUlaw(0) != 0xff || LinearToAlaw(0) != 0xd5 {
		t.Errorf("silence encodes as %#x / %#x, want 0xff / 0xd5", LinearToUlaw(0), LinearToAlaw(0))
	}
}

func abs(x int16) int32 {
	if x < 0 {
		return -int32(x)
	}
	return int32(x)
}

func rtpPacket(seq uint16, ts uint32) []byte {
	p := make([]byte, 172)
	p[0], p[1] = 0x80, 0
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], ts)
	return p
}

func TestRelayLatchesAndForwards(t *testing.T) {
	pool := &Pool{Host: "127.0.0.1", Min: 31000, Max: 31100}
	a, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	if a.Port()%2 != 0 || a.Port() == b.Port() {
		t.Fatalf("ports %d %d", a.Port(), b.Port())
	}
	phoneA, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	phoneB, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer phoneA.Close()
	defer phoneB.Close()

	// SDP for B points somewhere wrong; latching must fix it when B speaks.
	b.SetRemote("127.0.0.1", 9)
	Connect(a, b)
	a.Start()
	b.Start()

	toA := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.Port()}
	toB := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: b.Port()}
	if _, err := phoneB.WriteToUDP(rtpPacket(1, 160), toB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	for i := uint16(0); i < 5; i++ {
		seq := 100 + i
		if i == 3 {
			continue // one lost packet
		}
		if _, err := phoneA.WriteToUDP(rtpPacket(seq, uint32(seq)*160), toA); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 2048)
	_ = phoneB.SetReadDeadline(time.Now().Add(15 * time.Second))
	got := 0
	for got < 4 {
		n, _, err := phoneB.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("after %d packets: %v", got, err)
		}
		if n != 172 {
			t.Fatalf("size %d", n)
		}
		got++
	}
	st := a.Stats()
	if st.Packets != 4 || st.Lost != 1 || !st.Latched {
		t.Fatalf("stats %+v", st)
	}
	if st.MOS <= 1 || st.MOS > 4.5 {
		t.Fatalf("MOS %v", st.MOS)
	}
}

func TestEchoAndTone(t *testing.T) {
	pool := &Pool{Host: "127.0.0.1", Min: 31200, Max: 31300}
	e, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Echo()
	e.Start()
	phone, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer phone.Close()
	to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: e.Port()}
	_, _ = phone.WriteToUDP(rtpPacket(7, 7), to)
	buf := make([]byte, 2048)
	_ = phone.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, _, err := phone.ReadFromUDP(buf); err != nil {
		t.Fatalf("echo: %v", err)
	}

	p := NewPlayer(e, 0)
	p.Play(Tone{Freqs: []float64{1000}, Level: 0.3, On: 40 * time.Millisecond, Off: 20 * time.Millisecond, Repeat: 1})
	n, _, err := phone.ReadFromUDP(buf)
	if err != nil || n != 172 || buf[1] != 0 {
		t.Fatalf("tone packet: %d %v", n, err)
	}
	select {
	case <-p.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("finite tone did not finish")
	}
}

// Packets that arrive out of order are late, not lost: only the ones that never come count, as
// RFC 3550 counts them, across a sequence number wrap.
func TestLossWithReordering(t *testing.T) {
	e := &Endpoint{wantPT: -1}
	pkt := func(seq uint16) []byte {
		p := make([]byte, 172)
		p[0] = 0x80
		p[2], p[3] = byte(seq>>8), byte(seq)
		return p
	}
	now := time.Now()
	start := uint16(65000) // wraps past 65535
	var order []uint16
	for i := 0; i < 1000; i += 2 {
		a, b := start+uint16(i), start+uint16(i+1)
		order = append(order, b, a) // every pair swapped
	}
	lost := 0
	for i, s := range order {
		if i%20 == 7 { // one in twenty never arrives
			lost++
			continue
		}
		e.account(pkt(s), now)
	}
	if got := e.stats.Lost; int(got) > lost+1 || int(got) < lost-1 {
		t.Fatalf("lost %d, want %d", got, lost)
	}
}
