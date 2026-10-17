package pbx

import (
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A desk phone signs in by dialling *33, the extension and its voicemail PIN; then calls to that
// extension ring the desk, and calls from the desk go out as that extension. *34 signs out.
func TestHotDesk(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[0].Voicemail.PIN = "4321" // 100 signs in at the desk
	cfg.Extensions = append(cfg.Extensions, config.Extension{Number: "900", Name: "Desk 3", Secret: testSecret, Enabled: true, Outbound: true, RingSeconds: 5, HotDesk: true})
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var saved []HotDesk
	e, rec := startEngine(t, cfg, func(e *Engine) {
		e.SaveHotDesks = func(h []HotDesk) error { mu.Lock(); saved = h; mu.Unlock(); return nil }
	})
	savedN := func() int { mu.Lock(); defer mu.Unlock(); return len(saved) }
	ps := registered(t, e, "900", "101")
	desk, other := ps[0], ps[1]

	signIn := func(keys string) {
		t.Helper()
		d, code := desk.dial("*33")
		if code != 200 {
			t.Fatalf("*33 answered %d", code)
		}
		var seq uint16 = 1
		prompted(t, desk)
		d.press(&seq, keys)
		desk.waitBye(t, 12*time.Second)
	}
	signIn("100#1111#") // the wrong PIN
	if len(e.HotDesks()) != 0 {
		t.Fatal("signed in with the wrong PIN")
	}
	signIn("100#4321#")
	if hs := e.HotDesks(); len(hs) != 1 || hs[0].User != "100" || hs[0].Desk != "900" || savedN() != 1 {
		t.Fatalf("hot desks %+v saved %+v", hs, saved)
	}
	// A call to 100 rings the desk.
	go other.dial("100")
	if desk.waitRing(3*time.Second) == nil {
		t.Fatal("a call to 100 did not ring the desk it signed in at")
	}
	time.Sleep(300 * time.Millisecond)
	// A call from the desk is 100's.
	d, code := desk.dial(cfg.System.EchoNumber)
	if code != 200 {
		t.Fatalf("echo from the desk: %d", code)
	}
	desk.send(d.request(sip.BYE))
	if r := rec.find(t, "no record of the desk's echo call", func(r store.CDR) bool { return r.To == cfg.System.EchoNumber }); r.From != "100" || r.Ext != "100" {
		t.Fatalf("the desk's call was not 100's: %+v", r)
	}
	// Signing out by code.
	d, code = desk.dial("*34")
	if code != 200 {
		t.Fatalf("*34 answered %d", code)
	}
	desk.waitBye(t, 5*time.Second)
	if len(e.HotDesks()) != 0 {
		t.Fatal("still signed in after *34")
	}
	// The administrator's sign-in, the nightly sign-out and the lockout.
	if _, err := e.SignIn("900", "101", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SignIn("101", "100", "", true); err != ErrNotHotDesk {
		t.Fatalf("101 is not a desk: %v", err)
	}
	c2 := *e.Config()
	c2.System.HotDesk.SignOutAt = "00:00"
	e.Apply(c2)
	e.signOutNightly(time.Now())
	if len(e.HotDesks()) != 0 {
		t.Fatal("the nightly sign-out did not run")
	}
	for range 5 {
		e.SignIn("900", "100", "0000", false)
	}
	if _, err := e.SignIn("900", "100", "4321", false); err != ErrLocked {
		t.Fatalf("no lockout after five wrong PINs: %v", err)
	}
	// Unmarking the desk signs its user out.
	if _, err := e.SignIn("900", "102", "", true); err != nil {
		t.Fatal(err)
	}
	c3 := *e.Config()
	for i := range c3.Extensions {
		if c3.Extensions[i].Number == "900" {
			c3.Extensions[i].HotDesk = false
		}
	}
	e.Apply(c3)
	if len(e.HotDesks()) != 0 {
		t.Fatal("still signed in at a desk that is no longer one")
	}
}
