package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func menuConfig() config.Config {
	c := testConfig()
	c.Menus = []config.Menu{{ID: "main", Number: "800", Name: "Main menu", DialExtensions: true, TimeoutSeconds: 1, Repeats: 1,
		Options: []config.MenuOption{{Key: "9", Label: "Sales", Destination: config.Destination{Type: "ringgroup", Target: "600"}}}}}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func (d *dialog) press(seq *uint16, keys string) {
	for i := range len(keys) {
		d.speak(0, seq, keys[i])
		time.Sleep(60 * time.Millisecond)
	}
}

func TestMenuKeyRingsDestination(t *testing.T) {
	e, rec := startEngine(t, menuConfig())
	ps := registered(t, e, "100", "101", "102")
	d, code := ps[0].dial("800")
	if code != 200 {
		t.Fatalf("menu answered %d", code)
	}
	if !ps[0].gotRTP(time.Second) {
		t.Fatal("no prompt")
	}
	var seq uint16 = 1
	d.press(&seq, "9")
	ring := ps[1].waitRing(3 * time.Second) // 600 rings 101 and 102
	if ring == nil {
		t.Fatal("sales not rung after pressing 9")
	}
	agent := ps[1].answer(ring)
	time.Sleep(100 * time.Millisecond)
	ps[0].drainRTP()
	agent.sendRTP(5)
	if !ps[0].gotRTP(time.Second) {
		t.Fatal("caller does not hear the person who answered")
	}
	ps[0].send(d.request("BYE"))
	if r := rec.last(t); r.Disposition != "answered" {
		t.Fatalf("%+v", r)
	}
}

func TestMenuDialByExtensionAndTimeout(t *testing.T) {
	e, _ := startEngine(t, menuConfig())
	ps := registered(t, e, "100", "102")
	d, _ := ps[0].dial("800")
	ps[0].gotRTP(time.Second)
	var seq uint16 = 1
	d.press(&seq, "102")
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("dialling 102 at the menu did not ring it")
	}
	ps[0].send(d.request("BYE"))

	// Nobody presses anything: one prompt, a second of waiting, then goodbye.
	if _, code := ps[0].dial("800"); code != 200 {
		t.Fatal(code)
	}
	ps[0].waitBye(t, 5*time.Second)
}
