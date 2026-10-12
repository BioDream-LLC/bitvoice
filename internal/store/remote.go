package store

import (
	"path/filepath"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// RemoteAddress is where a remote worker said they are, for emergency calls.
type RemoteAddress struct {
	Address   config.Address `json:"address"`
	Confirmed time.Time      `json:"confirmed"`
}

func (s *Store) remotePath() string { return filepath.Join(s.dir, "remote-addresses.json") }

func (s *Store) loadRemote() map[string]RemoteAddress {
	m := map[string]RemoteAddress{}
	_ = s.readDoc(s.remotePath(), &m)
	return m
}

// RemoteAddress gives an extension's confirmed remote address (pbx.RemoteAddresses).
func (s *Store) RemoteAddress(ext string) (config.Address, time.Time, bool) {
	s.remMu.Lock()
	defer s.remMu.Unlock()
	r, ok := s.loadRemote()[ext]
	return r.Address, r.Confirmed, ok
}

// SetRemoteAddress records where a remote worker is, now.
func (s *Store) SetRemoteAddress(ext string, a config.Address) error {
	s.remMu.Lock()
	defer s.remMu.Unlock()
	m := s.loadRemote()
	m[ext] = RemoteAddress{Address: a, Confirmed: time.Now().UTC()}
	return s.writeDoc(s.remotePath(), m)
}
