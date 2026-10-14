package pbx

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// Two G.723.1 phones: BitVoice cannot convert G.723.1, so it passes the frames untouched between
// parties that both use it, and never offers it to a party whose caller did not.
func TestG723PassesThrough(t *testing.T) {
	cfg := testConfig()
	cfg.System.Codecs = []string{"G723", "PCMU"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	a.codecs = []sdp.Codec{{PT: 4, Name: "G723", ClockRate: 8000}}
	b.codecs = []sdp.Codec{{PT: 4, Name: "G723", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	off, _ := sdp.Parse(ring.Req.Body)
	if cs := off.Audio().Codecs(); len(cs) == 0 || cs[0].PT != 4 {
		t.Fatalf("offer %v", cs)
	}
	b.answer(ring)
	ad := <-done
	if ad == nil {
		t.Fatal("caller not answered")
	}
	frame := make([]byte, 24) // one 6.3 kbit/s frame
	for i := range frame {
		frame[i] = byte(i * 7)
	}
	p := make([]byte, 12, 36)
	p[0], p[1] = 0x80, 4
	binary.BigEndian.PutUint32(p[8:], 1234)
	p = append(p, frame...)
	buf := make([]byte, 2048)
	got := false
	for i := 0; i < 30 && !got; i++ {
		binary.BigEndian.PutUint16(p[2:], uint16(i))
		_, _ = a.rtp.WriteToUDPAddrPort(p, ad.media)
		_ = b.rtp.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
		if n, _, err := b.rtp.ReadFromUDP(buf); err == nil && n == 36 && buf[1]&0x7f == 4 && string(buf[12:n]) == string(frame) {
			got = true
		}
	}
	if !got {
		t.Fatal("the G.723.1 frame did not arrive untouched")
	}
	a.send(ad.request("BYE"))
	b.waitBye(t, 2*time.Second)

	// A PCMU caller: G.723.1 is not offered to the callee (BitVoice could not convert it).
	go func() { d, _ := c.dial("101"); done <- d }()
	ring = b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	off, _ = sdp.Parse(ring.Req.Body)
	for _, x := range off.Audio().Codecs() {
		if x.Name == "G723" {
			t.Fatalf("G.723.1 offered for a PCMU caller: %v", off.Audio().Codecs())
		}
	}
	b.answer(ring)
	<-done
}
