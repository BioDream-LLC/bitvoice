package pbx

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// A G.726-32 phone (dynamic payload type 99) calls a GSM phone: BitVoice converts both ways, each side
// in its own codec under its own number.
func TestG726CallsGSM(t *testing.T) {
	cfg := testConfig()
	cfg.System.Codecs = []string{"G726-32", "GSM", "PCMU"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.codecs = []sdp.Codec{{PT: 99, Name: "G726-32", ClockRate: 8000}}
	b.codecs = []sdp.Codec{{PT: 3, Name: "GSM", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	bd := b.answer(ring)
	ad := <-done
	if ad == nil {
		t.Fatal("caller not answered")
	}
	ans, _ := sdp.Parse(ad.body)
	if c := ans.Audio().Codecs(); len(c) == 0 || c[0].PT != 99 {
		t.Fatalf("caller's answer %v", c)
	}
	tone := func(seq int) []int16 {
		pcm := make([]int16, 160)
		for i := range pcm {
			pcm[i] = int16(5000 * ((seq*160+i)%40 - 20) / 20)
		}
		return pcm
	}
	g726 := media.NewG726Encoder(32)
	gsm := media.NewGSMEncoder()
	pkt := func(pt byte, seq int, payload []byte) []byte {
		p := make([]byte, 12, 12+len(payload))
		p[0], p[1] = 0x80, pt
		binary.BigEndian.PutUint16(p[2:], uint16(seq))
		binary.BigEndian.PutUint32(p[4:], uint32(seq*160))
		return append(p, payload...)
	}
	recv := func(p *phone, send func(int)) (pt, size int) {
		buf := make([]byte, 2048)
		for i := 0; i < 40; i++ {
			send(i)
			_ = p.rtp.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
			if n, _, err := p.rtp.ReadFromUDP(buf); err == nil && n >= 12 && buf[1]&0x7f != 101 {
				return int(buf[1] & 0x7f), n - 12
			}
		}
		return -1, 0
	}
	if pt, size := recv(b, func(i int) { _, _ = a.rtp.WriteToUDPAddrPort(pkt(99, i, g726.Encode(tone(i))), ad.media) }); pt != 3 || size%33 != 0 || size == 0 {
		t.Fatalf("callee got payload type %d, %d bytes; want GSM", pt, size)
	}
	if pt, size := recv(a, func(i int) { _, _ = b.rtp.WriteToUDPAddrPort(pkt(3, i, gsm.Encode(tone(i))), bd.media) }); pt != 99 || size == 0 || size%80 != 0 {
		t.Fatalf("caller got payload type %d (%d bytes), want G.726-32 as 99", pt, size)
	}
}
