package pbx

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

func TestParkAndPickup(t *testing.T) {
	cfg := testConfig()
	// Long enough that a slow machine never rings the parker back before the pickup (ring-back has
	// its own test).
	cfg.System.ParkSeconds = 30
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	_, bd := answeredCall(t, a, b, "101")

	// 101 transfers 100 to the park number; 101 is told the slot and released.
	refer := bd.request(sip.REFER)
	refer.Set("Refer-To", "<sip:70@127.0.0.1>")
	if r := b.send(refer); r == nil || r.StatusCode != 202 {
		t.Fatalf("REFER: %v", r)
	}
	select {
	case m := <-b.messages:
		if !strings.Contains(m, "parked on 71") {
			t.Fatalf("message %q", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parker not told the slot")
	}
	b.waitBye(t, 2*time.Second)
	if p := e.Parked(); len(p) != 1 || p[0].Slot != "71" {
		t.Fatalf("parked %+v", p)
	}
	a.drainRTP()
	if !a.gotRTP(time.Second) {
		t.Fatal("parked caller hears nothing")
	}

	// 102 dials 71 and talks to 100.
	cd, code := c.dial("71")
	if code != 200 {
		t.Fatalf("pickup %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	a.drainRTP()
	cd.sendRTP(5)
	if !a.gotRTP(time.Second) {
		t.Fatal("no media after pickup")
	}
	if len(e.Parked()) != 0 || len(e.Calls()) != 1 {
		t.Fatalf("after pickup: parked %v calls %d", e.Parked(), len(e.Calls()))
	}
	if _, code := c.dial("72"); code != 404 {
		t.Fatalf("empty slot: %d", code)
	}
}

// Left parked, the call rings whoever parked it.
func TestParkRingsBack(t *testing.T) {
	cfg := testConfig()
	cfg.System.ParkSeconds = 1
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	_, bd := answeredCall(t, a, b, "101")
	refer := bd.request(sip.REFER)
	refer.Set("Refer-To", "<sip:70@127.0.0.1>")
	b.send(refer)
	b.waitBye(t, 2*time.Second)
	if b.waitRing(4*time.Second) == nil {
		t.Fatal("the parker was not rung back")
	}
}
