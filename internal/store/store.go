// Package store keeps BitVoice's state on disk: the configuration with every revision, administrator
// accounts, call records and the audit log. It needs no database server: everything is files in one data
// directory, written atomically, so a power cut never leaves a half-written configuration.
package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/db"
)

// Revision is one saved version of the configuration.
type Revision struct {
	Rev     int           `json:"rev"`
	At      time.Time     `json:"at"`
	Actor   string        `json:"actor"`
	Summary string        `json:"summary"`
	Config  config.Config `json:"config,omitzero"`
}

// User is an administrator or operator of the web interface.
type User struct {
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	Role      string    `json:"role"` // admin or viewer
	Hash      string    `json:"hash"`
	Created   time.Time `json:"created"`
	LastLogin time.Time `json:"lastLogin,omitzero"`
	// Two-step sign-in: TOTP is true once a code has confirmed TOTPSecret; TOTPStep is the last step used.
	TOTP       bool   `json:"totp"`
	TOTPSecret string `json:"totpSecret,omitempty"`
	TOTPStep   uint64 `json:"totpStep,omitempty"`
	// SSO is "issuer subject" for people who sign in through the identity provider; they have no password.
	SSO string `json:"sso,omitempty"`
}

// State is installation state that is not configuration.
type State struct {
	InstanceID    string    `json:"instanceId"`
	SetupComplete bool      `json:"setupComplete"`
	SetupAt       time.Time `json:"setupAt,omitzero"`
	Created       time.Time `json:"created"`
}

// CDR is a call detail record.
type CDR struct {
	ID          string    `json:"id"`
	Start       time.Time `json:"start"`
	Answer      time.Time `json:"answer,omitzero"`
	End         time.Time `json:"end"`
	Direction   string    `json:"direction"` // internal, inbound, outbound, emergency, test
	From        string    `json:"from"`
	FromName    string    `json:"fromName"`
	To          string    `json:"to"`
	ToName      string    `json:"toName"`
	AnsweredBy  string    `json:"answeredBy"`
	Trunk       string    `json:"trunk"`
	Disposition string    `json:"disposition"` // answered, no-answer, busy, failed, cancelled
	Cause       string    `json:"cause"`
	Duration    int       `json:"duration"` // seconds from start to end
	Billsec     int       `json:"billsec"`  // seconds from answer to end
	HangupBy    string    `json:"hangupBy"`
	MOS         float64   `json:"mos"`
	// Recording names the call's recording, empty when it was not recorded.
	Recording string `json:"recording,omitempty"`
	// Paused lists the stretches of the recording that were paused (card payments), in seconds from answer.
	Paused []Span `json:"paused,omitempty"`
	// Quality is how each side sounded, as BitVoice received it: for quality by site and provider.
	Quality []LegQuality `json:"quality,omitempty"`
	// Attest and Verified are the caller ID's STIR/SHAKEN attestation (A, B, C) and verification
	// (passed, failed); Screened is labelled or blocked when spam screening acted.
	Attest string `json:"attest,omitempty"`
	// Signed is the attestation (A, B, C) this system signed a call out with (STIR/SHAKEN).
	Signed   string `json:"signed,omitempty"`
	Verified string `json:"verified,omitempty"`
	Screened string `json:"screened,omitempty"`
	// AnsweredExt is the extension that answered, when one did.
	AnsweredExt string `json:"answeredExt,omitempty"`
	// Emergency marks a 911 call this system answered as a PSAP (from the ESInet, or with location).
	Emergency bool `json:"emergency,omitempty"`
	// Taken is when a person (a call-taker) answered it, which for a 911 call is the answer that
	// counts: a queue holding the caller has not answered them.
	Taken time.Time `json:"taken,omitzero"`
	// Ext is the extension that placed an outgoing call; Department is its department then.
	Ext        string `json:"ext,omitempty"`
	Department string `json:"department,omitempty"`
	// Account is the account code: the PIN an outbound route asked for, when its PIN set records it.
	Account string `json:"account,omitempty"`
	// Cost is what the provider charges for the call, in the system currency, from the trunk's rates;
	// Rate names the rate used.
	Cost float64 `json:"cost,omitempty"`
	// CallerID is the number presented to the called party on a call out through a trunk.
	CallerID string `json:"callerId,omitempty"`
	// Code is the SIP failure an unanswered call ended with (607 unwanted, 608 rejected by analytics).
	Code int    `json:"code,omitempty"`
	Rate string `json:"rate,omitempty"`
	// Charged is what the phone company said the call cost (advice of charge), in its own words:
	// "EUR 1.50", "5 units". When it is in the system currency, Cost is that figure.
	Charged string `json:"charged,omitempty"`
	// Text is a 911 call's text conversation: TTY, real-time text or text-to-911.
	Text []TextLine `json:"text,omitempty"`
}

