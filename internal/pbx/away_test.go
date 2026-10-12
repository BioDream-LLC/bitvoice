package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// An extension that is away (a meeting, time off, do not disturb) does not ring: its calls go where
// its calendar settings say, and ring groups ring the others.
func TestAway(t *testing.T) {
	cfg := testConfig()
	cfg.FindExtension("102").Calendar = config.ExtCalendar{WhenBusy: "forward", ForwardTo: "100"}
	cfg.FindExtension("101").Voicemail.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	e.SetAway(Away{Ext: "102", Reason: "Board meeting", Until: time.Now().Add(time.Hour), Source: "calendar"})
	// A call to 102 is forwarded to 100.
	go ps[1].dial("102")
	if ps[0].waitRing(2*time.Second) == nil {
		t.Fatal("the call to 102 was not forwarded to 100")
	}
	if ps[2].waitRing(200*time.Millisecond) != nil {
		t.Fatal("102 rang while away")
	}
	time.Sleep(200 * time.Millisecond)
	// The ring group (101, 102) rings 101 only.
	go ps[0].dial("600")
	if ps[1].waitRing(2*time.Second) == nil {
		t.Fatal("the group did not ring 101")
	}
	if ps[2].waitRing(200*time.Millisecond) != nil {
		t.Fatal("the group rang 102 while away")
	}
	// A manual do not disturb wins over the calendar, and ends when cleared.
	e.SetAway(Away{Ext: "102", Reason: "Do not disturb", Source: "manual"})
	e.SetAway(Away{Ext: "102", Reason: "Lunch", Source: "calendar"})
	if a := e.Aways(); len(a) != 1 || a[0].Reason != "Do not disturb" {
		t.Fatalf("aways %+v", a)
	}
	e.ClearAway("102", "calendar")
	if len(e.Aways()) != 1 {
		t.Fatal("the calendar cleared a manual do not disturb")
	}
	e.ClearAway("102", "manual")
	// An away that has run out is forgotten.
	e.SetAway(Away{Ext: "101", Until: time.Now().Add(-time.Second), Source: "calendar"})
	if len(e.Aways()) != 0 {
		t.Fatalf("aways %+v", e.Aways())
	}
}
