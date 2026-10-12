// Package events is the in-process publish/subscribe bus behind the live web interface and the API's event
// stream. A slow subscriber never slows calls: its buffer fills and its oldest events are dropped, and it is
// told so it can reload.
package events

import (
	"sync"
	"time"
)

// Event is one thing that happened.
type Event struct {
	Seq  uint64    `json:"seq"`
	At   time.Time `json:"at"`
	Type string    `json:"type"`
	Data any       `json:"data"`
}

// Bus fans events out to subscribers.
type Bus struct {
	mu   sync.Mutex
	seq  uint64
	subs map[*Sub]struct{}
}

// Sub is one subscription.
type Sub struct {
	C       chan Event
	dropped bool
	mu      sync.Mutex
}

// Dropped reports, and clears, whether events were lost since the last call.
func (s *Sub) Dropped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dropped
	s.dropped = false
	return d
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[*Sub]struct{}{}} }

// Subscribe returns a subscription; call Unsubscribe when done.
func (b *Bus) Subscribe() *Sub {
	s := &Sub{C: make(chan Event, 256)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe ends a subscription.
func (b *Bus) Unsubscribe(s *Sub) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Publish sends an event to every subscriber without blocking.
func (b *Bus) Publish(typ string, data any) {
	b.mu.Lock()
	b.seq++
	e := Event{Seq: b.seq, At: time.Now().UTC(), Type: typ, Data: data}
	for s := range b.subs {
		select {
		case s.C <- e:
		default:
			s.mu.Lock()
			s.dropped = true
			s.mu.Unlock()
		}
	}
	b.mu.Unlock()
}
