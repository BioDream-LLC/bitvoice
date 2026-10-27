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
	last chan struct{} // closed once the previous line signal is delivered
}

// SetBits delivers line signals in the order they were set, as timeslot 16 does: a later signal never
// overtakes an earlier one (which once made a stale clear-back read as the next call's seize-ack).
func (w *wire) SetBits(b int) {
	w.mu.Lock()
	p, prev, done := w.peer, w.last, make(chan struct{})
	w.last = done
	w.mu.Unlock()
	if p == nil {
		close(done)
		return
	}
	go func() {
		defer close(done)
		time.Sleep(2 * time.Millisecond)
		if prev != nil {
			<-prev
		}
		p.Bits(b)
	}()
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
	for _, code := range []string{"itu", "mx", "br", "cn"} {
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

// tapped is a wire that also writes down every signal sent on it (tones by number, F for forward
// and A or B for backward as the group it is heard in).
type tapped struct {
	wire
	mu   *sync.Mutex
	tape *[]int
}

func (w *tapped) SetTone(n int) {
	if n != 0 {
		w.mu.Lock()
		*w.tape = append(*w.tape, n)
		w.mu.Unlock()
	}
	w.wire.SetTone(n)
}

// China No. 1 as Cisco's VCO/4K China 3 Supplement shows it (chapter 4, examples 1 and 3): each digit
// of the number dialled asked by A-1 and ended by I-15; A-6 for KA (1, ordinary); the caller's
// number digit by digit on A-1, ended by I-15; A-3 for KD (3, a local call); B-1 (idle). The C and
// D bits are both 1. A busy local line is KB-4 (with KD 3), which the caller hears as busy.
func TestChinaAsDocumented(t *testing.T) {
	v := Variants["cn"]
	var mu sync.Mutex
	var tape []int
	var bits []int
	wa, wb := &tapped{mu: &mu, tape: &tape}, &tapped{mu: &mu, tape: &tape}
	ra, rb := newRec(), newRec()
	var a, b *Chan
	a = New(v, &bitTap{wa, &mu, &bits}, ra.events(&a, nil))
	b = New(v, wb, rb.events(&b, func(c *Chan) { c.Accept(true); time.AfterFunc(100*time.Millisecond, c.Answer) }))
	wa.peer, wb.peer = b, a
	t.Cleanup(func() { a.Close(); b.Close() })
	a.Start()
	b.Start()
	time.Sleep(20 * time.Millisecond)
	if err := a.Dial("3125550199", "9677958", CategoryNational, false); err != nil {
		t.Fatal(err)
	}
	rb.want(t, "offered")
	ra.want(t, "accepted")
	ra.want(t, "answered")
	want := []int{}
	for _, d := range "9677958" {
		want = append(want, signalOf(byte(d)), 1) // a digit, A-1
	}
	want = append(want, 15, 6, 1, 1) // I-15, A-6, KA 1, A-1
	for _, d := range "3125550199" {
		want = append(want, signalOf(byte(d)), 1)
	}
	want = append(want, 15, 3, 3, 1) // I-15, A-3, KD 3, B-1
	mu.Lock()
	got := append([]int(nil), tape...)
	sent := append([]int(nil), bits...)
	mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("signals\n got  %v\n want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal %d is %d, want %d\n got  %v\n want %v", i, got[i], want[i], got, want)
		}
	}
	for _, b := range sent {
		if b&0x3 != 0x3 {
			t.Fatalf("C and D bits %02b, want 11 (China)", b&0x3)
		}
	}
	rb.mu.Lock()
	ani, dnis, cat := rb.ani, rb.dnis, rb.cat
	rb.mu.Unlock()
	if ani != "3125550199" || dnis != "9677958" || cat != CategoryNational {
		t.Fatalf("offered ANI %q DNIS %q category %d", ani, dnis, cat)
	}
	a.Hangup()
	rb.want(t, "disconnected normal clearing")
	ra.want(t, "ended")
	rb.want(t, "ended") // both ends idle before the next call

	// Busy on a local call is KB-4; vacant is KB-5 whatever the KD.
	for _, c := range []struct {
		cause Cause
		kb    int
		heard string
	}{{Busy, 4, "disconnected busy"}, {Unallocated, 5, "disconnected unallocated number"}} {
		mu.Lock()
		tape = nil
		mu.Unlock()
		b.ev.Offered = rb.events(&b, func(ch *Chan) { ch.Reject(c.cause) }).Offered
		if err := a.Dial("3125550199", "9677958", CategoryNational, false); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if t.Failed() {
					mu.Lock()
					t.Logf("tape %v", tape)
					mu.Unlock()
				}
			}()
			ra.want(t, c.heard)
		}()
		ra.want(t, "ended")
		rb.want(t, "ended")
		mu.Lock()
		last := tape[len(tape)-1]
		mu.Unlock()
		if last != c.kb {
			t.Fatalf("%v sent as KB-%d, want KB-%d", c.cause, last, c.kb)
		}
	}
}

// bitTap writes down the line bits a channel sets.
type bitTap struct {
	*tapped
	mu   *sync.Mutex
	bits *[]int
}

func (w *bitTap) SetBits(b int) {
	w.mu.Lock()
	*w.bits = append(*w.bits, b)
	w.mu.Unlock()
	w.tapped.SetBits(b)
}
