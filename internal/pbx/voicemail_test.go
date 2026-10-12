package pbx

import (
	"encoding/binary"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// speak sends n packets of loud audio in real time, then the key, as RFC 4733 events.
func (d *dialog) speak(n int, seq *uint16, key byte) {
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = media.LinearToUlaw(int16(8000 * (1 - 2*(i/20%2))))
	}
	evTS := uint32(0)
	send := func(pt byte, body []byte) {
		pkt := make([]byte, 12, 12+len(body))
		pkt[0], pkt[1] = 0x80, pt
		binary.BigEndian.PutUint16(pkt[2:], *seq)
		ts := uint32(*seq) * 160
		if pt == 101 {
			ts = evTS // RFC 4733: an event's packets, end repeats included, share one timestamp
		}
		binary.BigEndian.PutUint32(pkt[4:], ts)
		*seq++
		_, _ = d.p.rtp.WriteToUDPAddrPort(append(pkt, body...), d.media)
	}
	for range n {
		send(0, payload)
		time.Sleep(20 * time.Millisecond)
	}
	if key != 0 {
		ev := byte(key - '0')
		switch key {
		case '*':
			ev = 10
		case '#':
			ev = 11
		}
		evTS = uint32(*seq) * 160
		for range 3 {
			send(101, []byte{ev, 0x80 | 10, 0, 160})
		}
	}
}

func (p *phone) waitBye(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-p.byes:
	case <-time.After(within):
		t.Fatal("BitVoice did not hang up")
	}
}

func (p *phone) subscribeMWI(t *testing.T) {
	t.Helper()
	m := &sip.Message{IsRequest: true, Method: sip.SUBSCRIBE, RequestURI: "sip:" + p.user + "@127.0.0.1"}
	m.Set("From", "<sip:"+p.user+"@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:"+p.user+"@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 SUBSCRIBE")
	m.Set("Contact", p.contact())
	m.Set("Event", "message-summary")
	m.Set("Expires", "600")
	if r := p.send(m); r == nil || r.StatusCode != 200 {
		t.Fatalf("subscribe: %v", r)
	}
}

func (p *phone) waitNotify(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-p.notifies:
			if strings.Contains(b, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no NOTIFY containing %q", want)
		}
	}
}

func TestVoicemailLeaveAndCollect(t *testing.T) {
	cfg := testConfig()
	x := cfg.FindExtension("101")
	x.Voicemail.Enabled = true
	x.NoAnswer = &config.Destination{Type: "voicemail", Target: "101"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := storetest.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg, func(e *Engine) { e.Mail = st })
	caller := registered(t, e, "100")[0]

	// 101 has no phone connected, so the call goes straight to its mailbox.
	d, code := caller.dial("101")
	if code != 200 {
		t.Fatalf("voicemail answered %d", code)
	}
	if !caller.gotRTP(2 * time.Second) {
		t.Fatal("no greeting or beep")
	}
	time.Sleep(1200 * time.Millisecond) // greeting quiet and beep
	var seq uint16 = 1
	d.speak(100, &seq, '#') // two seconds, then # to finish
	caller.waitBye(t, 3*time.Second)

	msgs, _ := st.Voicemails("101")
	if len(msgs) != 1 || msgs[0].From != "100" || msgs[0].Seconds < 1 || msgs[0].Seconds > 3 {
		t.Fatalf("messages: %+v", msgs)
	}
	if r := rec.last(t); r.AnsweredBy != "Voicemail Ext 101" || r.Disposition != "answered" {
		t.Fatalf("record: %+v", r)
	}
	wav, _ := st.VoicemailAudio("101", msgs[0].ID)
	if pcm, err := media.DecodeWAV(wav); err != nil || len(pcm) < media.Rate {
		t.Fatalf("audio: %v, %d samples", err, len(pcm))
	}

	// The owner's phone subscribes and its lamp lights; hearing the message turns it off.
	owner := registered(t, e, "101")[0]
	owner.subscribeMWI(t)
	owner.waitNotify(t, "Voice-Message: 1/0")
	d2, code := owner.dial("*97")
	if code != 200 {
		t.Fatalf("*97 answered %d", code)
	}
	_ = d2
	owner.waitBye(t, 8*time.Second)
	owner.waitNotify(t, "Messages-Waiting: no")
	if f, o := st.VoicemailCounts("101"); f != 0 || o != 1 {
		t.Fatalf("counts %d new %d old", f, o)
	}

	// An extension without voicemail cannot dial into one.
	if _, code := caller.dial("*97"); code != 404 {
		t.Fatalf("*97 without a mailbox: %d", code)
	}
}

// A caller who hangs up at the beep leaves nothing.
func TestVoicemailHangupAtBeep(t *testing.T) {
	cfg := testConfig()
	x := cfg.FindExtension("101")
	x.Voicemail.Enabled = true
	x.NoAnswer = &config.Destination{Type: "voicemail", Target: "101"}
	st, _ := storetest.Open(t)
	e, _ := startEngine(t, cfg, func(e *Engine) { e.Mail = st })
	caller := registered(t, e, "100")[0]
	d, code := caller.dial("101")
	if code != 200 {
		t.Fatal(code)
	}
	time.Sleep(1200 * time.Millisecond)
	caller.send(d.request("BYE"))
	time.Sleep(300 * time.Millisecond)
	if msgs, _ := st.Voicemails("101"); len(msgs) != 0 {
		t.Fatalf("empty message kept: %+v", msgs)
	}
}

func (p *phone) subscribeDialog(t *testing.T, ext string) {
	t.Helper()
	m := &sip.Message{IsRequest: true, Method: sip.SUBSCRIBE, RequestURI: "sip:" + ext + "@127.0.0.1"}
	m.Set("From", "<sip:"+p.user+"@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:"+ext+"@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 SUBSCRIBE")
	m.Set("Contact", p.contact())
	m.Set("Event", "dialog")
	m.Set("Accept", "application/dialog-info+xml")
	m.Set("Expires", "600")
	if r := p.send(m); r == nil || r.StatusCode != 200 {
		t.Fatalf("subscribe dialog: %v", r)
	}
}

func TestBusyLamp(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	watcher := ps[2]
	watcher.subscribeDialog(t, "101")
	watcher.waitNotify(t, `entity="sip:101@`)
	a, b := answeredCall(t, ps[0], ps[1], "101")
	_ = b
	watcher.waitNotify(t, "<state>confirmed</state>")
	ps[0].send(a.request("BYE"))
	deadline := time.After(3 * time.Second)
	for {
		select {
		case body := <-watcher.notifies:
			if !strings.Contains(body, "<dialog ") {
				return // idle again
			}
		case <-deadline:
			t.Fatal("lamp did not go idle")
		}
	}
}
