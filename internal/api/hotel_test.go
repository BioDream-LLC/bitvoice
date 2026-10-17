package api

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/pms"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestHotelSystemLink(t *testing.T) {
	s, ts := newServer(t)
	s.Log = slog.New(slog.DiscardHandler)
	s.Engine.WakeUps = s.Store
	s.Engine.PMSEvent = s.pmsEvent
	s.Engine.SaveGuests = func(g []pbx.Guest) error { return s.Store.SaveState("guests", g) }
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// Room 100 is a guest room; the hotel system is FIAS on our stand-in, with a 50% markup.
	_, list := admin.do("GET", "/api/extensions", nil)
	x := list["items"].([]any)[0].(map[string]any)
	if x["number"] != "100" {
		t.Fatalf("first extension %v", x["number"])
	}
	x["guestRoom"] = true
	if code, out := admin.do("PUT", "/api/extensions/"+x["id"].(string), x); code != 200 {
		t.Fatalf("guest room %d %v", code, out)
	}
	_, cfg := admin.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	pmsBad := map[string]any{"kind": "fias", "host": "", "port": 70000}
	sys["hotel"] = map[string]any{"pms": pmsBad}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 422 || !hasProblem(out, "system.hotel.pms.host") || !hasProblem(out, "system.hotel.pms.port") {
		t.Fatalf("bad PMS %d %v", code, out)
	}
	n, _ := strconv.Atoi(port)
	sys["hotel"] = map[string]any{"barVacant": true, "checkOutClear": true,
		"pms": map[string]any{"kind": "fias", "host": "127.0.0.1", "port": n, "chargeCalls": true, "markup": 50}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("settings %d %v", code, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunHotel(ctx)
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	br := bufio.NewReader(c)
	next := func(typ string) pms.Record {
		for {
			_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
			if _, err := br.ReadBytes(0x02); err != nil {
				t.Fatalf("waiting for %s: %v", typ, err)
			}
			b, _ := br.ReadBytes(0x03)
			if r, _ := pms.Parse(string(b[:len(b)-1])); r.Type == typ {
				return r
			}
		}
	}
	next("LA")
	_, _ = c.Write(pms.Record{Type: "GI", Fields: map[string]string{"RN": "100", "GN": "Smith, Ann", "G#": "4711", "GL": "FR"}}.Encode())
	deadline := time.Now().Add(15 * time.Second)
	for {
		if g, in := s.Engine.GuestIn("100"); in && g.Name == "Smith, Ann" && g.Language == "fr" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("check-in from the hotel system not applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var kept []pbx.Guest
	if err := s.Store.LoadState("guests", &kept); err != nil || len(kept) != 1 {
		t.Fatalf("guests not kept across restarts: %v %v", err, kept)
	}
	_, view := admin.do("GET", "/api/hotel", nil)
	if !strings.Contains(asJSON(view), `"name":"Smith, Ann"`) || !strings.Contains(asJSON(view), `"up":true`) {
		t.Fatalf("hotel view %v", view)
	}
	// A call charged at cost 1.00 plus 50%: 150 cents on the guest's bill.
	s.Bus.Publish("call.logged", store.CDR{ID: "c1", Direction: "outbound", Ext: "100", To: "0044207946000", Billsec: 61, Cost: 1, Start: time.Now()})
	if r := next("PS"); r.Fields["TA"] != "150" || r.Fields["RN"] != "100" || r.Fields["DU"] != "000101" {
		t.Fatalf("charge %+v", r)
	}
	// A wake-up booked on the screen goes to the hotel system; housekeeping's status too.
	at := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	if code, _ := admin.do("PUT", "/api/wakeups/100", map[string]any{"at": at}); code != 200 {
		t.Fatal("wake-up")
	}
	if r := next("WR"); r.Fields["RN"] != "100" {
		t.Fatalf("wake request %+v", r)
	}
	if code, _ := admin.do("PUT", "/api/hotel/rooms/100/status", map[string]any{"status": "2"}); code != 204 {
		t.Fatal("status")
	}
	if r := next("RE"); r.Fields["RS"] != "2" {
		t.Fatalf("room status %+v", r)
	}
	// Check-out from the hotel system cancels the wake-up call.
	_, _ = c.Write(pms.Record{Type: "GO", Fields: map[string]string{"RN": "100"}}.Encode())
	deadline = time.Now().Add(15 * time.Second)
	for {
		_, in := s.Engine.GuestIn("100")
		_, wake := s.Store.WakeUpFor("100")
		if !in && !wake {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("check-out not applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _ := admin.do("POST", "/api/hotel/rooms/101/checkin", map[string]any{"name": "x"}); code != 404 {
		t.Fatal("checked in to a room that is not a guest room")
	}
}
