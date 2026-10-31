package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AddCDR stores a call record. Saving the same call again replaces it.
func (s *Store) AddCDR(r CDR) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO cdrs (id, start_ms, direction, num_from, num_to, caller_id, recording, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET start_ms = excluded.start_ms, direction = excluded.direction, num_from = excluded.num_from,
		num_to = excluded.num_to, caller_id = excluded.caller_id, recording = excluded.recording, body = excluded.body`,
		r.ID, r.Start.UnixMilli(), r.Direction, r.From, r.To, r.CallerID, r.Recording, string(b))
	return err
}

// CDRQuery filters call records.
type CDRQuery struct {
	Since, Until time.Time
	Search       string // matches from, to, names
	Direction    string
	Limit        int
}

// CDRs returns matching records newest first.
func (s *Store) CDRs(q CDRQuery) ([]CDR, error) {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	q.Limit = min(q.Limit, 100000)
	where, args := []string{"1 = 1"}, []any{}
	if !q.Since.IsZero() {
		where, args = append(where, "start_ms >= ?"), append(args, q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		where, args = append(where, "start_ms <= ?"), append(args, q.Until.UnixMilli())
	}
	if q.Direction != "" {
		where, args = append(where, "direction = ?"), append(args, q.Direction)
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	var out []CDR
	err := s.cdrRows(`SELECT body FROM cdrs WHERE `+strings.Join(where, " AND ")+` ORDER BY start_ms DESC, id DESC`, args, func(r CDR) bool {
		if search != "" && !strings.Contains(strings.ToLower(r.From+" "+r.To+" "+r.FromName+" "+r.ToName+" "+r.AnsweredBy), search) {
			return true
		}
		out = append(out, r)
		return len(out) < q.Limit
	})
	return out, err
}

// cdrRows runs a query whose one column is a record's JSON, calling f until it returns false.
func (s *Store) cdrRows(query string, args []any, f func(CDR) bool) error {
	rows, err := s.db.Query(s.ctx(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return err
		}
		var r CDR
		if json.Unmarshal([]byte(body), &r) == nil && !f(r) {
			return nil
		}
	}
	return rows.Err()
}

// MonthCDRs calls f for every record of a month (YYYY-MM, UTC), oldest first.
func (s *Store) MonthCDRs(month string, f func(CDR)) error {
	m, err := time.Parse("2006-01", month)
	if err != nil {
		return fmt.Errorf("month must be YYYY-MM")
	}
	return s.cdrRows(`SELECT body FROM cdrs WHERE start_ms >= ? AND start_ms < ? ORDER BY start_ms, id`,
		[]any{m.UnixMilli(), m.AddDate(0, 1, 0).UnixMilli()}, func(r CDR) bool { f(r); return true })
}

// CDRsFor lists one number's calls, from it or to it, newest first: a caller's history.
func (s *Store) CDRsFor(number string, limit int) ([]CDR, error) {
	var out []CDR
	err := s.cdrRows(`SELECT body FROM cdrs WHERE num_from = ? OR num_to = ? ORDER BY start_ms DESC LIMIT ?`,
		[]any{number, number, max(limit, 1)}, func(r CDR) bool { out = append(out, r); return true })
	return out, err
}

// CDRsWith lists calls from or to any of the numbers given (the forms one number may be written in)
// since a time, newest first, at most limit.
func (s *Store) CDRsWith(numbers []string, since time.Time, limit int) ([]CDR, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	in := "?" + strings.Repeat(", ?", len(numbers)-1)
	args := []any{}
	for _, n := range numbers {
		args = append(args, n)
	}
	for _, n := range numbers {
		args = append(args, n)
	}
	args = append(args, since.UnixMilli(), max(limit, 1))
	var out []CDR
	err := s.cdrRows(`SELECT body FROM cdrs WHERE (num_from IN (`+in+`) OR num_to IN (`+in+`)) AND start_ms >= ? ORDER BY start_ms DESC LIMIT ?`,
		args, func(r CDR) bool { out = append(out, r); return true })
	return out, err
}

// eachCDR visits every call record.
func (s *Store) eachCDR(f func(CDR)) {
	_ = s.cdrRows(`SELECT body FROM cdrs ORDER BY start_ms`, nil, func(r CDR) bool { f(r); return true })
}

// PruneCDRs deletes call records older than days (0 keeps everything), except those under hold, and
// reports how many went.
func (s *Store) PruneCDRs(days int) int {
	if days <= 0 {
		return 0
	}
	hs := s.activeHolds()
	cut := time.Now().AddDate(0, 0, -days)
	var ids []string
	_ = s.cdrRows(`SELECT body FROM cdrs WHERE start_ms < ?`, []any{cut.UnixMilli()}, func(r CDR) bool {
		if !heldBy(hs, r.Start, r.From, r.To, r.AnsweredBy) {
			ids = append(ids, r.ID)
		}
		return true
	})
	gone := 0
	for len(ids) > 0 {
		n := min(len(ids), 500)
		args := make([]any, n)
		for i := range n {
			args[i] = ids[i]
		}
		res, err := s.db.Exec(s.ctx(), `DELETE FROM cdrs WHERE id IN (?`+strings.Repeat(", ?", n-1)+`)`, args...)
		if err != nil {
			break
		}
		k, _ := res.RowsAffected()
		gone += int(k)
		ids = ids[n:]
	}
	return gone
}

// Audit adds an entry to the audit log.
func (s *Store) Audit(e AuditEntry) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = s.db.Exec(s.ctx(), `INSERT INTO audit (id, at_ms, actor, action, body) VALUES (?, ?, ?, ?, ?)`,
		randomHex(10), e.At.UnixMilli(), e.Actor, e.Action, string(b))
}

// AuditLog returns the newest entries first.
func (s *Store) AuditLog(limit int) []AuditEntry {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.Query(s.ctx(), `SELECT body FROM audit ORDER BY at_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var body string
		var e AuditEntry
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// importCDRs brings the monthly call record files in.
func (s *Store) importCDRs() ([]string, error) {
	files, _ := filepath.Glob(filepath.Join(s.dir, "cdr", "*.jsonl"))
	if len(files) == 0 {
		return nil, nil
	}
	sort.Strings(files)
	for _, f := range files {
		var err error
		if e := jsonLines(f, func(b []byte) {
			var r CDR
			if json.Unmarshal(b, &r) == nil && r.ID != "" && err == nil {
				err = s.AddCDR(r)
			}
		}); e != nil {
			return nil, e
		}
		if err != nil {
			return nil, err
		}
	}
	return []string{"cdr"}, nil
}

// importAudit brings the audit log file in.
func (s *Store) importAudit() ([]string, error) {
	p := filepath.Join(s.dir, "audit.jsonl")
	if _, err := os.Stat(p); err != nil {
		return nil, nil
	}
	if err := jsonLines(p, func(b []byte) {
		var e AuditEntry
		if json.Unmarshal(b, &e) == nil {
			s.Audit(e)
		}
	}); err != nil {
		return nil, err
	}
	return []string{"audit.jsonl"}, nil
}

// importRevisions brings the saved configurations in, and reports how many.
func (s *Store) importRevisions() (int, error) {
	files, _ := filepath.Glob(filepath.Join(s.dir, "revisions", "*.json"))
	n := 0
	for _, f := range files {
		var r Revision
		if readJSON(f, &r) != nil || r.Rev == 0 {
			continue
		}
		if err := s.saveRevision(r); err != nil {
			return n, err
		}
		n++
	}
	// config.json is the newest revision; an old installation may have only that.
	var r Revision
	if readJSON(filepath.Join(s.dir, "config.json"), &r) == nil && r.Rev > 0 {
		switch err := s.saveRevision(r); {
		case err == nil:
			n++
		case err != ErrConflict: // already in, from the revision files
			return n, err
		}
	}
	return n, nil
}
