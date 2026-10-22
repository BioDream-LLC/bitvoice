package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Token is an API token for automation (Terraform, scripts, CI): a bearer secret with a role. Only its
// SHA-256 is stored; the secret is shown once, when it is made.
type Token struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"` // admin or viewer
	Hash     string    `json:"hash"`
	Prefix   string    `json:"prefix"` // the first characters, to recognise it by
	Created  time.Time `json:"created"`
	By       string    `json:"by"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Expires  time.Time `json:"expires,omitzero"`
	// OnBehalf: a trusted program (a CAD) that names the person it acts for in each request
	// (X-BitVoice-On-Behalf-Of), so records such as the 911 exposure log name that person.
	OnBehalf bool `json:"onBehalf,omitempty"`
}

func (s *Store) tokensPath() string { return filepath.Join(s.dir, "tokens.json") }

func (s *Store) loadTokens() []Token {
	var ts []Token
	_ = s.readDoc(s.tokensPath(), &ts)
	return ts
}

// Tokens lists the API tokens, without their hashes.
func (s *Store) Tokens() []Token {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	out := []Token{}
	for _, t := range s.loadTokens() {
		t.Hash = ""
		out = append(out, t)
	}
	return out
}

// AddToken makes a token and returns it with its secret, which is never available again.
func (s *Store) AddToken(name, role, by string, expires time.Time, onBehalf bool) (Token, string, error) {
	if role != "admin" && role != "viewer" {
		return Token{}, "", errors.New("store: role must be admin or viewer")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Token{}, "", err
	}
	secret := "bv_" + hex.EncodeToString(b)
	t, err := s.AddTokenSecret(name, role, by, expires, onBehalf, secret)
	return t, secret, err
}

// AddTokenSecret adds a token whose secret was made elsewhere (bv_ and 64 hex digits, from a good random
// source): a server that is rebuilt from nothing, like the public demo every night, keeps the token a
// program outside it was given. Only its hash is kept.
func (s *Store) AddTokenSecret(name, role, by string, expires time.Time, onBehalf bool, secret string) (Token, error) {
	if role != "admin" && role != "viewer" {
		return Token{}, errors.New("store: role must be admin or viewer")
	}
	if raw, ok := strings.CutPrefix(secret, "bv_"); !ok || len(raw) != 64 || strings.Trim(raw, "0123456789abcdef") != "" {
		return Token{}, errors.New("store: a token secret is bv_ and 64 lower-case hex digits")
	}
	sum := sha256.Sum256([]byte(secret))
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	t := Token{ID: hex.EncodeToString(idb), Name: strings.TrimSpace(name), Role: role, Hash: hex.EncodeToString(sum[:]),
		Prefix: secret[:9], Created: time.Now().UTC(), By: by, Expires: expires, OnBehalf: onBehalf}
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	if err := s.writeDoc(s.tokensPath(), append(s.loadTokens(), t)); err != nil {
		return Token{}, err
	}
	t.Hash = ""
	return t, nil
}

// CheckToken returns the token a bearer secret belongs to, recording its use.
func (s *Store) CheckToken(secret string) (Token, bool) {
	if !strings.HasPrefix(secret, "bv_") {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(secret))
	want := hex.EncodeToString(sum[:])
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	ts := s.loadTokens()
	for i, t := range ts {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(want)) == 1 {
			if !t.Expires.IsZero() && time.Now().After(t.Expires) {
				return Token{}, false
			}
			if time.Since(t.LastUsed) > time.Minute { // not a write on every request
				ts[i].LastUsed = time.Now().UTC()
				_ = s.writeDoc(s.tokensPath(), ts)
			}
			return t, true
		}
	}
	return Token{}, false
}

// DeleteToken revokes a token.
func (s *Store) DeleteToken(id string) error {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	ts := s.loadTokens()
	i := slices.IndexFunc(ts, func(t Token) bool { return t.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.writeDoc(s.tokensPath(), slices.Delete(ts, i, i+1))
}
