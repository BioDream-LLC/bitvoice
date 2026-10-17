package pbx

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// loudness is the average level of the PCMU audio a phone receives over a while.
func loudness(p *phone, d time.Duration) float64 {
	end := time.Now().Add(d)
	var sum float64
	n := 0
	buf := make([]byte, 2048)
	for time.Now().Before(end) {
		_ = p.rtp.SetReadDeadline(end)
		k, _, err := p.rtp.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if k == 172 && buf[1]&0x7f == 0 {
			for _, b := range buf[12:k] {
				sum += math.Abs(float64(media.UlawToLinear(b)))
				n++
			}
		}
	}
	if n == 0 {
		return -1
	}
	return sum / float64(n)
}

// speakFor sends a loud tone from a phone for a while.
func speakFor(p *phone, to *dialog, d time.Duration) {
	end := time.Now().Add(d)
	for i := 0; time.Now().Before(end); i++ {
		pkt := make([]byte, 12, 172)
		pkt[0] = 0x80
		binary.BigEndian.PutUint16(pkt[2:], uint16(i))
		binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
		for j := 0; j < 160; j++ {
			pkt = append(pkt, media.LinearToUlaw(int16(12000*math.Sin(2*math.Pi*500*float64(j)/8000))))
		}
		_, _ = p.rtp.WriteToUDPAddrPort(pkt, to.media)
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSupervisorListenWhisperBarge(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	caller, agent, sup := ps[0], ps[1], ps[2]
	cd, ad := answeredCall(t, caller, agent, "101")
	id := e.Calls()[0].ID
	if err := e.Supervise(id, "102", "listen"); err != nil {
		t.Fatal(err)
	}
	ring := sup.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("supervisor not rung")
	}
	sd := sup.answer(ring)
	waitFor(t, "the supervisor's call is not shown", func() bool { return len(e.Calls()) == 2 })
	// Listen: the supervisor hears the caller; the agent does not hear the supervisor.
	go speakFor(caller, cd, 600*time.Millisecond)
	if l := loudness(sup, 600*time.Millisecond); l < 1000 {
		t.Fatalf("supervisor hears the caller at %.0f", l)
	}
	agent.drainRTP()
	go speakFor(sup, sd, 600*time.Millisecond)
	if l := loudness(agent, 600*time.Millisecond); l > 200 {
		t.Fatalf("agent hears a listening supervisor at %.0f", l)
	}
	// Whisper: the agent hears the supervisor, the caller does not.
	if err := e.SetSupervision(id, "whisper"); err != nil {
		t.Fatal(err)
	}
	caller.drainRTP()
	agent.drainRTP()
	go speakFor(sup, sd, 600*time.Millisecond)
	done := make(chan float64, 1)
	go func() { done <- loudness(caller, 600*time.Millisecond) }()
	if l := loudness(agent, 600*time.Millisecond); l < 1000 {
		t.Fatalf("agent does not hear the whisper (%.0f)", l)
	}
	if l := <-done; l > 200 {
		t.Fatalf("caller hears the whisper at %.0f", l)
	}
	// The supervisor leaves: caller and agent are joined directly again.
	sup.send(sd.request("BYE"))
	waitFor(t, "caller and agent not joined again", func() bool { c := e.Calls(); return len(c) == 1 && c[0].Supervision == "" })
	agent.drainRTP()
	cd.sendRTP(5)
	if !agent.gotRTP(time.Second) {
		t.Fatal("caller and agent no longer connected")
	}
	_ = ad
}
