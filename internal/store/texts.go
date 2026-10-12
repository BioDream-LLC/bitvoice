package store

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Text is one text message, sent or received, on one of our numbers.
type Text struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Direction string    `json:"direction"` // in or out
	Our       string    `json:"our"`       // our number
	Other     string    `json:"other"`     // theirs
	Body      string    `json:"body"`
	// Status is received, sent or failed.
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	By         string `json:"by,omitempty"` // who sent it
	ProviderID string `json:"providerId,omitempty"`
}

// Conversation is the latest of the texts between one of our numbers and another.
type Conversation struct {
	Our    string `json:"our"`
	Other  string `json:"other"`
	Last   Text   `json:"last"`
	Count  int    `json:"count"`
	Unread int    `json:"unread"`
	// OptedOut is true when they replied STOP: no more texts may be sent to them from Our.
	OptedOut bool `json:"optedOut"`
}

type textState struct {
	Read     map[string]time.Time `json:"read"`     // pair -> read up to
	OptedOut map[string]bool      `json:"optedOut"` // pair -> STOP received
}

func pairKey(our, other string) string { return our + "|" + other }

func (s *Store) textsDir() string { return filepath.Join(s.dir, "texts") }

func (s *Store) loadTextState() textState {
	st := textState{Read: map[string]time.Time{}, OptedOut: map[string]bool{}}
	_ = s.readDoc(filepath.Join(s.textsDir(), "state.json"), &st)
	if st.Read == nil {
		st.Read = map[string]time.Time{}
	}
	if st.OptedOut == nil {
		st.OptedOut = map[string]bool{}
	}
	return st
}

// AddText stores a text and, for one received, handles STOP and START (opt out and back in).
func (s *Store) AddText(t Text) (Text, error) {
	s.textMu.Lock()
	defer s.textMu.Unlock()
	if t.ID == "" {
		t.ID = config.NewID()
	}
	if t.At.IsZero() {
		t.At = time.Now().UTC()
	}
	b, _ := json.Marshal(t)
	if _, err := s.db.Exec(s.ctx(), `INSERT INTO texts (id, at_ms, our, other, body) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET body = excluded.body`, t.ID, t.At.UnixMilli(), t.Our, t.Other, string(b)); err != nil {
		return t, err
	}
	if t.Direction == "in" {
		word := strings.ToUpper(strings.TrimSpace(t.Body))
		st := s.loadTextState()
		switch word {
		case "STOP", "STOPALL", "UNSUBSCRIBE", "CANCEL", "END", "QUIT":
			st.OptedOut[pairKey(t.Our, t.Other)] = true
		case "START", "UNSTOP", "YES":
			delete(st.OptedOut, pairKey(t.Our, t.Other))
		default:
			return t, nil
		}
		return t, s.writeDoc(filepath.Join(s.textsDir(), "state.json"), st)
	}
	return t, nil
}

// Texts returns every text, oldest first (the last 12 months).
func (s *Store) Texts() []Text {
	s.textMu.Lock()
	defer s.textMu.Unlock()
	return s.textRows(`SELECT body FROM texts WHERE at_ms >= ? ORDER BY at_ms, id`, time.Now().AddDate(-1, 0, 0).UnixMilli())
}

// Conversations lists each pair of numbers with its latest text, newest first.
func (s *Store) Conversations() []Conversation {
	all := s.Texts()
	s.textMu.Lock()
	st := s.loadTextState()
	s.textMu.Unlock()
	by := map[string]*Conversation{}
	for _, t := range all {
		k := pairKey(t.Our, t.Other)
		c, ok := by[k]
		if !ok {
			c = &Conversation{Our: t.Our, Other: t.Other, OptedOut: st.OptedOut[k]}
			by[k] = c
		}
		c.Last = t
		c.Count++
		if t.Direction == "in" && t.At.After(st.Read[k]) {
			c.Unread++
		}
	}
	out := make([]Conversation, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Conversation) int { return b.Last.At.Compare(a.Last.At) })
	return out
}

// Thread returns the texts between two numbers, oldest first.
func (s *Store) Thread(our, other string) []Text {
	var out []Text
	for _, t := range s.textRows(`SELECT body FROM texts WHERE our = ? ORDER BY at_ms, id`, our) {
		if config.SameNumber(t.Other, other) {
			out = append(out, t)
		}
	}
	return out
}

// MarkRead marks a conversation read up to now.
func (s *Store) MarkRead(our, other string) error {
	s.textMu.Lock()
	defer s.textMu.Unlock()
	st := s.loadTextState()
	st.Read[pairKey(our, other)] = time.Now().UTC()
	return s.writeDoc(filepath.Join(s.textsDir(), "state.json"), st)
}

// OptedOut says whether other replied STOP to our.
func (s *Store) OptedOut(our, other string) bool {
	s.textMu.Lock()
	defer s.textMu.Unlock()
	return s.loadTextState().OptedOut[pairKey(our, other)]
}

func (s *Store) textRows(query string, args ...any) []Text {
	rows, err := s.db.Query(s.ctx(), query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Text
	for rows.Next() {
		var body string
		var t Text
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &t) == nil {
			out = append(out, t)
		}
	}
	return out
}

// importTexts brings the monthly text files in.
func (s *Store) importTexts() ([]string, error) {
	files, _ := filepath.Glob(filepath.Join(s.textsDir(), "*.jsonl"))
	if len(files) == 0 {
		return nil, nil
	}
	var moved []string
	for _, f := range files {
		var err error
		if e := jsonLines(f, func(b []byte) {
			var t Text
			if json.Unmarshal(b, &t) == nil && t.ID != "" && err == nil {
				_, err = s.db.Exec(s.ctx(), `INSERT INTO texts (id, at_ms, our, other, body) VALUES (?, ?, ?, ?, ?)
					ON CONFLICT (id) DO NOTHING`, t.ID, t.At.UnixMilli(), t.Our, t.Other, string(b))
			}
		}); e != nil {
			return nil, e
		}
		if err != nil {
			return nil, err
		}
		r, _ := filepath.Rel(s.dir, f)
		moved = append(moved, r)
	}
	return moved, nil
}
