package store

import (
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Note is a shared note on a customer's timeline, kept by their number.
type Note struct {
	ID     string    `json:"id"`
	Number string    `json:"number"`
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Text   string    `json:"text"`
}

// ErrNoNote means there is no note with that id.
var ErrNoNote = errors.New("store: no such note")

func (s *Store) notesPath() string { return filepath.Join(s.dir, "notes.json") }

func (s *Store) loadNotes() []Note {
	var ns []Note
	_ = s.readDoc(s.notesPath(), &ns)
	return ns
}

// Notes returns the notes on a number, newest first.
func (s *Store) Notes(number string) []Note {
	s.noteMu.Lock()
	defer s.noteMu.Unlock()
	var out []Note
	for _, n := range s.loadNotes() {
		if config.SameNumber(n.Number, number) {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b Note) int { return b.At.Compare(a.At) })
	return out
}

// AddNote adds a note.
func (s *Store) AddNote(n Note) (Note, error) {
	s.noteMu.Lock()
	defer s.noteMu.Unlock()
	n.ID, n.At = config.NewID(), time.Now().UTC()
	return n, s.writeDoc(s.notesPath(), append(s.loadNotes(), n))
}

// DeleteNote removes a note.
func (s *Store) DeleteNote(id string) error {
	s.noteMu.Lock()
	defer s.noteMu.Unlock()
	ns := s.loadNotes()
	i := slices.IndexFunc(ns, func(n Note) bool { return n.ID == id })
	if i < 0 {
		return ErrNoNote
	}
	return s.writeDoc(s.notesPath(), slices.Delete(ns, i, i+1))
}
