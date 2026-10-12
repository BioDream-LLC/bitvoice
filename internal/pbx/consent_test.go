package pbx

import (
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

func TestConsentFor(t *testing.T) {
	r := config.Recording{Notice: "beep", Rules: []config.ConsentRule{{Prefix: "1", Name: "US", Action: "off"}, {Prefix: "1415", Name: "California", Action: "announce"}, {Prefix: "49", Name: "Germany", Action: "never"}}}
	for n, want := range map[string]string{"+14155550100": "announce", "+12125550100": "off", "+4930123456": "never", "+447700900123": "beep", "": "beep"} {
		if got, _ := r.ConsentFor(n); got != want {
			t.Errorf("%s: %s, want %s", n, got, want)
		}
	}
}

// With the notice set to announce, both sides hear the recorded notice when the call is answered,
// before they hear each other, and the recording starts after it.
func TestRecordingNotice(t *testing.T) {
	cfg := testConfig()
	cfg.System.Recording.Mode = "all"
	cfg.System.Recording.Notice = "announce"
	st, _ := storetest.Open(t)
	notice := media.ToneSamples(media.Tone{Freqs: []float64{500}, Level: 0.3, On: 800 * time.Millisecond, Repeat: 1})
	if err := st.SetGreeting(noticeBox, media.EncodeWAV(notice)); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg, func(e *Engine) { e.Recordings = st; e.Mail = st })
	ps := registered(t, e, "100", "101")
	a, b := answeredCall(t, ps[0], ps[1], "101")
	// Neither has spoken: what each hears is the notice.
	if !ps[0].gotRTP(time.Second) || !ps[1].gotRTP(time.Second) {
		t.Fatal("the notice was not played to both sides")
	}
	if c := e.Calls(); len(c) != 1 || c[0].Recording != "" {
		t.Fatalf("recording before the notice ended: %+v", c)
	}
	time.Sleep(1200 * time.Millisecond)
	if c := e.Calls(); len(c) != 1 || c[0].Recording != "recording" {
		t.Fatal("not recording after the notice")
	}
	var sa, sb uint16 = 1, 1
	a.speak(10, &sa, 0)
	b.speak(10, &sb, 0)
	ps[0].send(a.request("BYE"))
	if r := rec.nth(t, 1); r.Recording == "" {
		t.Fatal("the call was not recorded")
	}
}
