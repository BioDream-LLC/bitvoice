package pbx

import (
	"strings"
	"testing"
	"time"
)

// Click-to-dial: extension 100 rings first and, once answered, 101 is called for it.
func TestOriginate(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	type res struct {
		id  string
		err error
	}
	done := make(chan res, 1)
	go func() { id, err := e.Originate("100", "101", 5*time.Second); done <- res{id, err} }()
	ringA := a.waitRing(2 * time.Second)
	if ringA == nil {
		t.Fatal("100 never rang")
	}
	if from := ringA.Req.Get("From"); !strings.Contains(from, "101") {
		t.Fatalf("100 should see the number it will call, got %q", from)
	}
	ad := a.answer(ringA)
	r := <-done
	if r.err != nil || r.id == "" {
		t.Fatalf("originate: %v", r.err)
	}
	ringB := b.waitRing(2 * time.Second)
	if ringB == nil {
		t.Fatal("101 never rang")
	}
	if from := ringB.Req.Get("From"); !strings.Contains(from, "100") {
		t.Fatalf("101 should see 100 calling, got %q", from)
	}
	bd := b.answer(ringB)
	time.Sleep(100 * time.Millisecond)
	a.drainRTP()
	b.drainRTP()
	bd.sendRTP(3)
	if !a.gotRTP(time.Second) {
		t.Fatal("no media reached 100")
	}
	ad.sendRTP(3)
	if !b.gotRTP(time.Second) {
		t.Fatal("no media reached 101")
	}
	calls := e.Calls()
	if len(calls) != 1 || calls[0].ID != r.id || calls[0].State != "answered" || calls[0].From != "100" || calls[0].To != "101" {
		t.Fatalf("calls %+v", calls)
	}
	if !e.Hangup(r.id, "test") {
		t.Fatal("hangup")
	}
	for _, p := range []*phone{a, b} {
		select {
		case <-p.byes:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s got no BYE", p.user)
		}
	}
	if cdr := rec.last(t); cdr.From != "100" || cdr.To != "101" || cdr.Disposition != "answered" {
		t.Fatalf("CDR %+v", cdr)
	}
}

func TestOriginateNoAnswer(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	registered(t, e, "100", "101")
	if _, err := e.Originate("100", "101", 300*time.Millisecond); err != ErrNoAnswer {
		t.Fatalf("got %v", err)
	}
	if _, err := e.Originate("999", "101", time.Second); err == nil {
		t.Fatal("unknown extension accepted")
	}
	if n := len(e.Calls()); n != 0 {
		t.Fatalf("%d calls left", n)
	}
}

// Redirect from the API: 101 leaves and 100 is connected to 102 without sending anyone a NOTIFY.
func TestRedirect(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	b.answer(ring)
	if <-done == nil {
		t.Fatal("not answered")
	}
	id := e.Calls()[0].ID
	if err := e.Redirect(id, "callee", "102"); err != nil {
		t.Fatal(err)
	}
	ringC := c.waitRing(2 * time.Second)
	if ringC == nil {
		t.Fatal("102 never rang")
	}
	c.answer(ringC)
	select {
	case <-b.byes:
	case <-time.After(2 * time.Second):
		t.Fatal("101 was not released")
	}
	select {
	case n := <-b.notifies:
		t.Fatalf("101 got a NOTIFY it never asked for: %s", n)
	default:
	}
	if calls := e.Calls(); len(calls) != 1 || calls[0].To != "102" {
		t.Fatalf("calls %+v", calls)
	}
	if err := e.Redirect("nope", "callee", "102"); err == nil {
		t.Fatal("unknown call accepted")
	}
}
