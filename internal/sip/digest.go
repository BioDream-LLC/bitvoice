package sip

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"
)

// Digest authentication (RFC 3261 22, RFC 2617, RFC 8760 for SHA-256).
//
// Nonces are stateless: a timestamp plus an HMAC over it with a per-process secret. That lets any node verify
// a nonce without shared state and lets a nonce expire without a table of issued nonces. Replay inside the
// validity window is bounded by the short lifetime and by the nonce-count check for qop=auth.

// Challenge is a parsed WWW-Authenticate or Proxy-Authenticate value.
type Challenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	Algorithm string // MD5 (default) or SHA-256
	QOP       string // offered qop list, e.g. "auth"
	Stale     bool
}

// Credentials is a parsed Authorization or Proxy-Authorization value.
type Credentials struct {
	Username  string
	Realm     string
	Nonce     string
	URI       string
	Response  string
	Algorithm string
	CNonce    string
	NC        string
	QOP       string
	Opaque    string
}

// parseAuthParams parses `Digest a="b", c=d`.
func parseAuthParams(v string) (map[string]string, error) {
	v = strings.TrimSpace(v)
	if len(v) < 7 || !strings.EqualFold(v[:6], "Digest") {
		return nil, errors.New("sip: not a Digest header")
	}
	out := map[string]string{}
	for _, part := range splitList(v[6:]) {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(part[:eq]))] = unquote(strings.TrimSpace(part[eq+1:]))
	}
	return out, nil
}

// ParseChallenge parses a WWW-Authenticate or Proxy-Authenticate header value.
func ParseChallenge(v string) (*Challenge, error) {
	p, err := parseAuthParams(v)
	if err != nil {
		return nil, err
	}
	c := &Challenge{Realm: p["realm"], Nonce: p["nonce"], Opaque: p["opaque"], Algorithm: p["algorithm"],
		QOP: p["qop"], Stale: strings.EqualFold(p["stale"], "true")}
	if c.Nonce == "" {
		return nil, errors.New("sip: challenge without nonce")
	}
	return c, nil
}

func (c *Challenge) String() string {
	s := fmt.Sprintf(`Digest realm="%s", nonce="%s"`, c.Realm, c.Nonce)
	if c.Opaque != "" {
		s += fmt.Sprintf(`, opaque="%s"`, c.Opaque)
	}
	if c.Algorithm != "" {
		s += ", algorithm=" + c.Algorithm
	}
	if c.QOP != "" {
		s += fmt.Sprintf(`, qop="%s"`, c.QOP)
	}
	if c.Stale {
		s += ", stale=true"
	}
	return s
}

// ParseCredentials parses an Authorization or Proxy-Authorization header value.
func ParseCredentials(v string) (*Credentials, error) {
	p, err := parseAuthParams(v)
	if err != nil {
		return nil, err
	}
	return &Credentials{Username: p["username"], Realm: p["realm"], Nonce: p["nonce"], URI: p["uri"],
		Response: p["response"], Algorithm: p["algorithm"], CNonce: p["cnonce"], NC: p["nc"],
		QOP: p["qop"], Opaque: p["opaque"]}, nil
}

func (c *Credentials) String() string {
	s := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		c.Username, c.Realm, c.Nonce, c.URI, c.Response)
	if c.Algorithm != "" {
		s += ", algorithm=" + c.Algorithm
	}
	if c.Opaque != "" {
		s += fmt.Sprintf(`, opaque="%s"`, c.Opaque)
	}
	if c.QOP != "" {
		s += fmt.Sprintf(`, qop=%s, nc=%s, cnonce="%s"`, c.QOP, c.NC, c.CNonce)
	}
	return s
}

func digestHash(algorithm string) (func() hash.Hash, error) {
	switch strings.ToUpper(algorithm) {
	case "", "MD5":
		return md5.New, nil
	case "SHA-256":
		return sha256.New, nil
	}
	return nil, fmt.Errorf("sip: unsupported digest algorithm %q", algorithm)
}

func hexHash(h func() hash.Hash, s string) string {
	x := h()
	x.Write([]byte(s))
	return hex.EncodeToString(x.Sum(nil))
}

// DigestResponse computes the response value for the given inputs.
func DigestResponse(algorithm, username, realm, password, method, uri, nonce, qop, nc, cnonce string) (string, error) {
	h, err := digestHash(algorithm)
	if err != nil {
		return "", err
	}
	ha1 := hexHash(h, username+":"+realm+":"+password)
	ha2 := hexHash(h, method+":"+uri)
	if qop == "" {
		return hexHash(h, ha1+":"+nonce+":"+ha2), nil
	}
	return hexHash(h, ha1+":"+nonce+":"+nc+":"+cnonce+":"+qop+":"+ha2), nil
}

