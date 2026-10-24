package pbx

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// Codecs BitVoice cannot convert pass through untouched when both phones speak them: G.722.1
// (Siren 7, 16 kHz), G.722.1 Annex C (Siren 14, 32 kHz), G.719 (48 kHz) and Codec2. The callee is
// offered the caller's codec with its parameters (bit rate), the caller's answer names it, and the
// packets arrive as they were sent, payload and all.
func TestPassThroughCodecs(t *testing.T) {
	for _, c := range []struct {
		name  string
		codec sdp.Codec
		frame int // bytes per 20 ms
	}{
		{"G7221", sdp.Codec{PT: 102, Name: "G7221", ClockRate: 16000, Fmtp: "bitrate=32000"}, 80},
		{"G7221C", sdp.Codec{PT: 103, Name: "G7221", ClockRate: 32000, Fmtp: "bitrate=48000"}, 120},
		{"G719", sdp.Codec{PT: 104, Name: "G719", ClockRate: 48000, Fmtp: "maxred=0"}, 160},
		{"CODEC2", sdp.Codec{PT: 105, Name: "codec2", ClockRate: 8000, Fmtp: "mode=3200"}, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.System.Codecs = []string{"PCMU", c.name}
			e, _ := startEngine(t, cfg)
			ps := registered(t, e, "100", "101")
			a, b := ps[0], ps[1]
			a.codecs = []sdp.Codec{c.codec}
			b.codecs = []sdp.Codec{c.codec}
			done := make(chan *dialog, 1)
			go func() { d, _ := a.dial("101"); done <- d }()
			ring := b.waitRing(5 * time.Second)
			if ring == nil {
				t.Fatal("101 never rang")
			}
			off, _ := sdp.Parse(ring.Req.Body)
			oc := off.Audio().Codecs()
			if len(oc) == 0 || !strings.EqualFold(oc[0].Name, c.codec.Name) || oc[0].ClockRate != c.codec.ClockRate || oc[0].Fmtp != c.codec.Fmtp {
				t.Fatalf("101 was offered %+v", oc)
			}
			bd := b.answer(ring)
			ad := <-done
			if ad == nil {
				t.Fatal("the caller was not answered")
			}
			ans, _ := sdp.Parse(ad.body)
			if ac := ans.Audio().Codecs(); len(ac) == 0 || ac[0].PT != c.codec.PT || ac[0].ClockRate != c.codec.ClockRate {
				t.Fatalf("the caller's answer %+v", ac)
			}
			// Packets both ways arrive unchanged in their payload and payload type.
			for _, dir := range []struct {
				from, to *phone
				d        *dialog
			}{{b, a, bd}, {a, b, ad}} {
				payload := bytes.Repeat([]byte{0x5a, 0xa5, 0x3c}, c.frame)[:c.frame]
				buf := make([]byte, 2048)
				ok := false
				for i := 0; i < 100 && !ok; i++ {
					pkt := make([]byte, 12, 12+len(payload))
					pkt[0], pkt[1] = 0x80, byte(c.codec.PT)
					binary.BigEndian.PutUint16(pkt[2:], uint16(i))
					binary.BigEndian.PutUint32(pkt[4:], uint32(i*c.codec.ClockRate/50))
					pkt = append(pkt, payload...)
					_, _ = dir.from.rtp.WriteToUDPAddrPort(pkt, dir.d.media)
					_ = dir.to.rtp.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
					if n, _, err := dir.to.rtp.ReadFromUDP(buf); err == nil && n > 12 && int(buf[1]&0x7f) == c.codec.PT {
						if !bytes.Equal(buf[12:n], payload) {
							t.Fatalf("payload changed: %x", buf[12:n])
						}
						ok = true
					}
				}
				if !ok {
					t.Fatalf("no %s packet reached %s", c.name, dir.to.user)
				}
			}
		})
	}
}

// The codec lists tell G.722.1 at 16 kHz (G7221) from its 32 kHz Annex C (G7221C), which SDP names
// alike: a list with one of them passes on only that one.
func TestShapeOfferG7221(t *testing.T) {
	offer := func() *sdp.Media {
		return sdp.New("127.0.0.1", 4000, 1, []sdp.Codec{
			{PT: 103, Name: "G7221", ClockRate: 32000, Fmtp: "bitrate=48000"},
			{PT: 102, Name: "G7221", ClockRate: 16000, Fmtp: "bitrate=32000"},
			{PT: 0, Name: "PCMU", ClockRate: 8000},
		}, 101).Audio()
	}
	for list, want := range map[string][]int{"G7221": {102, 0}, "G7221C": {103, 0}, "": {0}} {
		m := offer()
		allowed := []string{"PCMU"}
		if list != "" {
			allowed = []string{list, "PCMU"}
		}
		shapeOffer(m, allowed)
		var got []int
		for _, c := range m.Codecs() {
			if !isAux(c.Name) {
				got = append(got, c.PT)
			}
		}
		if len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
			t.Errorf("list %q: offered %v, want %v", list, got, want)
		}
	}
}
