package store

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// WakeUp is a wake-up call booked for an extension (a hotel room, a night shift): BitVoice rings it at
// At and says the wake-up prompt; unanswered, it tries again a few times.
type WakeUp struct {
	ID        string    `json:"id"`
	Extension string    `json:"extension"`
	At        time.Time `json:"at"`
	Daily     bool      `json:"daily,omitempty"` // again every day at the same time
	By        string    `json:"by"`              // phone, guest system (PMS) or an administrator
	// Tries made so far for this time, and the outcome of the last: answered, no answer, failed.
	Tries   int       `json:"tries"`
	Last    string    `json:"last,omitempty"`
	LastAt  time.Time `json:"lastAt,omitzero"`
	Created time.Time `json:"created"`
}

var wakeMu sync.Mutex

const wakeDoc = "wakeups.json"

// WakeUps lists the booked wake-up calls, soonest first.
func (s *Store) WakeUps() []WakeUp {
	wakeMu.Lock()
	defer wakeMu.Unlock()
	return s.loadWakeUps()
}

func (s *Store) loadWakeUps() []WakeUp {
	ws := []WakeUp{}
	_ = s.readDoc(wakeDoc, &ws)
	if ws == nil {
		ws = []WakeUp{}
	}
	slices.SortFunc(ws, func(a, b WakeUp) int { return a.At.Compare(b.At) })
	return ws
}

// PutWakeUp books a wake-up call, replacing any other for the same extension (one each, as on a
// hotel phone).
func (s *Store) PutWakeUp(w WakeUp) (WakeUp, error) {
	if w.Extension == "" || w.At.IsZero() {
		return w, errors.New("store: a wake-up call needs an extension and a time")
	}
	wakeMu.Lock()
	defer wakeMu.Unlock()
	ws := slices.DeleteFunc(s.loadWakeUps(), func(x WakeUp) bool { return x.Extension == w.Extension || x.ID == w.ID })
	if w.ID == "" {
		w.ID = strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000"), ".", "")
	}
	if w.Created.IsZero() {
		w.Created = time.Now()
	}
	ws = append(ws, w)
	return w, s.writeDoc(wakeDoc, ws)
}

// DeleteWakeUp cancels an extension's wake-up call.
func (s *Store) DeleteWakeUp(ext string) error {
	wakeMu.Lock()
	defer wakeMu.Unlock()
	ws := s.loadWakeUps()
	n := len(ws)
	ws = slices.DeleteFunc(ws, func(x WakeUp) bool { return x.Extension == ext })
	if len(ws) == n {
		return ErrNotFound
	}
	return s.writeDoc(wakeDoc, ws)
}

// WakeUpFor returns an extension's wake-up call.
func (s *Store) WakeUpFor(ext string) (WakeUp, bool) {
	for _, w := range s.WakeUps() {
		if w.Extension == ext {
			return w, true
		}
	}
	return WakeUp{}, false
}
