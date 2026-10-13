package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Recordings live in recordings/YYYY-MM/<name>.wav, so old months are easy to archive or remove.
var recordingName = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}/[0-9A-Za-z_.@-]{1,120}$`)

// AddRecording stores a call recording and returns its name (for the call record).
func (s *Store) AddRecording(callID string, at time.Time, wav []byte) (string, error) {
	id := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, callID)
	if len(id) > 100 {
		id = id[:100]
	}
	name := at.UTC().Format("2006-01") + "/" + at.UTC().Format("20060102T150405") + "-" + id
	if err := s.roomFor(len(wav)); err != nil {
		return "", err
	}
	d := filepath.Join(s.dir, "recordings", at.UTC().Format("2006-01"))
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return name, writeFile(filepath.Join(s.dir, "recordings", name+".wav"), wav)
}

// Recording returns a recording's audio by the name AddRecording gave.
func (s *Store) Recording(name string) ([]byte, error) {
	if !recordingName.MatchString(name) || strings.Contains(name, "..") {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "recordings", name+".wav"))
	if err != nil {
		return nil, ErrNotFound
	}
	return b, nil
}

// DeleteRecording removes a recording.
func (s *Store) DeleteRecording(name string) error {
	if !recordingName.MatchString(name) || strings.Contains(name, "..") {
		return ErrNotFound
	}
	if s.heldRecordings(s.activeHolds())[name] {
		return ErrHeld
	}
	err := os.Remove(filepath.Join(s.dir, "recordings", name+".wav"))
	s.deleteTranscript(name) // its transcript goes with it
	return err
}

// PruneRecordings removes recordings older than the given number of days (0 keeps everything) and
// reports how many went.
func (s *Store) PruneRecordings(days int) int {
	if days <= 0 {
		return 0
	}
	cut := time.Now().AddDate(0, 0, -days)
	held := s.heldRecordings(s.activeHolds())
	root := filepath.Join(s.dir, "recordings")
	n := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".wav") {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil && held[filepath.ToSlash(strings.TrimSuffix(rel, ".wav"))] {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cut) {
			if os.Remove(p) == nil {
				if rel, err := filepath.Rel(root, p); err == nil {
					s.deleteTranscript(filepath.ToSlash(strings.TrimSuffix(rel, ".wav")))
				}
				n++
			}
		}
		return nil
	})
	return n
}