// TextLine is one turn of a 911 call's text conversation.
type TextLine struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`           // caller or taker
	Name string    `json:"name,omitempty"` // the call-taker
	// Via is how it travelled: tty (tones on the voice line), rtt (real-time text, RFC 4103), msrp or
	// sip (text-to-911 through a text control centre).
	Via  string `json:"via"`
	Text string `json:"text"`
	Open bool   `json:"open,omitempty"` // the caller is still typing it
}

// LegQuality is one side of a call as BitVoice heard it.
type LegQuality struct {
	Ext      string  `json:"ext,omitempty"`   // the extension, when this side is one
	Trunk    string  `json:"trunk,omitempty"` // the trunk ID, when this side is a provider
	Codec    string  `json:"codec"`
	Packets  uint64  `json:"packets"`
	Lost     uint64  `json:"lost"`
	JitterMS float64 `json:"jitterMs"`
	MOS      float64 `json:"mos"`
}

// Span is a stretch of a call, in seconds from its answer.
type Span struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AuditEntry records one administrative action.
type AuditEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
	From   string    `json:"from"`
}

// Store is the on-disk state. All methods are safe for concurrent use.
type Store struct {
	dir string
	db  *db.DB

	mu        sync.Mutex
	cfg       config.Config
	rev       int
	users     []User
	state     State
	listeners []func(config.Config)
	cdrMu     sync.Mutex
	holdMu    sync.Mutex
	devMu     sync.Mutex
	tokMu     sync.Mutex
	seenMu    sync.Mutex
	remMu     sync.Mutex
	auditMu   sync.Mutex
	cbMu      sync.Mutex
	dirMu     sync.Mutex
	textMu    sync.Mutex
	noteMu    sync.Mutex
	tenantMu  sync.Mutex
	spec      string // the database it was opened with
	// limit, when set, is checked on every configuration change (a hosted tenant's allowances).
	limit func(*config.Config) config.Problems
	quota quota
}

// Open opens or creates a data directory.
func Open(dir string) (*Store, error) {
	return OpenWith(dir, "")
}

// OpenWith opens the data directory with its data in the database spec names: a PostgreSQL address,
// or empty for SQLite in the data directory (bitvoice.db). Audio stays in the data directory either way.
func OpenWith(dir, spec string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if spec == "" {
		spec = SQLitePath(dir)
	}
	d, err := db.Open(spec)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, db: d, spec: spec}
	if err := s.importFiles(); err != nil {
		d.Close()
		return nil, fmt.Errorf("store: bringing the old files into the database: %w", err)
	}
	if err := s.readDoc("state.json", &s.state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		s.state = State{InstanceID: randomHex(8), Created: time.Now().UTC()}
		if err := s.writeDoc("state.json", s.state); err != nil {
			return nil, err
		}
	}
	if err := s.readDoc("users.json", &s.users); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	cur, err := s.latestRevision()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("store: reading configuration: %w", err)
		}
		cur = Revision{Rev: 1, At: time.Now().UTC(), Actor: "system", Summary: "New installation", Config: config.Default()}
		if err := cur.Config.Validate(); err != nil {
			return nil, err
		}
		if err := s.saveRevision(cur); err != nil {
			return nil, err
		}
	}
	s.cfg, s.rev = cur.Config, cur.Rev
	return s, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

// Config returns a copy of the current configuration and its revision number.
func (s *Store) Config() (config.Config, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Clone(), s.rev
}

// OnChange registers a function called (outside the lock) after every configuration change.
func (s *Store) OnChange(f func(config.Config)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, f)
	s.mu.Unlock()
}

// ErrConflict means the configuration changed since the caller read it.
var ErrConflict = errors.New("store: the configuration was changed by someone else; reload and try again")

// Update applies a change. expectRev, when non-zero, must equal the current revision (optimistic locking,
// so two administrators cannot silently overwrite each other). The change is validated before it is saved.
func (s *Store) Update(actor, summary string, expectRev int, change func(*config.Config) error) (config.Config, int, error) {
	s.mu.Lock()
	if expectRev != 0 && expectRev != s.rev {
		s.mu.Unlock()
		return config.Config{}, 0, ErrConflict
	}
	next := s.cfg.Clone()
	if err := change(&next); err != nil {
		s.mu.Unlock()
		return config.Config{}, 0, err
	}
	next.Schema = config.Schema
	if err := next.Validate(); err != nil {
		s.mu.Unlock()
		return config.Config{}, 0, err
	}
	if s.limit != nil {
		if p := s.limit(&next); len(p) > 0 {
			s.mu.Unlock()
			return config.Config{}, 0, p
		}
	}
	r := Revision{Rev: s.rev + 1, At: time.Now().UTC(), Actor: actor, Summary: summary, Config: next}
	if err := s.saveRevision(r); err != nil {
		s.mu.Unlock()
		return config.Config{}, 0, err
	}
	s.cfg, s.rev = next, r.Rev
	listeners := append([]func(config.Config){}, s.listeners...)
	s.mu.Unlock()
	for _, f := range listeners {
		f(next.Clone())
	}
	return next.Clone(), r.Rev, nil
}

func (s *Store) saveRevision(r Revision) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO revisions (rev, at_ms, actor, summary, body) VALUES (?, ?, ?, ?, ?)`,
		r.Rev, r.At.UnixMilli(), r.Actor, r.Summary, string(b))
	if err != nil && isUniqueViolation(err) {
		return ErrConflict // another server sharing the database saved this revision first
	}
	return err
}

