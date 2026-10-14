package media

import (
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"
)

// goertzel is the power of one frequency in audio at a rate.
func goertzel(pcm []int16, hz, rate float64) float64 {
	k := 2 * math.Cos(2*math.Pi*hz/rate)
	var s1, s2 float64
	for _, v := range pcm {
		s := float64(v) + k*s1 - s2
		s2, s1 = s1, s
	}
	return (s1*s1 + s2*s2 - k*s1*s2) / float64(len(pcm)*len(pcm))
}

// In an HD bridge, a 6 kHz tone (beyond narrowband's 4 kHz) reaches a wideband party; a narrowband
// party hears the same room at 8 kHz, where the tone cannot be.
func TestWidebandBridge(t *testing.T) {
	pool := &Pool{Host: "127.0.0.1", Min: 31200, Max: 31300}
	eps := make([]*Endpoint, 3)
	phones := make([]*net.UDPConn, 3)
	for i := range eps {
		ep, err := pool.Allocate()
		if err != nil {
			t.Fatal(err)
		}
		defer ep.Close()
		ph, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		defer ph.Close()
		ep.SetRemote("127.0.0.1", ph.LocalAddr().(*net.UDPAddr).Port)
		eps[i], phones[i] = ep, ph
	}
	eps[0].SetPayloadType(PTG722)
	eps[1].SetPayloadType(PTG722)
	eps[2].SetPayloadType(PTPCMU)
	b := NewWideBridge(nil)
	defer b.Close()
	for i, ep := range eps {
		b.Add(string(rune('a'+i)), ep)
		ep.Start()
	}
	// Party a speaks a 6 kHz tone in G.722 for a second.
	go func() {
		enc := NewG722Encoder()
		to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: eps[0].Port()}
		n := 0
		for seq := range 50 {
			pcm := make([]int16, 320)
			for i := range pcm {
				pcm[i] = int16(8000 * math.Sin(2*math.Pi*6000*float64(n)/16000))
				n++
			}
			pkt := make([]byte, 12, 172)
			pkt[0], pkt[1] = 0x80, PTG722
			binary.BigEndian.PutUint16(pkt[2:], uint16(seq))
			binary.BigEndian.PutUint32(pkt[4:], uint32(seq*160))
			pkt = append(pkt, enc.Encode(pcm)...)
			_, _ = phones[0].WriteToUDP(pkt, to)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	hear := func(ph *net.UDPConn, wantPT byte) []int16 {
		dec := NewG722Decoder()
		var out []int16
		buf := make([]byte, 2048)
		_ = ph.SetReadDeadline(time.Now().Add(2 * time.Second))
		for len(out) < 16000/2 {
			n, _, err := ph.ReadFromUDP(buf)
			if err != nil {
				t.Fatalf("heard %d samples: %v", len(out), err)
			}
			if buf[1]&0x7f != wantPT {
				t.Fatalf("payload type %d, want %d", buf[1]&0x7f, wantPT)
			}
			if wantPT == PTG722 {
				out = append(out, dec.Decode(buf[12:n])...)
			} else {
				for _, u := range buf[12:n] {
					out = append(out, UlawToLinear(u))
				}
			}
		}
		return out
	}
	wide := hear(phones[1], PTG722)
	if p := goertzel(wide[len(wide)/2:], 6000, 16000); p < 1e5 {
		t.Fatalf("the wideband party did not hear the 6 kHz tone (power %.0f)", p)
	}
	narrow := hear(phones[2], PTPCMU)
	if p := goertzel(narrow[len(narrow)/2:], 2000, 8000); p > 1e5 { // 6 kHz would alias to 2 kHz unfiltered
		t.Fatalf("the narrowband party heard aliasing (power %.0f)", p)
	}
}
