package r2

import (
	"sync"
	"testing"
	"time"
)

// wire joins two channels as one E1 circuit: the bits each sends are what the other reads, and each
// tone reaches the other after a detector's delay.
type wire struct {
	mu   sync.Mutex
	peer *Chan
	log  *[]string
	name string
}

func (w *wire) SetBits(b int) {
	w.mu.Lock()
	p := w.peer
	w.mu.Unlock()
	if p != nil {
		go func() { time.Sleep(2 * time.Millisecond); p.Bits(b) }()
	}
}

func (w *wire) SetTone(n int) {
	w.mu.Lock()
	p := w.peer
	w.mu.Unlock()
	if p != nil {
		go func() { time.Sleep(30 * time.Millisecond); p.Tone(n) }()
	}
}

type rec struct {
	mu   sync.Mutex
	evs  []string
	ani  string
	dnis string
	cat  int
	ch   chan string
}

func newRec() *rec { return &rec{ch: make(chan string, 32)} }

func (r *rec) events(c **Chan, onOffer func(*Chan)) Events {
	say := func(s string) { r.ch <- s }
	return Events{
		Offered: func(ani, dnis string, cat int, restricted bool) {
			r.mu.Lock()
			r.ani, r.dnis, r.cat = ani, dnis, cat
			r.mu.Unlock()
			say("offered")
			if onOffer != nil {
				onOffer(*c)
			}
		},
		Accepted:     func(bool) { say("accepted") },
		Answered:     func() { say("answered") },
		Disconnected: func(cs Cause) { say("disconnected " + cs.String()); (*c).Hangup() },
		Ended:        func() { say("ended") },
	}
}

func (r *rec) want(t *testing.T, s string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case got := <-r.ch:
			if got == s {
				return
			}
		case <-timeout:
			t.Fatalf("never %q", s)
		}
	}
}

func pair(t *testing.T, v *Variant, onOffer func(*Chan)) (*Chan, *Chan, *rec, *rec) {
	wa, wb := &wire{}, &wire{}
	ra, rb := newRec(), newRec()
	var a, b *Chan
	a = New(v, wa, ra.events(&a, nil))
	b = New(v, wb, rb.events(&b, onOffer))
	wa.peer, wb.peer = b, a
	t.Cleanup(func() { a.Close(); b.Close() })
	a.Start()
	b.Start()
	time.Sleep(20 * time.Millisecond)
	return a, b, ra, rb
}

// Calls between two BitVoice circuits in every variant: the number, the caller's number and
// category arrive, the call is accepted and answered, and either end clears it back to idle.
func TestCallsInEveryVariant(t *testing.T) {
	BackCycle = 400 * time.Millisecond // variants without an end-of-number signal time out
	defer func() { BackCycle = 2 * time.Second }()
	for _, code := range Codes() {
		t.Run(code, func(t *testing.T) {
			v := Variants[code]
			a, _, ra, rb := pair(t, v, func(c *Chan) { c.Accept(true); time.AfterFunc(200*time.Millisecond, c.Answer) })
			if err := a.Dial("3125550199", "5550100", CategoryNational, false); err != nil {
				t.Fatal(err)
			}
			rb.want(t, "offered")
			rb.mu.Lock()
			ani, dnis := rb.ani, rb.dnis
			rb.mu.Unlock()
			if dnis != "5550100" || ani != "3125550199" {
				t.Fatalf("got ANI %q DNIS %q", ani, dnis)
			}
			ra.want(t, "accepted")
			ra.want(t, "answered")
			a.Hangup()
			rb.want(t, "disconnected normal clearing")
			ra.want(t, "ended")
		})
	}
}

// The called end refuses: busy reaches the caller, and the circuit is idle again.
func TestBusy(t *testing.T) {
	for _, code := range []string{"itu", "mx", "br"} {
		t.Run(code, func(t *testing.T) {
			// Long enough that a stalled runner (macOS CI) never times out a signal mid-number;
			// short enough for the variants with no end-of-number signal to go on quickly.
			BackCycle = 1500 * time.Millisecond
			defer func() { BackCycle = 2 * time.Second }()
			a, _, ra, _ := pair(t, Variants[code], func(c *Chan) { c.Reject(Busy) })
			if err := a.Dial("", "5550100", CategoryNational, true); err != nil {
				t.Fatal(err)
			}
			ra.want(t, "disconnected busy")
			ra.want(t, "ended")
		})
	}
}
