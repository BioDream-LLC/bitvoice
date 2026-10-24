package store

import (
	"encoding/json"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/facts"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// CallTranscript is what the AI made of a recorded call: who said what and when, a summary and the
// action items. It is kept next to the recording (recordings/<name>.json) and goes when the recording
// goes, so retention and legal holds cover it too.
type CallTranscript struct {
	Recording string    `json:"recording"`
	CallID    string    `json:"callId"`
	At        time.Time `json:"at"` // when the call started
	From      string    `json:"from"`
	FromName  string    `json:"fromName"`
	To        string    `json:"to"`
	ToName    string    `json:"toName"`
	// Status is pending while it is worked on, then done or failed (Error says why).
	Status      string           `json:"status"`
	Error       string           `json:"error,omitempty"`
	Lines       []TranscriptLine `json:"lines"`
	Summary     string           `json:"summary"`
	ActionItems []string         `json:"actionItems"`
	// Facts are what the call said, read by rules with no language model, each with its sentence;
	// SummaryBy says who wrote Summary: "model" or "facts".
	Facts     []facts.Fact `json:"facts,omitempty"`
	SummaryBy string       `json:"summaryBy,omitempty"`
	Done      time.Time    `json:"done,omitzero"`
	// Talk is measured from the recording's sound; Insight is the AI's notes for a supervisor.
	Talk    *media.Talk `json:"talk,omitempty"`
	Insight *ai.Insight `json:"insight,omitempty"`
	// AnsweredExt is the extension that took the call, for insight by agent.
	AnsweredExt string `json:"answeredExt,omitempty"`
	// Coaching is what supervisors wrote about the call, each note at a moment in it.
	Coaching []CoachingNote `json:"coaching,omitempty"`
}

// CoachingNote is a supervisor's note on a moment of a recorded call.
type CoachingNote struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Second float64   `json:"second"`
	Note   string    `json:"note"`
}

// TimedText is the transcript with each line's second, for the insight model.
func (t CallTranscript) TimedText() string {
	var b strings.Builder
	for _, l := range t.Lines {
		who := t.FromName
		if who == "" {
			who = t.From
		}
		if l.Side == "callee" {
			who = t.ToName
			if who == "" {
				who = t.To
			}
		}
		fmt.Fprintf(&b, "[%ds] %s: %s\n", int(l.Second), who, l.Text)
	}
	return b.String()
}

// Transcripts returns every finished transcript of calls since a time, newest first.
func (s *Store) Transcripts(since time.Time) []CallTranscript {
	return s.transcriptRows(`SELECT body FROM transcripts WHERE at_ms >= ? ORDER BY at_ms DESC`, since.UnixMilli())
}

func (s *Store) transcriptRows(query string, args ...any) []CallTranscript {
	rows, err := s.db.Query(s.ctx(), query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CallTranscript
	for rows.Next() {
		var body string
		var t CallTranscript
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &t) == nil && t.Status == "done" {
			out = append(out, t)
		}
	}
	return out
}

// TranscriptLine is one stretch of speech, Second seconds into the recording.
type TranscriptLine struct {
	Second float64 `json:"second"`
	Side   string  `json:"side"` // caller (the recording's left channel) or callee (the right)
	Text   string  `json:"text"`
}

// Text is the whole transcript, one line per stretch of speech, with names.
func (t CallTranscript) Text() string {
	var b strings.Builder
	for _, l := range t.Lines {
		who := firstOf(t.FromName, t.From, "Caller")
		if l.Side == "callee" {
			who = firstOf(t.ToName, t.To, "Callee")
		}
		b.WriteString(who + ": " + l.Text + "\n")
	}
	return b.String()
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *Store) transcriptPath(name string) (string, bool) {
	if !recordingName.MatchString(name) || strings.Contains(name, "..") {
		return "", false
	}
	return filepath.Join(s.dir, "recordings", name+".json"), true
}

// SetCallTranscript stores (or replaces) a recording's transcript. The recording must exist.
func (s *Store) SetCallTranscript(t CallTranscript) error {
	p, ok := s.transcriptPath(t.Recording)
	if !ok {
		return ErrNotFound
	}
	if _, err := os.Stat(strings.TrimSuffix(p, ".json") + ".wav"); err != nil {
		return ErrNotFound
	}
	if t.Lines == nil {
		t.Lines = []TranscriptLine{}
	}
	if t.ActionItems == nil {
		t.ActionItems = []string{}
	}
	return s.putTranscript(t)
}

// putTranscript saves a transcript with the text it is searched by.
func (s *Store) putTranscript(t CallTranscript) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO transcripts (name, at_ms, search, body) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET at_ms = excluded.at_ms, search = excluded.search, body = excluded.body`,
		t.Recording, t.At.UnixMilli(), searchText(t), string(b))
	return err
}

func searchText(t CallTranscript) string {
	return strings.ToLower(t.Text() + "\n" + t.Summary + "\n" + strings.Join(t.ActionItems, "\n") + "\n" + t.FromName + " " + t.ToName)
}

// deleteTranscript removes a recording's transcript with the recording.
func (s *Store) deleteTranscript(name string) {
	_, _ = s.db.Exec(s.ctx(), `DELETE FROM transcripts WHERE name = ?`, name)
}

// CallTranscript returns a recording's transcript, or ErrNotFound when it has none.
func (s *Store) CallTranscript(name string) (CallTranscript, error) {
	var t CallTranscript
	if _, ok := s.transcriptPath(name); !ok {
		return t, ErrNotFound
	}
	var body string
	if s.db.QueryRow(s.ctx(), `SELECT body FROM transcripts WHERE name = ?`, name).Scan(&body) != nil {
		return t, ErrNotFound
	}
	return t, json.Unmarshal([]byte(body), &t)
}

// SearchTranscripts finds calls by what was said: every word of q must be in the transcript, the
// summary or the action items (any case). Newest first, at most limit.
func (s *Store) SearchTranscripts(q string, limit int) []CallTranscript {
	words := strings.Fields(strings.ToLower(q))
	where, args := []string{"1 = 1"}, []any{}
	for _, w := range words {
		where, args = append(where, `search LIKE ? ESCAPE '\'`), append(args, "%"+likeEscape(w)+"%")
	}
	out := s.transcriptRows(`SELECT body FROM transcripts WHERE `+strings.Join(where, " AND ")+` ORDER BY at_ms DESC`, args...)
	slices.SortFunc(out, func(a, b CallTranscript) int { return b.At.Compare(a.At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// importTranscripts brings the transcripts kept beside recordings in.
func (s *Store) importTranscripts() ([]string, error) {
	var moved []string
	err := filepath.WalkDir(filepath.Join(s.dir, "recordings"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		var t CallTranscript
		if readJSON(p, &t) != nil || t.Recording == "" {
			return nil
		}
		if err := s.putTranscript(t); err != nil {
			return err
		}
		r, _ := filepath.Rel(s.dir, p)
		moved = append(moved, r)
		return nil
	})
	return moved, err
}
