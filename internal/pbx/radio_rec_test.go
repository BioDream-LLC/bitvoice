package pbx

import (
	"net"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
)

// Every transmission on a channel is recorded and logged: a radio heard through the gateway (cut
// at the end of its speech), then the dispatcher answering on push-to-talk.
func TestRadioRecording(t *testing.T) {
	gw, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer gw.Close()
	port := freeUDP(t) &^ 1
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Console: "800", Record: true,
		Channels: []config.RadioChannel{{ID: "a", Name: "Fire dispatch", Port: port, Gateway: gw.LocalAddr().String()}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, _ := storetest.Open(t)
	e, rec := startEngine(t, cfg, func(e *Engine) { e.Recordings = st })
	disp := registered(t, e, "100")[0]
	d, code := disp.dial("800")
	if code != 200 {
		t.Fatalf("console answered %d", code)
	}
	var call string
	for i := 0; i < 40 && call == ""; i++ {
		if ps := e.Console().Positions; len(ps) == 1 {
			call = ps[0].Call
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The radio talks for a second; the gateway keeps sending silence after.
	ch := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	silence := make([]byte, 160)
	for i := range silence {
		silence[i] = 0xff
	}
	for seq := uint16(0); seq < 150; seq++ {
		pkt := tonePacket(seq)
		if seq >= 50 {
			pkt = rtpPacket(seq, silence)
		}
		gw.WriteToUDP(pkt, ch)
		time.Sleep(20 * time.Millisecond)
	}
	// The dispatcher answers for a second.
	if err := e.PTT(call, "a", true); err != nil {
		t.Fatal(err)
	}
	for seq := uint16(0); seq < 50; seq++ {
		disp.rtp.WriteToUDP(voicePacket(seq), net.UDPAddrFromAddrPort(d.media))
		time.Sleep(20 * time.Millisecond)
	}
	e.PTT(call, "a", false)
	deadline := time.Now().Add(3 * time.Second)
	for len(rec.transmissions()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	xs := rec.transmissions()
	if len(xs) != 2 {
		t.Fatalf("radio log %+v", xs)
	}
	rx, tx := xs[0], xs[1]
	if rx.Direction != "rx" || rx.ChannelName != "Fire dispatch" || rx.Seconds < 0.8 || rx.Seconds > 1.6 || rx.Recording == "" {
		t.Fatalf("heard transmission %+v", rx)
	}
	if tx.Direction != "tx" || tx.Dispatcher != "100" || tx.Seconds < 0.6 || tx.Recording == "" {
		t.Fatalf("sent transmission %+v", tx)
	}
	wav, err := st.Recording(rx.Recording)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := media.DecodeWAV(wav)
	if err != nil || len(pcm) < 6000 {
		t.Fatalf("recording %d samples, %v", len(pcm), err)
	}
}
