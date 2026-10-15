package pbx

import (
	"encoding/binary"
	"os/exec"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// A G.723.1-only phone calls a PCMU-only phone. With G.723.1 conversion on (and ffmpeg installed),
// the call connects and BitVoice converts both ways through ffmpeg; with it off, G.723.1 only
// passes, and these two have no codec in common.
func TestG723Converted(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	cfg := testConfig()
	cfg.System.Codecs = []string{"PCMU", "G723"}
	cfg.System.G723Convert = true
	e, _ := startEngine(t, cfg)
	defer media.SetG723Converter("")
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.codecs = []sdp.Codec{{PT: 4, Name: "G723", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	bd := b.answer(ring)
	ad := <-done
	if ad == nil {
		t.Fatal("the caller was not answered")
	}
	if ans, _ := sdp.Parse(ad.body); len(ans.Audio().Codecs()) == 0 || ans.Audio().Codecs()[0].PT != 4 {
		t.Fatalf("the caller's answer %v", ans.Audio().Codecs())
	}
	// PCMU from 101 reaches 100 as G.723.1 (24-byte frames, after ffmpeg has started).
	seq := 0
	buf := make([]byte, 2048)
	got := -1
	for i := 0; i < 150 && got < 0; i++ {
		pkt := make([]byte, 12, 172)
		pkt[0] = 0x80
		binary.BigEndian.PutUint16(pkt[2:], uint16(seq))
		binary.BigEndian.PutUint32(pkt[4:], uint32(seq*160))
		for k := 0; k < 160; k++ {
			pkt = append(pkt, media.LinearToUlaw(int16(4000*((seq*160+k)%40-20)/20)))
		}
		seq++
		_, _ = b.rtp.WriteToUDPAddrPort(pkt, bd.media)
		_ = a.rtp.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		if n, _, err := a.rtp.ReadFromUDP(buf); err == nil && n >= 12 && buf[1]&0x7f == 4 && (n-12)%24 == 0 && n > 12 {
			got = n - 12
		}
	}
	if got < 0 {
		t.Fatal("no G.723.1 reached the caller")
	}
}
