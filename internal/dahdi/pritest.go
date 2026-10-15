package dahdi

import (
	"errors"
	"sync"
)

// FakeD is an in-memory D-channel pair for tests: one end for the bridge (through OpenD), the other
// for the far side's isdn.PRI.
type FakeD struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

// NewFakeD makes both ends.
func NewFakeD() (bridge, far *FakeD) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	done, once := make(chan struct{}), &sync.Once{}
	return &FakeD{in: a, out: b, done: done, once: once}, &FakeD{in: b, out: a, done: done, once: once}
}

func (f *FakeD) ReadFrame() ([]byte, error) {
	select {
	case b := <-f.in:
		return b, nil
	case <-f.done:
		return nil, errors.New("closed")
	}
}

func (f *FakeD) WriteFrame(b []byte) error {
	select {
	case f.out <- append([]byte(nil), b...):
	case <-f.done:
	}
	return nil
}

// Close ends both ends.
func (f *FakeD) Close() error { f.once.Do(func() { close(f.done) }); return nil }
