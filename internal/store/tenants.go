package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Tenant is one organisation hosted on this BitVoice: its own extensions, trunks, numbers, users and
// data, kept in its own folder (and database), and reached by its own SIP domain and web address.
type Tenant struct {
	ID   string `json:"id"` // short name: letters, digits and dashes; part of its web address /t/<id>/
	Name string `json:"name"`
	// Domain is the tenant's own SIP domain and web host (pbx.acme.example). Phones registering to it,
	// and browsers opening it, reach this tenant. Empty: phones sign in as <id>.<extension>.
	Domain    string       `json:"domain"`
	Suspended bool         `json:"suspended"` // only emergency calls go through; nobody can sign in
	Limits    TenantLimits `json:"limits"`
	Note      string       `json:"note"`
	Created   time.Time    `json:"created"`
}

// TenantLimits caps what a tenant may use; 0 is no limit.
type TenantLimits struct {
	Extensions int `json:"extensions"`
	Calls      int `json:"calls"`     // calls at once; emergency calls are never refused
	StorageMB  int `json:"storageMB"` // recordings, voicemail, faxes and greetings
	AIPerDay   int `json:"aiPerDay"`  // AI requests a day (transcripts, summaries, receptionist turns)
}

var (
	tenantIDRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	tenantDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	// reservedTenantIDs would collide with paths or words BitVoice uses.
	reservedTenantIDs = []string{"api", "admin", "system", "default", "root", "www", "static", "assets", "ari", "lost", "hooks", "provision", "call"}
)

// ErrTenantExists is a tenant short name or domain already in use.
var ErrTenantExists = errors.New("store: a tenant with that short name or domain already exists")

// Tenants lists the hosted tenants, by name.
func (s *Store) Tenants() []Tenant {
	var ts []Tenant
	if err := s.readDoc("tenants.json", &ts); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	slices.SortFunc(ts, func(a, b Tenant) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return ts
}

// CheckTenant says what is wrong with a tenant, field by field.
func CheckTenant(t *Tenant) config.Problems {
	var p config.Problems
	t.ID, t.Name, t.Domain = strings.ToLower(strings.TrimSpace(t.ID)), strings.TrimSpace(t.Name), strings.ToLower(strings.TrimSpace(t.Domain))
	switch {
	case !tenantIDRe.MatchString(t.ID):
		p = append(p, config.Problem{Field: "id", Message: "3 to 32 lowercase letters, digits and dashes, starting with a letter"})
	case slices.Contains(reservedTenantIDs, t.ID):
		p = append(p, config.Problem{Field: "id", Message: t.ID + " is a word BitVoice uses itself; choose another"})
	}
	if t.Name == "" {
		p = append(p, config.Problem{Field: "name", Message: "enter the organisation's name"})
	}
	if t.Domain != "" && !tenantDomainRe.MatchString(t.Domain) {
		p = append(p, config.Problem{Field: "domain", Message: "a domain name, such as pbx.acme.example"})
	}
	l := t.Limits
	for _, v := range []struct {
		f string
		n int
	}{{"limits.extensions", l.Extensions}, {"limits.calls", l.Calls}, {"limits.storageMB", l.StorageMB}, {"limits.aiPerDay", l.AIPerDay}} {
		if v.n < 0 {
			p = append(p, config.Problem{Field: v.f, Message: "0 (no limit) or more"})
		}
	}
	return p
}

// PutTenant adds or replaces a tenant (matched by ID).
func (s *Store) PutTenant(t Tenant) (Tenant, error) {
	if p := CheckTenant(&t); len(p) > 0 {
		return t, p
	}
	s.tenantMu.Lock()
	defer s.tenantMu.Unlock()
	ts := s.Tenants()
	found := false
	for i, x := range ts {
		if x.ID != t.ID && t.Domain != "" && strings.EqualFold(x.Domain, t.Domain) {
			return t, ErrTenantExists
		}
		if x.ID == t.ID {
			t.Created = x.Created
			ts[i] = t
			found = true
		}
	}
	if !found {
		t.Created = time.Now().UTC()
		ts = append(ts, t)
	}
	return t, s.writeDoc("tenants.json", ts)
}

// AddTenant adds a new tenant; ErrTenantExists when its ID is taken.
func (s *Store) AddTenant(t Tenant) (Tenant, error) {
	for _, x := range s.Tenants() {
		if x.ID == strings.ToLower(strings.TrimSpace(t.ID)) {
			return t, ErrTenantExists
		}
	}
	return s.PutTenant(t)
}

// DeleteTenant removes a tenant from the list (its folder is left for the caller to remove).
func (s *Store) DeleteTenant(id string) error {
	s.tenantMu.Lock()
	defer s.tenantMu.Unlock()
	ts := s.Tenants()
	kept := ts[:0]
	for _, x := range ts {
		if x.ID != id {
			kept = append(kept, x)
		}
	}
	if len(kept) == len(ts) {
		return ErrNotFound
	}
	return s.writeDoc("tenants.json", kept)
}

// Spec is the database address the store was opened with (a SQLite path or a PostgreSQL address).
func (s *Store) Spec() string { return s.spec }

// TenantDir is where a tenant's data lives inside the main data folder.
func TenantDir(dataDir, id string) string { return filepath.Join(dataDir, "tenants", id) }

// SetLimit installs a check every configuration change must pass (a tenant's limits); nil removes it.
func (s *Store) SetLimit(f func(*config.Config) config.Problems) {
	s.mu.Lock()
	s.limit = f
	s.mu.Unlock()
}

// SetQuota caps the audio and fax files kept (bytes); 0 is no cap.
func (s *Store) SetQuota(bytes int64) {
	s.quota.Lock()
	s.quota.max = bytes
	s.quota.at = time.Time{}
	s.quota.Unlock()
}

// ErrQuota is a write refused because the tenant's storage is full.
var ErrQuota = errors.New("store: the storage allowance is used up; delete old recordings or ask for more")

type quota struct {
	sync.Mutex
	max  int64
	used int64
	at   time.Time // when used was last measured
}

// roomFor says whether n more bytes fit in the storage allowance. The folder is measured at most
// once a minute; writes in between are added as they happen.
func (s *Store) roomFor(n int) error {
	s.quota.Lock()
	defer s.quota.Unlock()
	if s.quota.max <= 0 {
		return nil
	}
	if time.Since(s.quota.at) > time.Minute {
		s.quota.used = s.audioBytes()
		s.quota.at = time.Now()
	}
	if s.quota.used+int64(n) > s.quota.max {
		return ErrQuota
	}
	s.quota.used += int64(n)
	return nil
}

// StorageUsed is the bytes of audio and faxes kept.
func (s *Store) StorageUsed() int64 { return s.audioBytes() }

func (s *Store) audioBytes() int64 {
	var total int64
	for _, sub := range []string{"recordings", "voicemail", "faxes"} {
		_ = filepath.WalkDir(filepath.Join(s.dir, sub), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					total += fi.Size()
				}
			}
			return nil
		})
	}
	return total
}