// Answer builds credentials answering a challenge, for BitVoice acting as a client (trunks).
func Answer(c *Challenge, username, password, method, uri string) (*Credentials, error) {
	cr := &Credentials{Username: username, Realm: c.Realm, Nonce: c.Nonce, URI: uri, Algorithm: c.Algorithm,
		Opaque: c.Opaque}
	if c.QOP != "" {
		for _, q := range strings.Split(c.QOP, ",") {
			if strings.TrimSpace(strings.ToLower(q)) == "auth" {
				cr.QOP, cr.NC, cr.CNonce = "auth", "00000001", randomHex(8)
			}
		}
	}
	resp, err := DigestResponse(cr.Algorithm, username, c.Realm, password, method, uri, c.Nonce, cr.QOP, cr.NC, cr.CNonce)
	if err != nil {
		return nil, err
	}
	cr.Response = resp
	return cr, nil
}

// Authenticator issues and verifies stateless digest challenges for one realm.
type Authenticator struct {
	Realm    string
	secret   []byte
	Lifetime time.Duration
	now      func() time.Time
}

// NewAuthenticator returns an authenticator with a random secret.
func NewAuthenticator(realm string) *Authenticator {
	s := make([]byte, 32)
	if _, err := randRead(s); err != nil {
		panic("sip: system random source failed: " + err.Error())
	}
	return &Authenticator{Realm: realm, secret: s, Lifetime: 5 * time.Minute, now: time.Now}
}

func (a *Authenticator) nonce() string {
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(a.now().Unix()))
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(ts[:])
	return base64.RawURLEncoding.EncodeToString(append(ts[:], mac.Sum(nil)[:16]...))
}

// nonceState reports whether a nonce is genuine and whether it is still fresh.
func (a *Authenticator) nonceState(n string) (genuine, fresh bool) {
	raw, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil || len(raw) != 24 {
		return false, false
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(raw[:8])
	if !hmac.Equal(mac.Sum(nil)[:16], raw[8:]) {
		return false, false
	}
	issued := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	age := a.now().Sub(issued)
	return true, age >= -time.Minute && age <= a.Lifetime
}

// Challenge returns a fresh challenge header value. stale tells a client its credentials were right but the
// nonce had expired, so it retries without asking the user.
func (a *Authenticator) Challenge(stale bool) string {
	c := &Challenge{Realm: a.Realm, Nonce: a.nonce(), Algorithm: "MD5", QOP: "auth", Stale: stale}
	return c.String()
}

// Result of verifying credentials.
type AuthResult int

const (
	AuthMissing AuthResult = iota // no credentials: challenge
	AuthOK
	AuthStale // right password, expired nonce: challenge with stale=true
	AuthBad   // wrong: challenge again (and count towards fail-to-ban)
)

// Verify checks credentials from a request. lookup returns the password for a username, and false when the
// user does not exist. method is the request method.
func (a *Authenticator) Verify(header, method string, lookup func(user string) (string, bool)) (AuthResult, string) {
	if header == "" {
		return AuthMissing, ""
	}
	cr, err := ParseCredentials(header)
	if err != nil || cr.Username == "" {
		return AuthBad, ""
	}
	if cr.Realm != a.Realm {
		return AuthBad, cr.Username
	}
	genuine, fresh := a.nonceState(cr.Nonce)
	if !genuine {
		// A nonce we never issued (for example from before a restart) is treated as stale, so the phone
		// retries silently rather than the user being asked for a password.
		return AuthStale, cr.Username
	}
	password, ok := lookup(cr.Username)
	if !ok {
		// Compute a response anyway so a missing user takes as long as a wrong password.
		_, _ = DigestResponse(cr.Algorithm, cr.Username, cr.Realm, "x", method, cr.URI, cr.Nonce, cr.QOP, cr.NC, cr.CNonce)
		return AuthBad, cr.Username
	}
	want, err := DigestResponse(cr.Algorithm, cr.Username, cr.Realm, password, method, cr.URI, cr.Nonce, cr.QOP, cr.NC, cr.CNonce)
	if err != nil {
		return AuthBad, cr.Username
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(cr.Response)), []byte(want)) != 1 {
		return AuthBad, cr.Username
	}
	if !fresh {
		return AuthStale, cr.Username
	}
	return AuthOK, cr.Username
}

// ParseExpires reads an Expires header or contact parameter.
func ParseExpires(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