// latestRevision reads the newest revision; os.ErrNotExist on a new database.
func (s *Store) latestRevision() (Revision, error) {
	var r Revision
	var body string
	err := s.db.QueryRow(s.ctx(), `SELECT body FROM revisions ORDER BY rev DESC LIMIT 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return r, os.ErrNotExist
	}
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal([]byte(body), &r)
}

// Sync picks up a configuration another server sharing the database saved, and tells the listeners.
// It reports whether anything changed.
func (s *Store) Sync() bool {
	var rev int
	if s.db.QueryRow(s.ctx(), `SELECT COALESCE(MAX(rev), 0) FROM revisions`).Scan(&rev) != nil {
		return false
	}
	s.mu.Lock()
	if s.db.Postgres {
		// Another server may have changed the users or finished setup.
		var users []User
		if s.readDoc("users.json", &users) == nil {
			s.users = users
		}
		var st State
		if s.readDoc("state.json", &st) == nil {
			s.state = st
		}
	}
	if rev <= s.rev {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	r, err := s.latestRevision()
	if err != nil {
		return false
	}
	s.mu.Lock()
	if r.Rev <= s.rev {
		s.mu.Unlock()
		return false
	}
	s.cfg, s.rev = r.Config, r.Rev
	listeners := append([]func(config.Config){}, s.listeners...)
	s.mu.Unlock()
	for _, f := range listeners {
		f(r.Config.Clone())
	}
	return true
}

func isUniqueViolation(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate key") || strings.Contains(m, "23505")
}

// History lists revisions newest first, without their configurations.
func (s *Store) History(limit int) ([]Revision, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.Query(s.ctx(), `SELECT rev, at_ms, actor, summary FROM revisions ORDER BY rev DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		var at int64
		if err := rows.Scan(&r.Rev, &at, &r.Actor, &r.Summary); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revision returns one revision with its configuration.
func (s *Store) Revision(rev int) (Revision, error) {
	var r Revision
	var body string
	err := s.db.QueryRow(s.ctx(), `SELECT body FROM revisions WHERE rev = ?`, rev).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return r, os.ErrNotExist
	}
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal([]byte(body), &r)
}

// Revert makes an old revision current again, as a new revision.
func (s *Store) Revert(actor string, rev int) (int, error) {
	old, err := s.Revision(rev)
	if err != nil {
		return 0, fmt.Errorf("store: no revision %d", rev)
	}
	_, n, err := s.Update(actor, fmt.Sprintf("Restored revision %d", rev), 0, func(c *config.Config) error {
		*c = old.Config
		return nil
	})
	return n, err
}

// State returns installation state.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// CompleteSetup marks the first-run wizard as finished.
func (s *Store) CompleteSetup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	st.SetupComplete, st.SetupAt = true, time.Now().UTC()
	if err := s.writeDoc("state.json", st); err != nil {
		return err
	}
	s.state = st
	return nil
}

