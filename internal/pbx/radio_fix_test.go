package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Radio positions: kept per radio with its name, a late older report changes nothing, bad ones are
// refused, and each goes out as radio.location.
func TestRadioLocations(t *testing.T) {
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Aliases: []config.UnitAlias{{Unit: 1205, Name: "Engine 5"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	sub := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(sub)
	now := time.Now().UTC().Truncate(time.Second)
	f, err := e.RadioLocation(RadioFix{Unit: 1205, Lat: 40.2633, Lon: -76.8823, Speed: 42, Heading: 90, At: now})
	if err != nil || f.Alias != "Engine 5" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := e.RadioLocation(RadioFix{Unit: 1205, Lat: 40.1, Lon: -76.1, At: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if l := e.RadioLocations(); len(l) != 1 || l[0].Lat != 40.2633 {
		t.Fatalf("a late older report replaced the newer one: %+v", l)
	}
	for _, bad := range []RadioFix{{Unit: 0, Lat: 1, Lon: 1}, {Unit: 5, Lat: 91, Lon: 1}, {Unit: 5}, {Unit: 5, Lat: 1, Lon: 1, Heading: 360}} {
		if _, err := e.RadioLocation(bad); err == nil {
			t.Fatalf("took %+v", bad)
		}
	}
	for {
		select {
		case ev := <-sub.C:
			if ev.Type == "radio.location" {
				if x := ev.Data.(RadioFix); x.Unit != 1205 || x.Alias != "Engine 5" {
					t.Fatalf("event %+v", x)
				}
				return
			}
		case <-time.After(15 * time.Second):
			t.Fatal("no radio.location event")
		}
	}
}
