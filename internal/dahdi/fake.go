package dahdi

import (
	"errors"
	"sync"
	"time"
)

// Fake is a channel in memory, standing for a card's port in tests: what is "heard" on the port is
// given with Speak, events with Raise, and what the bridge does to it is recorded.
type Fake struct {
	mu     sync.Mutex
	events []int
	in     [][]byte
	hooks  []int
	tones  []int
	wrote  int
	sent   []byte // everything written, for tests that look at the audio
	closed bool
	wake   chan struct{}
	law    int
	rxBits int
	txBits []int
	peer   *Fake
}

// Patch joins two fakes back to back, as two circuits of one E1 looped: what one writes the other
// hears, and the bits one sets the other reads.
func Patch(a, b *Fake) {
	a.mu.Lock()
	a.peer = b
	a.mu.Unlock()
	b.mu.Lock()
	b.peer = a
	b.mu.Unlock()
}

// SetRxBits sets the signalling bits the far end sends (a CAS channel), and raises the event.
func (f *Fake) SetRxBits(abcd int) {
	f.mu.Lock()
	f.rxBits = abcd
	f.mu.Unlock()
	f.Raise(EventBitsChanged)
}

// TxBitsSent is every value of the signalling bits the bridge set.
func (f *Fake) TxBitsSent() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.txBits...)
}

func (f *Fake) RxBits() (int, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.rxBits, nil }

func (f *Fake) TxBits(abcd int) error {
	f.mu.Lock()
	f.txBits = append(f.txBits, abcd)
	peer := f.peer
	f.mu.Unlock()
	if peer != nil {
		peer.SetRxBits(abcd)
	}
	return nil
}

// NewFake makes a fake port.
func NewFake() *Fake { return &Fake{wake: make(chan struct{}, 1)} }

func (f *Fake) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Raise queues a driver event (EventRingOffHook ...).
func (f *Fake) Raise(ev int) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
	f.poke()
}

// Speak queues audio heard on the port, G.711 in 20 ms frames.
func (f *Fake) Speak(frames [][]byte) {
	f.mu.Lock()
	f.in = append(f.in, frames...)
	f.mu.Unlock()
}

// Hooks, Tones and Wrote say what the bridge did.
func (f *Fake) Hooks() []int { f.mu.Lock(); defer f.mu.Unlock(); return append([]int(nil), f.hooks...) }
func (f *Fake) Tones() []int { f.mu.Lock(); defer f.mu.Unlock(); return append([]int(nil), f.tones...) }
func (f *Fake) Wrote() int   { f.mu.Lock(); defer f.mu.Unlock(); return f.wrote }

// Sent is the audio written to the port.
func (f *Fake) Sent() []byte { f.mu.Lock(); defer f.mu.Unlock(); return append([]byte(nil), f.sent...) }

// Read gives a frame every 20 ms (silence when nothing is queued), or ErrEvent.
func (f *Fake) Read(b []byte) (int, error) {
	select {
	case <-f.wake:
	case <-time.After(20 * time.Millisecond):
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, errors.New("closed")
	}
	if len(f.events) > 0 {
		return 0, ErrEvent
	}
	if len(f.in) > 0 {
		n := copy(b, f.in[0])
		f.in = f.in[1:]
		return n, nil
	}
	for i := range b {
		b[i] = 0xff // µ-law silence
	}
	return len(b), nil
}

func (f *Fake) Write(b []byte) (int, error) {
	f.mu.Lock()
	f.wrote += len(b)
	if len(f.sent) < 1<<20 {
		f.sent = append(f.sent, b...)
	}
	peer := f.peer
	f.mu.Unlock()
	if peer != nil {
		peer.Speak([][]byte{append([]byte(nil), b...)})
	}
	return len(b), nil
}

func (f *Fake) Event() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return 0, nil
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}

func (f *Fake) Hook(op int) error {
	f.mu.Lock()
	f.hooks = append(f.hooks, op)
	f.mu.Unlock()
	return nil
}

func (f *Fake) Tone(t int) error {
	f.mu.Lock()
	f.tones = append(f.tones, t)
	f.mu.Unlock()
	return nil
}

func (f *Fake) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.poke()
	return nil
}

// DTMF is the frames of a dialled number, as an analogue phone sends it (for tests).
func DTMF(number string) [][]byte { return dtmfFrames(number, 0, false) }