// Users returns the accounts without password hashes.
func (s *Store) Users() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, len(s.users))
	for i, u := range s.users {
		u.Hash, u.TOTPSecret, u.TOTPStep = "", "", 0
		out[i] = u
	}
	return out
}

// SetTOTP stores a user's two-step secret and whether it is on.
func (s *Store) SetTOTP(username, secret string, on bool) error {
	return s.updateUser(username, func(u *User) { u.TOTPSecret, u.TOTP, u.TOTPStep = secret, on, 0 })
}

// UseTOTPStep records the step of an accepted code, refusing one already used (a replayed code).
func (s *Store) UseTOTPStep(username string, step uint64) bool {
	ok := false
	_ = s.updateUser(username, func(u *User) {
		if step > u.TOTPStep {
			u.TOTPStep, ok = step, true
		}
	})
	return ok
}

func (s *Store) updateUser(username string, f func(*User)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := append([]User(nil), s.users...)
	for i := range users {
		if users[i].Username == username {
			f(&users[i])
			if err := s.writeDoc("users.json", users); err != nil {
				return err
			}
			s.users = users
			return nil
		}
	}
	return ErrNotFound
}

// HasUsers reports whether any account exists.
func (s *Store) HasUsers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users) > 0
}

// ErrUserExists means the username is taken.
var ErrUserExists = errors.New("store: that username is taken")

