package store

import (
	"testing"
	"time"
)

func TestRadioLog(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	for i, x := range []Transmission{
		{ID: "1", Channel: "a", ChannelName: "Fire dispatch", Direction: "rx", Start: base, Unit: "bee07.ad5.0000c5", Emergency: true},
		{ID: "2", Channel: "a", ChannelName: "Fire dispatch", Direction: "tx", Start: base.Add(time.Minute), Dispatcher: "100", Tone: "warble"},
		{ID: "3", Channel: "b", ChannelName: "Police 1", Direction: "rx", Start: base.Add(2 * time.Minute)},
	} {
		if err := s.AddTransmission(x); err != nil {
			t.Fatal(i, err)
		}
	}
	all, err := s.RadioLog(RadioLogQuery{})
	if err != nil || len(all) != 3 || all[0].ID != "3" {
		t.Fatalf("all %+v %v", all, err)
	}
	if a, _ := s.RadioLog(RadioLogQuery{Channel: "a"}); len(a) != 2 {
		t.Fatalf("channel a: %+v", a)
	}
	if u, _ := s.RadioLog(RadioLogQuery{Search: "0000c5"}); len(u) != 1 || !u[0].Emergency {
		t.Fatalf("by unit: %+v", u)
	}
	if w, _ := s.RadioLog(RadioLogQuery{Since: base.Add(30 * time.Second), Until: base.Add(90 * time.Second)}); len(w) != 1 || w[0].Tone != "warble" {
		t.Fatalf("window: %+v", w)
	}
}
