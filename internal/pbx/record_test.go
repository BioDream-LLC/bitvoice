package pbx

import (
	"encoding/binary"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

func TestCallRecording(t *testing.T) {
	cfg := testConfig()
	cfg.System.Recording.Mode = "selected"
	cfg.FindExtension("101").Record = true
	st, _ := storetest.Open(t)
	e, rec := startEngine(t, cfg, func(e *Engine) { e.Recordings = st })
	ps := registered(t, e, "100", "101", "102")
	a, b := answeredCall(t, ps[0], ps[1], "101")
	var sa, sb uint16 = 1, 1
	a.speak(25, &sa, 0)
	b.speak(25, &sb, 0)
	ps[0].send(a.request("BYE"))
	r := rec.nth(t, 1)
	if r.Recording == "" {
		t.Fatal("call to a recorded extension was not recorded")
	}
	wav, err := st.Recording(r.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(wav[22:]) != 2 {
		t.Fatal("not stereo")
	}
	var left, right bool
	for i := 44; i+4 <= len(wav); i += 4 {
		left = left || int16(binary.LittleEndian.Uint16(wav[i:])) != 0
		right = right || int16(binary.LittleEndian.Uint16(wav[i+2:])) != 0
	}
	if !left || !right {
		t.Fatalf("channels: caller %v, called %v", left, right)
	}

	// 100 to 102: neither is marked, nothing recorded.
	a2, _ := answeredCall(t, ps[0], ps[2], "102")
	time.Sleep(50 * time.Millisecond)
	ps[0].send(a2.request("BYE"))
	if r := rec.nth(t, 2); r.Recording != "" {
		t.Fatal("unmarked call recorded")
	}
}

// Pausing for a card payment: *7 from the extension pauses, *8 resumes; the paused stretch is silent in
// the recording and listed in the call record.
func TestRecordingPause(t *testing.T) {
	cfg := testConfig()
	cfg.System.Recording.Mode = "all"
	cfg.System.Recording.PauseCode, cfg.System.Recording.ResumeCode = "*7", "*8"
	st, _ := storetest.Open(t)
	e, rec := startEngine(t, cfg, func(e *Engine) { e.Recordings = st })
	ps := registered(t, e, "100", "101")
	a, b := answeredCall(t, ps[0], ps[1], "101")
	var sa, sb uint16 = 1, 1
	b.speak(10, &sb, 0)
	b.speak(0, &sb, '*')
	b.speak(0, &sb, '7')
	time.Sleep(100 * time.Millisecond)
	if c := e.Calls(); len(c) != 1 || c[0].Recording != "paused" {
		t.Fatalf("not paused: %+v", c)
	}
	a.speak(50, &sa, 0) // the card number: must not be recorded
	b.speak(0, &sb, '*')
	b.speak(0, &sb, '8')
	time.Sleep(100 * time.Millisecond)
	if c := e.Calls(); c[0].Recording != "recording" {
		t.Fatalf("not resumed: %s", c[0].Recording)
	}
	// From the screen too.
	if !e.PauseRecording(e.Calls()[0].ID, true, "admin") || e.Calls()[0].Recording != "paused" {
		t.Fatal("screen pause")
	}
	e.PauseRecording(e.Calls()[0].ID, false, "admin")
	ps[0].send(a.request("BYE"))
	r := rec.nth(t, 1)
	if len(r.Paused) != 2 || r.Recording == "" {
		t.Fatalf("call record %+v", r)
	}
	wav, _ := st.Recording(r.Recording)
	// The caller's channel (left) is silent across the first pause.
	from, to := 44+4*8000*r.Paused[0].From, 44+4*8000*r.Paused[0].To
	for i := from + 4*800; i+4 <= to && i+4 <= len(wav); i += 4 {
		if v := int16(binary.LittleEndian.Uint16(wav[i:])); v != 0 {
			t.Fatalf("audio recorded while paused at byte %d: %d", i, v)
		}
	}
}

// With call clean-up on, steady hiss from one party reaches the other much quieter.
func TestCallAudioCleanup(t *testing.T) {
	for _, on := range []bool{false, true} {
		cfg := testConfig()
		cfg.System.Audio.Calls = on
		e, _ := startEngine(t, cfg)
		ps := registered(t, e, "100", "101")
		a, _ := answeredCall(t, ps[0], ps[1], "101")
		rng := rand.New(rand.NewPCG(3, 4))
		var got []int16
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 2048)
			for {
				_ = ps[1].rtp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, _, err := ps[1].rtp.ReadFromUDP(buf)
				if err != nil {
					return
				}
				if n == 172 && buf[1]&0x7f == 0 {
					for _, b := range buf[12:n] {
						got = append(got, media.UlawToLinear(b))
					}
				}
			}
		}()
		for i := 0; i < 150; i++ { // three seconds of hiss
			pkt := make([]byte, 12, 172)
			pkt[0] = 0x80
			binary.BigEndian.PutUint16(pkt[2:], uint16(i))
			binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
			for j := 0; j < 160; j++ {
				pkt = append(pkt, media.LinearToUlaw(int16(300*rng.NormFloat64())))
			}
			_, _ = ps[0].rtp.WriteToUDPAddrPort(pkt, a.media)
			time.Sleep(10 * time.Millisecond)
		}
		<-done
		if len(got) < 8000*2 {
			t.Fatalf("clean-up %v: only %d samples arrived", on, len(got))
		}
		var sum float64
		tail := got[len(got)-8000:]
		for _, v := range tail {
			sum += float64(v) * float64(v)
		}
		level := math.Sqrt(sum / float64(len(tail)))
		t.Logf("clean-up %v: hiss level %.0f", on, level)
		if on && level > 150 || !on && level < 200 {
			t.Fatalf("clean-up %v: hiss level %.0f", on, level)
		}
	}
}
