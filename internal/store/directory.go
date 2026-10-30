package store

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// DirContact is one entry of the company directory that every phone shows (beside the extensions).
type DirContact struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Company string        `json:"company,omitempty"`
	Numbers []PhoneNumber `json:"numbers"`
	Email   string        `json:"email,omitempty"`
	Notes   string        `json:"notes,omitempty"`
	Updated time.Time     `json:"updated"`
	// Source is where a contact comes from, kept in step from there (microsoft365 or google);
	// empty for one made here. SourceID is its id there.
	Source   string `json:"source,omitempty"`
	SourceID string `json:"sourceId,omitempty"`
}

// PhoneNumber is one of a contact's numbers.
type PhoneNumber struct {
	Kind   string `json:"kind"` // work, mobile, home or other
	Number string `json:"number"`
}

// ErrNoContact means there is no directory contact with that id.
var ErrNoContact = errors.New("store: no such contact")

func (s *Store) dirPath() string { return filepath.Join(s.dir, "directory.json") }

func (s *Store) loadDirectory() []DirContact {
	var cs []DirContact
	_ = s.readDoc(s.dirPath(), &cs)
	slices.SortFunc(cs, func(a, b DirContact) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return cs
}

// Directory returns every shared contact, by name.
func (s *Store) Directory() []DirContact {
	s.dirMu.Lock()
	defer s.dirMu.Unlock()
	return s.loadDirectory()
}

// PutContact adds a contact (no ID) or replaces the one with its ID.
func (s *Store) PutContact(c DirContact) (DirContact, error) {
	s.dirMu.Lock()
	defer s.dirMu.Unlock()
	cs := s.loadDirectory()
	c.Updated = time.Now().UTC()
	if c.ID == "" {
		c.ID = config.NewID()
		cs = append(cs, c)
	} else {
		i := slices.IndexFunc(cs, func(x DirContact) bool { return x.ID == c.ID })
		if i < 0 {
			return DirContact{}, ErrNoContact
		}
		cs[i] = c
	}
	return c, s.writeDoc(s.dirPath(), cs)
}

// DeleteContact removes a contact.
func (s *Store) DeleteContact(id string) error {
	s.dirMu.Lock()
	defer s.dirMu.Unlock()
	cs := s.loadDirectory()
	i := slices.IndexFunc(cs, func(x DirContact) bool { return x.ID == id })
	if i < 0 {
		return ErrNoContact
	}
	return s.writeDoc(s.dirPath(), slices.Delete(cs, i, i+1))
}

// ImportContacts adds contacts in one write; replace drops every existing contact first. A contact
// whose name matches an existing one (ignoring case) updates it instead of adding a second.
func (s *Store) ImportContacts(in []DirContact, replace bool) (added, updated int, err error) {
	s.dirMu.Lock()
	defer s.dirMu.Unlock()
	cs := s.loadDirectory()
	if replace {
		cs = nil
	}
	now := time.Now().UTC()
	for _, c := range in {
		c.Updated = now
		i := slices.IndexFunc(cs, func(x DirContact) bool { return strings.EqualFold(x.Name, c.Name) })
		if i >= 0 {
			c.ID = cs[i].ID
			cs[i] = c
			updated++
			continue
		}
		c.ID = config.NewID()
		cs = append(cs, c)
		added++
	}
	return added, updated, s.writeDoc(s.dirPath(), cs)
}

// SyncContacts makes the contacts from source exactly in: those it no longer has go, those it still
// has keep their id here, new ones are added. Contacts made here, or from elsewhere, are untouched.
func (s *Store) SyncContacts(source string, in []DirContact) (added, updated, removed int, err error) {
	s.dirMu.Lock()
	defer s.dirMu.Unlock()
	cs := s.loadDirectory()
	old := map[string]DirContact{}
	keep := cs[:0]
	for _, c := range cs {
		if c.Source == source {
			old[c.SourceID] = c
			continue
		}
		keep = append(keep, c)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}
	for _, c := range in {
		if seen[c.SourceID] {
			continue
		}
		seen[c.SourceID] = true
		c.Source = source
		if o, ok := old[c.SourceID]; ok {
			c.ID, c.Updated = o.ID, o.Updated
			if !sameContact(o, c) {
				c.Updated = now
				updated++
			}
		} else {
			c.ID, c.Updated = config.NewID(), now
			added++
		}
		keep = append(keep, c)
	}
	removed = len(old) - (len(seen) - added)
	return added, updated, removed, s.writeDoc(s.dirPath(), keep)
}

func sameContact(a, b DirContact) bool {
	if a.Name != b.Name || a.Company != b.Company || a.Email != b.Email || a.Notes != b.Notes || len(a.Numbers) != len(b.Numbers) {
		return false
	}
	for i := range a.Numbers {
		if a.Numbers[i] != b.Numbers[i] {
			return false
		}
	}
	return true
}
