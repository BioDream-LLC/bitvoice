package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Hold is a legal hold: call records, recordings and voicemail it covers are kept, whatever the
// retention settings say, and cannot be deleted, until it is released.
type Hold struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Numbers are the extensions or phone numbers it covers (either side of a call, or a mailbox); empty
	// covers everyone.
	Numbers []string `json:"numbers"`
	// Since and Until bound when the calls happened; zero is open-ended.
	Since    time.Time `json:"since,omitzero"`
	Until    time.Time `json:"until,omitzero"`
	Created  time.Time `json:"created"`
	By       string    `json:"by"`
	Released time.Time `json:"released,omitzero"`
	FreedBy  string    `json:"releasedBy,omitempty"`
}

// ErrHeld means something is under a legal hold.
var ErrHeld = errors.New("store: this is under a legal hold and cannot be deleted")

func (s *Store) holdsPath() string { return filepath.Join(s.dir, "holds.json") }

// Holds returns every hold, active and released, newest first.
func (s *Store) Holds() []Hold {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	return s.loadHolds()
}

func (s *Store) loadHolds() []Hold {
	var hs []Hold
	_ = s.readDoc(s.holdsPath(), &hs)
	slices.SortFunc(hs, func(a, b Hold) int { return b.Created.Compare(a.Created) })
	return hs
}

// PutHold creates a hold, or changes one that is still active.
func (s *Store) PutHold(h Hold) (Hold, error) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	hs := s.loadHolds()
	i := slices.IndexFunc(hs, func(x Hold) bool { return x.ID == h.ID })
	if i >= 0 {
		if !hs[i].Released.IsZero() {
			return h, errors.New("store: a released hold cannot be changed")
		}
		h.Created, h.By = hs[i].Created, hs[i].By
		hs[i] = h
	} else {
		h.Created = time.Now().UTC()
		hs = append(hs, h)
	}
	if h.Numbers == nil {
		h.Numbers = []string{}
	}
	return h, s.writeDoc(s.holdsPath(), hs)
}

// ReleaseHold ends a hold; what it kept is then subject to retention again.
func (s *Store) ReleaseHold(id, by string) error {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	hs := s.loadHolds()
	i := slices.IndexFunc(hs, func(x Hold) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	if hs[i].Released.IsZero() {
		hs[i].Released, hs[i].FreedBy = time.Now().UTC(), by
	}
	return s.writeDoc(s.holdsPath(), hs)
}

func (h Hold) active() bool { return h.Released.IsZero() }

func (h Hold) covers(at time.Time, numbers ...string) bool {
	if !h.active() || (!h.Since.IsZero() && at.Before(h.Since)) || (!h.Until.IsZero() && at.After(h.Until)) {
		return false
	}
	if len(h.Numbers) == 0 {
		return true
	}
	for _, n := range numbers {
		if n != "" && slices.Contains(h.Numbers, n) {
			return true
		}
	}
	return false
}

func (s *Store) activeHolds() []Hold {
	var out []Hold
	for _, h := range s.Holds() {
		if h.active() {
			out = append(out, h)
		}
	}
	return out
}

func heldBy(hs []Hold, at time.Time, numbers ...string) bool {
	for _, h := range hs {
		if h.covers(at, numbers...) {
			return true
		}
	}
	return false
}

// CDRHeld reports whether a call record (and its recording) is under hold.
func (s *Store) CDRHeld(r CDR) bool {
	return heldBy(s.activeHolds(), r.Start, r.From, r.To, r.AnsweredBy)
}

// VoicemailHeld reports whether a message is under hold.
func (s *Store) VoicemailHeld(box string, at time.Time, from string) bool {
	return heldBy(s.activeHolds(), at, box, from)
}

// heldRecordings is the set of recording names whose calls are under hold.
func (s *Store) heldRecordings(hs []Hold) map[string]bool {
	out := map[string]bool{}
	if len(hs) == 0 {
		return out
	}
	s.eachCDR(func(r CDR) {
		if r.Recording != "" && heldBy(hs, r.Start, r.From, r.To, r.AnsweredBy) {
			out[r.Recording] = true
		}
	})
	return out
}

// PruneVoicemail deletes messages older than days (0 keeps everything), except those under hold.
func (s *Store) PruneVoicemail(days int) int {
	if days <= 0 {
		return 0
	}
	cut := time.Now().AddDate(0, 0, -days)
	dirs, _ := os.ReadDir(filepath.Join(s.dir, "voicemail"))
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		msgs, err := s.Voicemails(d.Name())
		if err != nil {
			continue
		}
		for _, v := range msgs {
			if v.At.Before(cut) && s.DeleteVoicemail(d.Name(), v.ID) == nil {
				n++
			}
		}
	}
	return n
}
