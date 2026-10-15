package pbx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func hotelConfig() config.Config {
	c := testConfig()
	c.Extensions[1].GuestRoom = true
	c.System.Hotel.BarVacant, c.System.Hotel.CheckOutClear = true, true
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func TestNextTime(t *testing.T) {
	now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC)
	if at, ok := nextTime("0630", now); !ok || at.Day() != 15 || at.Hour() != 6 {
		t.Fatal(at)
	}
	if at, ok := nextTime("2330", now); !ok || at.Day() != 14 {
		t.Fatal(at)
	}
	for _, bad := range []string{"2460", "063", "ab12"} {
		if _, ok := nextTime(bad, now); ok {
			t.Fatal(bad)
		}
	}
}

func TestGuestRoomsAndWakeUps(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() }) // after the engine stops, before the directory goes (Windows cannot remove an open file)
	e, _ := startEngine(t, hotelConfig(), func(e *Engine) { e.WakeUps = st; e.Mail = st })
	ps := registered(t, e, "100", "101")
	cfg := e.Config()
	room := cfg.FindExtension("101")
	if !e.vacantBarred(cfg, room) {
		t.Fatal("a vacant room should be barred from calling out")
	}
	if p := e.route(cfg, room, "100"); p.reject != 0 {
		t.Fatal("a vacant room should still call extensions")
	}
	if e.CheckIn(Guest{Room: "100", Name: "x"}) == nil {
		t.Fatal("checked a guest in to a room that is not a guest room")
	}
	if err := e.CheckIn(Guest{Room: "101", Name: "Ann Smith", Language: "es"}); err != nil {
		t.Fatal(err)
	}
	if e.vacantBarred(cfg, room) || e.langOf(cfg, "101") != "es" {
		t.Fatal("checked in: may call out, in the guest's language")
	}
	// *62 from the room books 06:30.
	d, code := ps[1].dial("*62")
	if code != 200 {
		t.Fatalf("*62 answered %d", code)
	}
	ps[1].gotRTP(time.Second)
	var seq uint16 = 1
	time.Sleep(1200 * time.Millisecond)
	d.press(&seq, "0630")
	ps[1].waitBye(t, 8*time.Second)
	w, ok := st.WakeUpFor("101")
	if !ok || w.At.In(cfg.System.Location()).Format("1504") != "0630" || w.By != "phone" {
		t.Fatalf("wake-up %+v %v", w, ok)
	}
	// Due now: the room rings and, answered, hears it and is hung up on.
	w.At = time.Now().Add(-time.Second)
	_, _ = st.PutWakeUp(w)
	go e.ringWakeUp(w)
	ring := ps[1].waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("wake-up call did not ring")
	}
	guest := ps[1].answer(ring)
	if !ps[1].gotRTP(2 * time.Second) {
		t.Fatal("nothing played")
	}
	ps[1].send(guest.request("BYE")) // awake: the guest hangs up
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := st.WakeUpFor("101"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an answered one-off wake-up call should be cleared")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Room status from housekeeping, then check-out clears it all but the status.
	e.SetRoomStatus("101", "3")
	_, _ = st.PutWakeUp(store.WakeUp{Extension: "101", At: time.Now().Add(time.Hour)})
	if err := e.CheckOut("101"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.WakeUpFor("101"); ok {
		t.Fatal("check-out should cancel the wake-up call")
	}
	if g, in := e.GuestIn("101"); in || g.Status != "3" {
		t.Fatalf("after check-out %+v %v", g, in)
	}
}

// An unanswered wake-up call is tried again later.
func TestWakeUpRetry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() }) // after the engine stops, before the directory goes (Windows cannot remove an open file)
	e, _ := startEngine(t, hotelConfig(), func(e *Engine) { e.WakeUps = st })
	w, _ := st.PutWakeUp(store.WakeUp{Extension: "102", At: time.Now()}) // no phone registered: no answer
	e.ringWakeUp(w)
	got, ok := st.WakeUpFor("102")
	if !ok || got.Tries != 1 || got.Last != "no answer" || time.Until(got.At) < 4*time.Minute {
		t.Fatalf("retry %+v", got)
	}
}
