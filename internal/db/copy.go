package db

import (
	"context"
	"fmt"
	"strings"
)

// tables lists every table with its columns, in an order that respects nothing but itself (there are
// no foreign keys between them).
var tables = []struct{ name, cols string }{
	{"docs", "name, body, updated"},
	{"revisions", "rev, at_ms, actor, summary, body"},
	{"cdrs", "id, start_ms, direction, num_from, num_to, caller_id, recording, body"},
	{"audit", "id, at_ms, actor, action, body"},
	{"texts", "id, at_ms, our, other, body"},
	{"transcripts", "name, at_ms, search, body"},
}

// Counts says how many rows each table holds.
func (d *DB) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, t := range tables {
		var n int
		if err := d.QueryRow(ctx, `SELECT COUNT(*) FROM `+t.name).Scan(&n); err != nil {
			return nil, err
		}
		out[t.name] = n
	}
	return out, nil
}

// Empty says whether a database holds nothing yet.
func (d *DB) Empty(ctx context.Context) (bool, error) {
	c, err := d.Counts(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range c {
		if n > 0 {
			return false, nil
		}
	}
	return true, nil
}

// CopyTo copies everything into dst, which must be empty, in one transaction: it all arrives or none
// of it does. progress, when set, hears each table as it is done.
func (d *DB) CopyTo(ctx context.Context, dst *DB, progress func(table string, rows int)) error {
	empty, err := dst.Empty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("db: %s already holds data: use an empty database", dst.Where)
	}
	return dst.Tx(ctx, func(tx *Tx) error {
		for _, t := range tables {
			n := len(strings.Split(t.cols, ","))
			ins := `INSERT INTO ` + t.name + ` (` + t.cols + `) VALUES (?` + strings.Repeat(", ?", n-1) + `)`
			rows, err := d.Query(ctx, `SELECT `+t.cols+` FROM `+t.name)
			if err != nil {
				return err
			}
			count := 0
			for rows.Next() {
				vals := make([]any, n)
				ptrs := make([]any, n)
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					rows.Close()
					return err
				}
				for i, v := range vals {
					if b, ok := v.([]byte); ok {
						vals[i] = string(b) // SQLite hands text back as bytes; PostgreSQL wants text
					}
				}
				if _, err := tx.Exec(ctx, ins, vals...); err != nil {
					rows.Close()
					return fmt.Errorf("db: copying %s: %w", t.name, err)
				}
				count++
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if progress != nil {
				progress(t.name, count)
			}
		}
		return nil
	})
}
