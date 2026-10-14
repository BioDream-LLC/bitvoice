package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MusicFile is one track of a hold music class.
type MusicFile struct {
	Name    string `json:"name"`
	Seconds int    `json:"seconds"`
}

func (s *Store) musicDir(class string) (string, error) {
	if !safeName.MatchString(class) {
		return "", ErrNotFound
	}
	return filepath.Join(s.dir, "music", class), nil
}

// MusicFiles lists a class's tracks in name order (8 kHz 16-bit WAV, as BitVoice stores them).
func (s *Store) MusicFiles(class string) []MusicFile {
	d, err := s.musicDir(class)
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(d)
	out := []MusicFile{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".wav")
		if !ok || !safeName.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, MusicFile{Name: name, Seconds: int(max(info.Size()-44, 0) / 16000)})
	}
	slices.SortFunc(out, func(a, b MusicFile) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// MusicAudio returns one track.
func (s *Store) MusicAudio(class, name string) ([]byte, error) {
	d, err := s.musicDir(class)
	if err != nil || !safeName.MatchString(name) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(d, name+".wav"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// AddMusic stores a track (replacing one of the same name).
func (s *Store) AddMusic(class, name string, wav []byte) error {
	d, err := s.musicDir(class)
	if err != nil || !safeName.MatchString(name) {
		return ErrNotFound
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	return writeFile(filepath.Join(d, name+".wav"), wav)
}

// DeleteMusic removes one track; an empty name removes the whole class.
func (s *Store) DeleteMusic(class, name string) error {
	d, err := s.musicDir(class)
	if err != nil {
		return err
	}
	if name == "" {
		return os.RemoveAll(d)
	}
	if !safeName.MatchString(name) {
		return ErrNotFound
	}
	err = os.Remove(filepath.Join(d, name+".wav"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
