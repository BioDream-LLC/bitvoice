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
	// MCPOnly: the token works for AI assistants (/mcp) and nowhere else: one an assistant got by
	// signing in (OAuth), never a key to the whole API.
	MCPOnly bool `json:"mcpOnly,omitempty"`
	// Client is the OAuth client (an assistant) the token was issued to, and Refresh the hash of the
	// refresh token it renews itself with until RefreshExpires.
	Client         string    `json:"client,omitempty"`
	Refresh        string    `json:"refresh,omitempty"`
	RefreshExpires time.Time `json:"refreshExpires,omitzero"`
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
		t.Hash, t.Refresh = "", ""
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

func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func newSecret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// AddOAuthToken issues an assistant's token after a person signed in and allowed it: an access
// token for /mcp only that lasts ttl, and a refresh token that renews it for refreshTTL.
func (s *Store) AddOAuthToken(name, role, by, client string, ttl, refreshTTL time.Duration) (Token, string, string, error) {
	if role != "admin" && role != "viewer" {
		return Token{}, "", "", errors.New("store: role must be admin or viewer")
	}
	access, err := newSecret("bv_")
	if err != nil {
		return Token{}, "", "", err
	}
	refresh, err := newSecret("bvr_")
	if err != nil {
		return Token{}, "", "", err
	}
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	now := time.Now().UTC()
	t := Token{ID: hex.EncodeToString(idb), Name: strings.TrimSpace(name), Role: role, Hash: secretHash(access), Prefix: access[:9],
		Created: now, By: by, Expires: now.Add(ttl), MCPOnly: true, Client: client, Refresh: secretHash(refresh), RefreshExpires: now.Add(refreshTTL)}
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	if err := s.writeDoc(s.tokensPath(), append(s.loadTokens(), t)); err != nil {
		return Token{}, "", "", err
	}
	t.Hash, t.Refresh = "", ""
	return t, access, refresh, nil
}

// RefreshOAuthToken renews an assistant's token from its refresh token: a new access token and a
// new refresh token (the old ones stop working). A revoked token cannot be renewed.
func (s *Store) RefreshOAuthToken(client, refresh string, ttl, refreshTTL time.Duration) (Token, string, string, error) {
	want := secretHash(refresh)
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	ts := s.loadTokens()
	for i, t := range ts {
		if t.Refresh == "" || subtle.ConstantTimeCompare([]byte(t.Refresh), []byte(want)) != 1 {
			continue
		}
		if t.Client != client || time.Now().After(t.RefreshExpires) {
			return Token{}, "", "", ErrNotFound
		}
		access, err := newSecret("bv_")
		if err != nil {
			return Token{}, "", "", err
		}
		next, err := newSecret("bvr_")
		if err != nil {
			return Token{}, "", "", err
		}
		now := time.Now().UTC()
		ts[i].Hash, ts[i].Prefix, ts[i].Expires = secretHash(access), access[:9], now.Add(ttl)
		ts[i].Refresh, ts[i].RefreshExpires = secretHash(next), now.Add(refreshTTL)
		if err := s.writeDoc(s.tokensPath(), ts); err != nil {
			return Token{}, "", "", err
		}
		t = ts[i]
		t.Hash, t.Refresh = "", ""
		return t, access, next, nil
	}
	return Token{}, "", "", ErrNotFound
}

// OAuthClient is an assistant registered by OAuth dynamic client registration (RFC 7591): a public
// client (no secret, PKCE) with the addresses it may be sent back to.
type OAuthClient struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirectUris"`
	Created      time.Time `json:"created"`
}

func (s *Store) oauthClientsPath() string { return filepath.Join(s.dir, "oauth-clients.json") }

// maxOAuthClients bounds registrations (anyone may register); the oldest go first.
const maxOAuthClients = 200

// RegisterOAuthClient records a new assistant.
func (s *Store) RegisterOAuthClient(name string, redirects []string) (OAuthClient, error) {
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return OAuthClient{}, err
	}
	c := OAuthClient{ID: "bvc_" + hex.EncodeToString(idb), Name: strings.TrimSpace(name), RedirectURIs: redirects, Created: time.Now().UTC()}
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	var all []OAuthClient
	_ = s.readDoc(s.oauthClientsPath(), &all)
	all = append(all, c)
	if len(all) > maxOAuthClients {
		all = all[len(all)-maxOAuthClients:]
	}
	return c, s.writeDoc(s.oauthClientsPath(), all)
}

// FindOAuthClient returns a registered assistant.
func (s *Store) FindOAuthClient(id string) (OAuthClient, bool) {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	var all []OAuthClient
	_ = s.readDoc(s.oauthClientsPath(), &all)
	for _, c := range all {
		if c.ID == id {
			return c, true
		}
	}
	return OAuthClient{}, false
}
