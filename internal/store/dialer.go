package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// DialContact is one number a campaign calls, and where it stands.
type DialContact struct {
	ID       string            `json:"id"`
	Campaign string            `json:"campaign"`
	Number   string            `json:"number"`
	Name     string            `json:"name,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"` // the other columns of the list, shown to the agent
	// State: pending (to call), dialling, retry (to call again from Next), done, failed (out of tries),
	// dnc (on the do-not-call list, or asked not to be called).
	State    string    `json:"state"`
	Next     time.Time `json:"next,omitzero"`
	Attempts int       `json:"attempts"`
	// Result of the last try: answered, machine, abandoned, busy, no-answer, failed, opted-out.
	Result   string    `json:"result,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	LastCall time.Time `json:"lastCall,omitzero"`
	CallID   string    `json:"callId,omitempty"`
}

// ErrNoDialerContact means a campaign has no such contact.
var ErrNoDialerContact = errors.New("store: no such campaign contact")

// PutDialContact saves a contact.
func (s *Store) PutDialContact(c DialContact) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO dialer_contacts (campaign, id, state, next_ms, number, body) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (campaign, id) DO UPDATE SET state = excluded.state, next_ms = excluded.next_ms, number = excluded.number, body = excluded.body`,
		c.Campaign, c.ID, c.State, c.Next.UnixMilli(), c.Number, string(b))
	return err
}

// AddDialContacts saves many contacts at once.
func (s *Store) AddDialContacts(cs []DialContact) error {
	for _, c := range cs {
		if err := s.PutDialContact(c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) dialRows(query string, args ...any) ([]DialContact, error) {
	rows, err := s.db.Query(s.ctx(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DialContact
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var c DialContact
		if json.Unmarshal([]byte(body), &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// DueDialContacts are a campaign's contacts to call now: pending, and retries whose time has come; oldest
// first, at most n.
func (s *Store) DueDialContacts(campaign string, now time.Time, n int) ([]DialContact, error) {
	return s.dialRows(`SELECT body FROM dialer_contacts WHERE campaign = ? AND (state = 'pending' OR (state = 'retry' AND next_ms <= ?))
		ORDER BY next_ms, id LIMIT ?`, campaign, now.UnixMilli(), n)
}

// DialContacts lists a campaign's contacts, optionally in one state, at most limit after offset.
func (s *Store) DialContacts(campaign, state string, offset, limit int) ([]DialContact, error) {
	if state != "" {
		return s.dialRows(`SELECT body FROM dialer_contacts WHERE campaign = ? AND state = ? ORDER BY id LIMIT ? OFFSET ?`, campaign, state, limit, offset)
	}
	return s.dialRows(`SELECT body FROM dialer_contacts WHERE campaign = ? ORDER BY id LIMIT ? OFFSET ?`, campaign, limit, offset)
}

// DialContact reads one.
func (s *Store) DialContactByID(campaign, id string) (DialContact, error) {
	cs, err := s.dialRows(`SELECT body FROM dialer_contacts WHERE campaign = ? AND id = ?`, campaign, id)
	if err != nil {
		return DialContact{}, err
	}
	if len(cs) == 0 {
		return DialContact{}, ErrNoDialerContact
	}
	return cs[0], nil
}

// DialContactCounts counts a campaign's contacts by state, and by the last try's result.
func (s *Store) DialContactCounts(campaign string) (states, results map[string]int, err error) {
	states, results = map[string]int{}, map[string]int{}
	rows, err := s.db.Query(s.ctx(), `SELECT state, COUNT(*) FROM dialer_contacts WHERE campaign = ? GROUP BY state`, campaign)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		states[st] = n
	}
	rows.Close()
	all, err := s.dialRows(`SELECT body FROM dialer_contacts WHERE campaign = ? AND state <> 'pending'`, campaign)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range all {
		if c.Result != "" {
			results[c.Result]++
		}
	}
	return states, results, nil
}

// DeleteDialContacts removes a campaign's contacts (all of them, or those in a state).
func (s *Store) DeleteDialContacts(campaign, state string) error {
	if state != "" {
		_, err := s.db.Exec(s.ctx(), `DELETE FROM dialer_contacts WHERE campaign = ? AND state = ?`, campaign, state)
		return err
	}
	_, err := s.db.Exec(s.ctx(), `DELETE FROM dialer_contacts WHERE campaign = ?`, campaign)
	return err
}

// ResetDialling puts back contacts left dialling (BitVoice stopped mid-call) as retries.
func (s *Store) ResetDialling() error {
	cs, err := s.dialRows(`SELECT body FROM dialer_contacts WHERE state = 'dialling'`)
	if err != nil {
		return err
	}
	for _, c := range cs {
		c.State, c.Next = "retry", time.Now()
		if err := s.PutDialContact(c); err != nil {
			return err
		}
	}
	return nil
}

// DNCEntry is a number never to be called.
type DNCEntry struct {
	Number string    `json:"number"`
	Added  time.Time `json:"added"`
	Reason string    `json:"reason,omitempty"` // asked on a call, imported, added by hand
}

// NormalNumber is how numbers are compared: digits only, the North American leading 1 dropped.
func NormalNumber(n string) string {
	n = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	if len(n) == 11 && n[0] == '1' {
		n = n[1:]
	}
	return n
}

// AddDNC puts numbers on the do-not-call list.
func (s *Store) AddDNC(reason string, numbers ...string) error {
	now := time.Now().UTC()
	for _, n := range numbers {
		n = NormalNumber(n)
		if n == "" {
			continue
		}
		b, _ := json.Marshal(DNCEntry{Number: n, Added: now, Reason: reason})
		if _, err := s.db.Exec(s.ctx(), `INSERT INTO dnc (number, at_ms, body) VALUES (?, ?, ?) ON CONFLICT (number) DO NOTHING`,
			n, now.UnixMilli(), string(b)); err != nil {
			return err
		}
	}
	return nil
}

// RemoveDNC takes a number off the list.
func (s *Store) RemoveDNC(number string) error {
	_, err := s.db.Exec(s.ctx(), `DELETE FROM dnc WHERE number = ?`, NormalNumber(number))
	return err
}

// OnDNC says whether a number is on the do-not-call list.
func (s *Store) OnDNC(number string) bool {
	var n int
	err := s.db.QueryRow(s.ctx(), `SELECT COUNT(*) FROM dnc WHERE number = ?`, NormalNumber(number)).Scan(&n)
	return err == nil && n > 0
}

// DNC lists the do-not-call list, newest first.
func (s *Store) DNC(limit int) ([]DNCEntry, error) {
	rows, err := s.db.Query(s.ctx(), `SELECT body FROM dnc ORDER BY at_ms DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNCEntry
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var e DNCEntry
		if json.Unmarshal([]byte(body), &e) == nil {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}
