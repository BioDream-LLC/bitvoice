package pbx

import (
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/p25"
)

// toneHz finds the strongest frequency in 20 ms of µ-law (Goertzel, 500-2000 Hz in 50 Hz steps).
func toneHz(payload []byte) (float64, float64) {
	best, bestP, total := 0.0, 0.0, 0.0
	pcm := make([]float64, len(payload))
	for i, b := range payload {
		pcm[i] = float64(media.UlawToLinear(b))
		total += pcm[i] * pcm[i]
	}
	for f := 500.0; f <= 2000; f += 50 {
		w := 2 * math.Pi * f / 8000
		var s1, s2 float64
		for _, x := range pcm {
			s1, s2 = x+2*math.Cos(w)*s1-s2, s1
		}
		if p := s1*s1 + s2*s2 - 2*math.Cos(w)*s1*s2; p > bestP {
			best, bestP = f, p
		}
	}
	return best, math.Sqrt(total / float64(len(pcm)))
}

// An alert tone goes out on a gateway channel (the warble's two pitches), holds the channel
// against other dispatchers while it plays, and frees it after; on a P25 talkgroup it waits for
// the grant and goes as IMBE.
func TestAlertTones(t *testing.T) {
	gw, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer gw.Close()
	h := newHomeRFSS(t)
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Console: "800",
		P25: config.P25Console{WACN: "BEE07", System: "AD5", RFSS: 2, Unit: 0x1001, SIPPort: freeUDP(t)},
		Channels: []config.RadioChannel{
			{ID: "a", Name: "Fire dispatch", Port: freeUDP(t) &^ 1, Gateway: gw.LocalAddr().String()},
			{ID: "tg", Name: "County fire", Kind: "p25", Port: freeUDP(t) &^ 1, Group: 0x333, Home: "127.0.0.1:" + strconv.Itoa(h.stack.Port()), HomeRFSS: 3},
		}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	disp := registered(t, e, "100")[0]
	if _, code := disp.dial("800"); code != 200 {
		t.Fatalf("console answered %d", code)
	}
	var call string
	for i := 0; i < 40 && call == ""; i++ {
		if ps := e.Console().Positions; len(ps) == 1 {
			call = ps[0].Call
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := e.AlertTone(call, "a", "warble"); err != nil {
		t.Fatal(err)
	}
	if err := e.AlertTone(call, "a", "steady"); err == nil {
		t.Fatal("a second tone started over the first")
	}
	seen := map[float64]int{}
	buf := make([]byte, 2048)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		gw.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := gw.ReadFromUDP(buf)
		if err != nil {
			break // the tone ended
		}
		if f, rms := toneHz(buf[12:n]); rms > 3000 {
			seen[f]++
		}
	}
	if seen[1000] < 20 || seen[1500] < 20 {
		t.Fatalf("warble frequencies at the gateway: %v", seen)
	}
	for i := 0; i < 20 && e.Console().Channels[1].Tone; i++ { // sorted by name: Fire dispatch second
		time.Sleep(50 * time.Millisecond)
	}
	if c := e.Console().Channels; c[1].Tone || c[1].TxBy != "" {
		t.Fatalf("channel still held after the tone: %+v", c[1])
	}

	// On the talkgroup: requested, granted, sent as IMBE the home RFSS decodes as 1000 Hz.
	h.waitPeer(t)
	if err := e.AlertTone(call, "tg", "beep"); err != nil {
		t.Fatal(err)
	}
	var pcm [][]int16
	rx := p25.NewReceiver(0x333, nil, p25.Events{Voice: func(s []int16) { pcm = append(pcm, s) }})
	req, end := false, false
	for !end {
		select {
		case p := <-h.got:
			req = req || p.Packet.Type == p25.PTTRequest
			end = p.Packet.Type == p25.PTTEnd
			rx.Receive(p)
		case <-time.After(3 * time.Second):
			t.Fatalf("request %v, no end of the tone's transmission", req)
		}
	}
	loud := 0
	for _, s := range pcm {
		b := make([]byte, len(s))
		for i, v := range s {
			b[i] = media.LinearToUlaw(v)
		}
		if f, rms := toneHz(b); rms > 1500 && f >= 950 && f <= 1050 {
			loud++
		}
	}
	if !req || loud < 5 {
		t.Fatalf("request %v, %d frames of the 1000 Hz beep at the home RFSS (of %d)", req, loud, len(pcm))
	}
}
