package store

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"time"
)

// SavedSession is a signed-in browser kept across restarts. Only a hash of the cookie is stored, so the
// file does not hold anything that signs a browser in.
type SavedSession struct {
	Username string    `json:"username"`
	Expires  time.Time `json:"expires"`
}

// SessionKey is the stored form of a session cookie.
func SessionKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Store) sessionsPath() string { return filepath.Join(s.dir, "sessions.json") }

// LoadSessions reads the saved sessions, dropping the expired ones.
func (s *Store) LoadSessions() map[string]SavedSession {
	out := map[string]SavedSession{}
	var all map[string]SavedSession
	if s.readDoc(s.sessionsPath(), &all) != nil {
		return out
	}
	now := time.Now()
	for k, v := range all {
		if v.Expires.After(now) {
			out[k] = v
		}
	}
	return out
}

// SaveSessions replaces the saved sessions.
func (s *Store) SaveSessions(all map[string]SavedSession) error {
	if len(all) == 0 {
		return s.db.DeleteDoc(s.ctx(), s.docName(s.sessionsPath()))
	}
	return s.writeDoc(s.sessionsPath(), all)
}
