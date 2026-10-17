package pms

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type events struct {
	mu  sync.Mutex
	got []string
}

func (e *events) add(s string) { e.mu.Lock(); e.got = append(e.got, s); e.mu.Unlock() }
func (e *events) has(s string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range e.got {
		if g == s {
			return true
		}
	}
	return false
}
func (e *events) CheckIn(room, name, account, lang string) {
	e.add("in " + room + " " + name + " " + account + " " + lang)
}
func (e *events) CheckOut(room string)              { e.add("out " + room) }
func (e *events) WakeSet(room string, at time.Time) { e.add("wake " + room + " " + at.Format("15:04")) }
func (e *events) WakeClear(room string)             { e.add("clear " + room) }

func TestRecordRoundTrip(t *testing.T) {
	r := Record{Type: "GI", Fields: map[string]string{"RN": "101", "GN": "Smith|John"}}
	b := r.Encode()
	if b[0] != stx || b[len(b)-1] != etx || string(b[1:len(b)-1]) != "GI|GNSmith/John|RN101|" {
		t.Fatalf("%q", b)
	}
	back, err := Parse(string(b[1 : len(b)-1]))
	if err != nil || back.Fields["RN"] != "101" || back.Fields["GN"] != "Smith/John" {
		t.Fatalf("%+v %v", back, err)
	}
}

// A stand-in hotel system: BitVoice connects, says hello, receives guests and wake-ups, and its
// wake-up results and charges arrive on the link.
func TestLinkWithHotelSystem(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ev := &events{}
	l := &Link{Addr: ln.Addr().String(), Handler: ev, Loc: time.UTC}
	// A charge made while the link is down waits for it.
	l.Charge("101", "0044207946000", 125, 95*time.Second, time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	br := bufio.NewReader(c)
	read := func() Record {
		_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
		if _, err := br.ReadBytes(stx); err != nil {
			t.Fatal(err)
		}
		b, err := br.ReadBytes(etx)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := Parse(string(b[:len(b)-1]))
		return r
	}
	var types []string
	var ps Record
	for range 11 { // LD, 8 LR, LA, then the waiting PS
		r := read()
		types = append(types, r.Type)
		if r.Type == "PS" {
			ps = r
		}
	}
	if types[0] != "LD" || !strings.Contains(strings.Join(types, " "), "LR LR") || ps.Fields["TA"] != "125" || ps.Fields["DU"] != "000135" || ps.Fields["RN"] != "101" {
		t.Fatalf("hello %v, charge %+v", types, ps)
	}
	send := func(r Record) { _, _ = c.Write(r.Encode()) }
	send(Record{Type: "GI", Fields: map[string]string{"RN": "101", "GN": "Smith, John", "G#": "4711", "GL": "SP"}})
	send(Record{Type: "WR", Fields: map[string]string{"RN": "101", "DA": "261015", "TI": "0630"}})
	send(Record{Type: "GC", Fields: map[string]string{"RN": "102", "RO": "101", "GN": "Smith, John", "G#": "4711"}})
	send(Record{Type: "GO", Fields: map[string]string{"RN": "102"}})
	deadline := time.Now().Add(15 * time.Second)
	for _, want := range []string{"in 101 Smith, John 4711 es", "wake 101 06:30", "in 102 Smith, John 4711 ", "out 101", "out 102"} {
		for !ev.has(want) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !ev.has(want) {
			t.Fatalf("missing %q in %v", want, ev.got)
		}
	}
	l.WakeResult("101", time.Date(2026, 10, 15, 6, 30, 0, 0, time.UTC), false)
	if r := read(); r.Type != "WA" || r.Fields["AS"] != "NR" || r.Fields["TI"] != "063000" {
		t.Fatalf("wake answer %+v", r)
	}
	if !l.Status().Up {
		t.Fatal("status should be up")
	}
}
