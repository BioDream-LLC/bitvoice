package store

import (
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Callback is a request, taken by an AI receptionist (or a person), for someone to ring a caller back.
type Callback struct {
	ID           string    `json:"id"`
	Created      time.Time `json:"created"`
	Receptionist string    `json:"receptionist,omitempty"` // the receptionist's name, when one took it
	CallID       string    `json:"callId,omitempty"`
	Name         string    `json:"name"`
	Number       string    `json:"number"`
	When         string    `json:"when"`   // the time the caller asked for, in their words
	Reason       string    `json:"reason"` // what it is about, in their words
	// Status is open until someone rings back (done) or decides not to (cancelled).
	Status string    `json:"status"`
	By     string    `json:"by,omitempty"` // who closed it
	Closed time.Time `json:"closed,omitzero"`
	Note   string    `json:"note,omitempty"`
}

// ErrNoCallback means there is no callback with that id.
var ErrNoCallback = errors.New("store: no such callback")

func (s *Store) callbacksPath() string { return filepath.Join(s.dir, "callbacks.json") }

func (s *Store) loadCallbacks() []Callback {
	var cs []Callback
	_ = s.readDoc(s.callbacksPath(), &cs)
	slices.SortFunc(cs, func(a, b Callback) int { return b.Created.Compare(a.Created) })
	return cs
}

// Callbacks returns every callback, newest first.
func (s *Store) Callbacks() []Callback {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	return s.loadCallbacks()
}

// AddCallback records a new open callback.
func (s *Store) AddCallback(c Callback) (Callback, error) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	cs := s.loadCallbacks()
	c.ID, c.Created, c.Status = config.NewID(), time.Now().UTC(), "open"
	cs = append(cs, c)
	return c, s.writeDoc(s.callbacksPath(), cs)
}

// SetCallback changes a callback's status (open, done or cancelled) and note.
func (s *Store) SetCallback(id, status, note, by string) (Callback, error) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	cs := s.loadCallbacks()
	i := slices.IndexFunc(cs, func(x Callback) bool { return x.ID == id })
	if i < 0 {
		return Callback{}, ErrNoCallback
	}
	c := &cs[i]
	c.Note = note
	if status != c.Status {
		c.Status = status
		if status == "open" {
			c.By, c.Closed = "", time.Time{}
		} else {
			c.By, c.Closed = by, time.Now().UTC()
		}
	}
	return *c, s.writeDoc(s.callbacksPath(), cs)
}

// DeleteCallback removes a callback.
func (s *Store) DeleteCallback(id string) error {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	cs := s.loadCallbacks()
	i := slices.IndexFunc(cs, func(x Callback) bool { return x.ID == id })
	if i < 0 {
		return ErrNoCallback
	}
	return s.writeDoc(s.callbacksPath(), slices.Delete(cs, i, i+1))
}
