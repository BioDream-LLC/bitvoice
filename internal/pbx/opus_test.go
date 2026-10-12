package pbx

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// An Opus-only phone (dynamic payload type 107) calls a G.729-only phone: BitVoice converts both
// ways, and each side gets its own codec under its own payload number.
func TestOpusCallsG729(t *testing.T) {
	cfg := testConfig()
	cfg.System.Codecs = []string{"OPUS", "G729", "PCMU"}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.codecs = []sdp.Codec{{PT: 107, Name: "opus", ClockRate: 48000, Channels: 2}}
	b.codecs = []sdp.Codec{{PT: 18, Name: "G729", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	offer, _ := sdp.Parse(ring.Req.Body)
	cs := offer.Audio().Codecs()
	if len(cs) < 2 || cs[0].Name != "opus" || cs[1].Name != "G729" {
		t.Fatalf("callee offered %v", cs)
	}
	bd := b.answer(ring)
	ad := <-done
	if ad == nil {
		t.Fatal("caller not answered")
	}
	ans, _ := sdp.Parse(ad.body)
	if c := ans.Audio().Codecs(); len(c) == 0 || c[0].PT != 107 {
		t.Fatalf("caller's answer %v", c)
	}
	// Opus packets from the caller, made from PCMU speech.
	mk := media.NewTranscoder(media.PTPCMU, media.PTOpus)
	seq := 0
	opusPkt := func() []byte {
		for {
			pcm := make([]byte, 12, 172)
			pcm[0], pcm[1] = 0x80, 0
			binary.BigEndian.PutUint16(pcm[2:], uint16(seq))
			binary.BigEndian.PutUint32(pcm[4:], uint32(seq*160))
			for i := 0; i < 160; i++ {
				pcm = append(pcm, media.LinearToUlaw(int16(4000*((seq*160+i)%40-20)/20)))
			}
			seq++
			if p := mk.Convert(pcm); p != nil {
				p[1] = p[1]&0x80 | 107
				return p
			}
		}
	}
	recv := func(p *phone, send func()) (pt, size int) {
		buf := make([]byte, 2048)
		for i := 0; i < 40; i++ {
			send()
			_ = p.rtp.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
			if n, _, err := p.rtp.ReadFromUDP(buf); err == nil && n >= 12 && buf[1]&0x7f != 101 {
				return int(buf[1] & 0x7f), n - 12
			}
		}
		return -1, 0
	}
	if pt, size := recv(b, func() { _, _ = a.rtp.WriteToUDPAddrPort(opusPkt(), ad.media) }); pt != 18 || size%10 != 0 || size == 0 {
		t.Fatalf("callee got payload type %d, %d bytes; want G.729", pt, size)
	}
	g := media.NewTranscoder(media.PTPCMU, media.PTG729)
	gseq := 0
	if pt, size := recv(a, func() {
		pcm := make([]byte, 12, 172)
		pcm[0] = 0x80
		binary.BigEndian.PutUint16(pcm[2:], uint16(gseq))
		binary.BigEndian.PutUint32(pcm[4:], uint32(gseq*160))
		for i := 0; i < 160; i++ {
			pcm = append(pcm, media.LinearToUlaw(int16(3000*((gseq*160+i)%32-16)/16)))
		}
		gseq++
		if p := g.Convert(pcm); p != nil {
			_, _ = b.rtp.WriteToUDPAddrPort(p, bd.media)
		}
	}); pt != 107 || size == 0 {
		t.Fatalf("caller got payload type %d (%d bytes), want Opus as 107", pt, size)
	}
}

// BitVoice answers an Opus-only phone itself (the echo test) and speaks Opus back to it.
func TestLocalAnswerOpusOnly(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	a.codecs = []sdp.Codec{{PT: 120, Name: "opus", ClockRate: 48000, Channels: 2}}
	d, code := a.dial("*43")
	if code != 200 {
		t.Fatalf("echo answered %d", code)
	}
	ans, _ := sdp.Parse(d.body)
	if cs := ans.Audio().Codecs(); len(cs) == 0 || cs[0].PT != 120 || cs[0].Name != "opus" {
		t.Fatalf("answer codecs %v", cs)
	}
	mk := media.NewTranscoder(media.PTPCMU, media.PTOpus)
	buf := make([]byte, 2048)
	for i := 0; i < 60; i++ {
		pcm := make([]byte, 12, 172)
		pcm[0] = 0x80
		binary.BigEndian.PutUint16(pcm[2:], uint16(i))
		binary.BigEndian.PutUint32(pcm[4:], uint32(i*160))
		for k := 0; k < 160; k++ {
			pcm = append(pcm, media.LinearToUlaw(int16(4000*((i*160+k)%40-20)/20)))
		}
		if p := mk.Convert(pcm); p != nil {
			p[1] = p[1]&0x80 | 120
			_, _ = a.rtp.WriteToUDPAddrPort(p, d.media)
		}
		_ = a.rtp.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		if n, _, err := a.rtp.ReadFromUDP(buf); err == nil && n > 12 && buf[1]&0x7f == 120 {
			return
		}
	}
	t.Fatal("no Opus echo")
}

// An Opus phone with key presses at 48000 calls a G.711 phone with them at 8000: a menu still hears
// the key, and the G.711 phone gets the key under its own number, its duration rescaled.
func TestOpusKeyPressesConverted(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.codecs = []sdp.Codec{{PT: 111, Name: "opus", ClockRate: 48000, Channels: 2}, {PT: 110, Name: "telephone-event", ClockRate: 48000}}
	b.codecs = []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	offer, _ := sdp.Parse(ring.Req.Body)
	ev8 := eventFor(offer.Audio().Codecs(), 8000)
	if ev8 < 0 || ev8 == 110 {
		t.Fatalf("callee not offered key presses at 8000: %v", offer.Audio().Codecs())
	}
	b.answer(ring) // it answers with its own number for key presses, 101
	ad := <-done
	if ad == nil {
		t.Fatal("caller not answered")
	}
	mk := media.NewTranscoder(media.PTPCMU, media.PTOpus)
	var seq uint16
	var ts uint32
	audio := func() {
		pcm := make([]byte, 12, 172)
		pcm[0] = 0x80
		binary.BigEndian.PutUint16(pcm[2:], seq)
		binary.BigEndian.PutUint32(pcm[4:], uint32(seq)*160)
		for i := 0; i < 160; i++ {
			pcm = append(pcm, media.LinearToUlaw(int16(3000*((i%20)-10)/10)))
		}
		if p := mk.Convert(pcm); p != nil {
			ts = binary.BigEndian.Uint32(p[4:])
			binary.BigEndian.PutUint16(p[2:], seq)
			_, _ = a.rtp.WriteToUDPAddrPort(p, ad.media)
		}
		seq++
	}
	for range 10 {
		audio()
		time.Sleep(20 * time.Millisecond)
	}
	// Key 5 for 960 * 6 ticks at 48000 (120 ms), end sent three times.
	evTS := ts + 960
	for i := 0; i < 6; i++ {
		pkt := make([]byte, 16)
		pkt[0], pkt[1] = 0x80, 110
		binary.BigEndian.PutUint16(pkt[2:], seq)
		seq++
		binary.BigEndian.PutUint32(pkt[4:], evTS)
		pkt[12] = 5
		dur := uint16(min(i+1, 3) * 1920)
		if i >= 3 {
			pkt[13] = 0x80
		}
		binary.BigEndian.PutUint16(pkt[14:], dur)
		_, _ = a.rtp.WriteToUDPAddrPort(pkt, ad.media)
		time.Sleep(20 * time.Millisecond)
	}
	buf := make([]byte, 2048)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = b.rtp.SetReadDeadline(deadline)
		n, _, err := b.rtp.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n == 16 && int(buf[1]&0x7f) == 101 && buf[13]&0x80 != 0 {
			if d := binary.BigEndian.Uint16(buf[14:]); buf[12] != 5 || d != 960 {
				t.Fatalf("key %d, duration %d at 8000 (want 5, 960)", buf[12], d)
			}
			return
		}
	}
	t.Fatal("the G.711 phone never got the key press")
}
