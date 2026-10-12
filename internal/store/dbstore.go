package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/db"
)

// The database holds what used to be the data directory's JSON files. Small lists (users, contacts,
// tokens and the like) are documents named as their file was ("users.json", "texts/state.json"), so
// the code that keeps them reads as before; logs that grow (call records, audit, texts, transcripts)
// have their own indexed tables. Audio stays in files.

func (s *Store) ctx() context.Context { return context.Background() }

// readDoc reads a document into v; os.ErrNotExist when there is none, as a missing file was.
func (s *Store) readDoc(name string, v any) error {
	b, ok, err := s.db.GetDoc(s.ctx(), s.docName(name))
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	return json.Unmarshal(b, v)
}

// writeDoc writes a document whole.
func (s *Store) writeDoc(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.PutDoc(s.ctx(), s.docName(name), b)
}

// docName turns a path (full, or relative to the data directory) into a document name.
func (s *Store) docName(p string) string {
	if filepath.IsAbs(p) {
		if r, err := filepath.Rel(s.dir, p); err == nil && !strings.HasPrefix(r, "..") {
			p = r
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// Where says where the data is kept, for people.
func (s *Store) Where() string { return s.db.Where }

// DB is the database, for the server's own reports.
func (s *Store) DB() *db.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// importNote is kept in the database once the old files are in it.
const importNote = "import.json"

// importFiles brings an older data directory's JSON files into a new, empty database, then moves
// them into pre-database/ so nobody edits a file BitVoice no longer reads. It runs once.
func (s *Store) importFiles() error {
	var done struct{ At time.Time }
	if s.readDoc(importNote, &done) == nil {
		return nil
	}
	var moved []string
	docs := []string{"state.json", "users.json", "callbacks.json", "devices.json", "directory.json", "holds.json",
		"notes.json", "remote-addresses.json", "seen-devices.json", "sessions.json", "tokens.json", "hotdesk.json",
		filepath.Join("texts", "state.json")}
	for _, n := range docs {
		b, err := os.ReadFile(filepath.Join(s.dir, n))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			continue // a damaged file is left where it is, and not imported
		}
		if err := s.db.PutDoc(s.ctx(), s.docName(n), b); err != nil {
			return err
		}
		moved = append(moved, n)
	}
	n, err := s.importRevisions()
	if err != nil {
		return err
	}
	if n > 0 {
		moved = append(moved, "config.json", "revisions")
	}
	for _, f := range []func() ([]string, error){s.importCDRs, s.importAudit, s.importTexts, s.importTranscripts} {
		m, err := f()
		if err != nil {
			return err
		}
		moved = append(moved, m...)
	}
	if len(moved) > 0 {
		old := filepath.Join(s.dir, "pre-database")
		for _, m := range moved {
			from := filepath.Join(s.dir, m)
			to := filepath.Join(old, m)
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				return err
			}
			if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return s.writeDoc(importNote, struct {
		At    time.Time `json:"at"`
		Files []string  `json:"files"`
	}{time.Now().UTC(), moved})
}

// jsonLines calls f for each line of a JSON Lines file.
func jsonLines(path string, f func([]byte)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && json.Valid([]byte(line)) {
			f([]byte(line))
		}
	}
	return nil
}