// PutUser creates or updates an account. An empty password keeps the existing one.
func (s *Store) PutUser(u User, password string) error {
	u.Username = strings.ToLower(strings.TrimSpace(u.Username))
	if !usernameRe.MatchString(u.Username) {
		return config.Problems{{Field: "username", Message: "use letters, digits, dots, dashes or underscores"}}
	}
	if u.Role != "admin" && u.Role != "viewer" {
		return config.Problems{{Field: "role", Message: "choose admin or viewer"}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, x := range s.users {
		if x.Username == u.Username {
			idx = i
		}
	}
	if password != "" {
		if err := CheckPassword(password); err != nil {
			return err
		}
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		u.Hash = h
	} else if idx >= 0 {
		u.Hash = s.users[idx].Hash
	} else if u.SSO != "" {
		u.Hash = "" // signs in only through the identity provider
	} else {
		return config.Problems{{Field: "password", Message: "set a password"}}
	}
	if idx >= 0 && s.users[idx].Role == "admin" && u.Role != "admin" {
		admins := 0
		for _, x := range s.users {
			if x.Role == "admin" {
				admins++
			}
		}
		if admins == 1 {
			return config.Problems{{Field: "role", Message: "this is the only administrator; make someone else an administrator first"}}
		}
	}
	users := append([]User(nil), s.users...)
	if idx >= 0 {
		old := s.users[idx]
		u.Created, u.LastLogin = old.Created, old.LastLogin
		u.TOTP, u.TOTPSecret, u.TOTPStep = old.TOTP, old.TOTPSecret, old.TOTPStep
		if u.SSO == "" {
			u.SSO = old.SSO
		}
		users[idx] = u
	} else {
		u.Created = time.Now().UTC()
		users = append(users, u)
	}
	if err := s.writeDoc("users.json", users); err != nil {
		return err
	}
	s.users = users
	return nil
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// DeleteUser removes an account. The last administrator cannot be removed.
func (s *Store) DeleteUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var users []User
	admins := 0
	for _, u := range s.users {
		if u.Username != username {
			users = append(users, u)
			if u.Role == "admin" {
				admins++
			}
		}
	}
	if admins == 0 {
		return errors.New("store: at least one administrator must remain")
	}
	if err := s.writeDoc("users.json", users); err != nil {
		return err
	}
	s.users = users
	return nil
}

// Authenticate checks a username and password and returns the user.
func (s *Store) Authenticate(username, password string) (User, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	s.mu.Lock()
	var found *User
	for i := range s.users {
		if s.users[i].Username == username {
			u := s.users[i]
			found = &u
		}
	}
	s.mu.Unlock()
	if found == nil {
		// Spend the same time as a real check so usernames cannot be discovered by timing.
		_ = VerifyPassword(dummyHash, password)
		return User{}, false
	}
	if !VerifyPassword(found.Hash, password) {
		return User{}, false
	}
	s.mu.Lock()
	for i := range s.users {
		if s.users[i].Username == username {
			s.users[i].LastLogin = time.Now().UTC()
		}
	}
	users := append([]User(nil), s.users...)
	s.mu.Unlock()
	_ = s.writeDoc("users.json", users)
	found.Hash = ""
	return *found, true
}

// User returns one account without its hash.
func (s *Store) User(username string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Username == username {
			u.Hash = ""
			return u, true
		}
	}
	return User{}, false
}

// HashIterations is the PBKDF2-HMAC-SHA256 work factor (OWASP 2023 recommendation).
const HashIterations = 600_000

var dummyHash, _ = HashPassword("not a real password")

// HashPassword returns an encoded PBKDF2-SHA256 hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, HashIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", HashIterations, base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CheckPassword enforces a minimum password strength: 10 characters, at least 5 different ones, and
// not one of the passwords attackers try first. The web interface's meter applies the same rules.
func CheckPassword(p string) error {
	bad := func(m string) error { return config.Problems{{Field: "password", Message: m}} }
	if utf8.RuneCountInString(p) < 10 {
		return bad("use at least 10 characters")
	}
	distinct := map[rune]bool{}
	for _, r := range p {
		distinct[unicode.ToLower(r)] = true
	}
	if len(distinct) < 5 {
		return bad("use more different characters")
	}
	if commonPassword(p) {
		return bad("that password is one attackers try first")
	}
	return nil
}

// commonPassword catches the well-known passwords and the keyboard and counting runs they are made of.
func commonPassword(p string) bool {
	l := strings.ToLower(p)
	for _, w := range []string{"password", "passw0rd", "123456789", "1234567890", "qwertyuiop", "asdfghjkl", "letmein",
		"iloveyou", "administrator", "welcome1", "changeme", "abcdefghij", "bitvoice"} {
		if strings.Contains(l, w) && len(l) < len(w)+4 {
			return true
		}
	}
	return false
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes a file atomically: a temporary file, synced, then renamed over the old one.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'))
}

// writeFile writes bytes atomically: a temporary file, synced, then renamed over the old one.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// SSOUser finds the account linked to an identity provider's subject.
func (s *Store) SSOUser(link string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.SSO == link {
			return u, true
		}
	}
	return User{}, false
}

// TouchLogin records a sign-in time.
func (s *Store) TouchLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := append([]User(nil), s.users...)
	for i := range users {
		if users[i].Username == username {
			users[i].LastLogin = time.Now().UTC()
		}
	}
	if s.writeDoc("users.json", users) == nil {
		s.users = users
	}
}

// stateNames are the small files of live state kept across restarts.
var stateNames = map[string]bool{"hotdesk": true, "divert": true, "guests": true}

// SaveState keeps a piece of live state (hot desk sign-ins) across restarts.
func (s *Store) SaveState(name string, v any) error {
	if !stateNames[name] {
		return fmt.Errorf("store: unknown state %q", name)
	}
	return s.writeDoc(name+".json", v)
}

// LoadState reads what SaveState kept; a missing file leaves v as it is.
func (s *Store) LoadState(name string, v any) error {
	if !stateNames[name] {
		return fmt.Errorf("store: unknown state %q", name)
	}
	err := s.readDoc(name+".json", v)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
