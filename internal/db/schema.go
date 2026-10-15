package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// migrations bring the schema up to date, in order; each runs once. Each record keeps its whole JSON
// in body, so a new field needs no migration; columns exist only to find and sort records quickly.
// Every statement works on both SQLite and PostgreSQL.
var migrations = []string{
	`CREATE TABLE docs (name TEXT PRIMARY KEY, body TEXT NOT NULL, updated BIGINT NOT NULL);
	CREATE TABLE revisions (rev BIGINT PRIMARY KEY, at_ms BIGINT NOT NULL, actor TEXT NOT NULL, summary TEXT NOT NULL, body TEXT NOT NULL);
	CREATE TABLE cdrs (id TEXT PRIMARY KEY, start_ms BIGINT NOT NULL, direction TEXT NOT NULL, num_from TEXT NOT NULL,
		num_to TEXT NOT NULL, caller_id TEXT NOT NULL, recording TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX cdrs_start ON cdrs (start_ms);
	CREATE INDEX cdrs_from ON cdrs (num_from, start_ms);
	CREATE INDEX cdrs_to ON cdrs (num_to, start_ms);
	CREATE INDEX cdrs_caller ON cdrs (caller_id, start_ms);
	CREATE TABLE audit (id TEXT PRIMARY KEY, at_ms BIGINT NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX audit_at ON audit (at_ms);
	CREATE TABLE texts (id TEXT PRIMARY KEY, at_ms BIGINT NOT NULL, our TEXT NOT NULL, other TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX texts_pair ON texts (our, other, at_ms);
	CREATE INDEX texts_at ON texts (at_ms);
	CREATE TABLE transcripts (name TEXT PRIMARY KEY, at_ms BIGINT NOT NULL, search TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX transcripts_at ON transcripts (at_ms);`,
	// Servers sharing one PostgreSQL database: who is alive, and which phones each one holds.
	`CREATE TABLE nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL, sip_addr TEXT NOT NULL, version TEXT NOT NULL,
		started_ms BIGINT NOT NULL, seen_ms BIGINT NOT NULL, info TEXT NOT NULL);
	CREATE TABLE registrations (node TEXT NOT NULL, ext TEXT NOT NULL, contact TEXT NOT NULL, body TEXT NOT NULL,
		expires_ms BIGINT NOT NULL, PRIMARY KEY (node, ext, contact));
	CREATE INDEX registrations_ext ON registrations (ext);`,
	// Connected calls, saved so another server can carry them on if theirs stops.
	`CREATE TABLE live_calls (id TEXT PRIMARY KEY, node TEXT NOT NULL, body TEXT NOT NULL, updated_ms BIGINT NOT NULL);
	CREATE INDEX live_calls_node ON live_calls (node);`,
	// The radio console's log: each transmission on a channel, heard or sent, with its recording.
	`CREATE TABLE radio_log (id TEXT PRIMARY KEY, start_ms BIGINT NOT NULL, channel TEXT NOT NULL, unit TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX radio_log_start ON radio_log (start_ms);
	CREATE INDEX radio_log_channel ON radio_log (channel, start_ms);`,
	// The outbound dialer: each campaign's contacts and where each stands, and the do-not-call list.
	`CREATE TABLE dialer_contacts (campaign TEXT NOT NULL, id TEXT NOT NULL, state TEXT NOT NULL, next_ms BIGINT NOT NULL,
		number TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY (campaign, id));
	CREATE INDEX dialer_due ON dialer_contacts (campaign, state, next_ms);
	CREATE TABLE dnc (number TEXT PRIMARY KEY, at_ms BIGINT NOT NULL, body TEXT NOT NULL);`,
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	return d.Tx(ctx, func(tx *Tx) error {
		if d.Postgres {
			// Two servers starting at once must not both migrate.
			if _, err := tx.Exec(ctx, `LOCK TABLE schema_version IN EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		var v int
		err := tx.QueryRow(ctx, `SELECT version FROM schema_version`).Scan(&v)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
				return err
			}
		case err != nil:
			return err
		}
		if v > len(migrations) {
			return fmt.Errorf("db: the database was made by a newer BitVoice (schema %d, this one knows %d): upgrade BitVoice", v, len(migrations))
		}
		for i := v; i < len(migrations); i++ {
			if _, err := tx.Exec(ctx, migrations[i]); err != nil {
				return fmt.Errorf("db: schema step %d: %w", i+1, err)
			}
		}
		_, err = tx.Exec(ctx, `UPDATE schema_version SET version = ?`, len(migrations))
		return err
	})
}

// GetDoc reads a document (a small JSON list or record); ok is false when there is none.
func (d *DB) GetDoc(ctx context.Context, name string) (body []byte, ok bool, err error) {
	err = d.QueryRow(ctx, `SELECT body FROM docs WHERE name = ?`, name).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return body, err == nil, err
}

// PutDoc writes a document whole.
func (d *DB) PutDoc(ctx context.Context, name string, body []byte) error {
	_, err := d.Exec(ctx, `INSERT INTO docs (name, body, updated) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET body = excluded.body, updated = excluded.updated`, name, string(body), time.Now().UnixMilli())
	return err
}

// DeleteDoc removes a document.
func (d *DB) DeleteDoc(ctx context.Context, name string) error {
	_, err := d.Exec(ctx, `DELETE FROM docs WHERE name = ?`, name)
	return err
}

// MutateDoc reads a document, lets f change it and writes it back, all in one transaction with the
// row locked: two servers on one PostgreSQL never lose each other's change. f gets nil when the
// document does not exist yet; returning nil from f leaves it as it was.
func (d *DB) MutateDoc(ctx context.Context, name string, f func(old []byte) ([]byte, error)) error {
	return d.Tx(ctx, func(tx *Tx) error {
		var old []byte
		err := tx.QueryRow(ctx, `SELECT body FROM docs WHERE name = ?`+tx.ForUpdate(), name).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := f(old)
		if err != nil || body == nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO docs (name, body, updated) VALUES (?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET body = excluded.body, updated = excluded.updated`, name, string(body), time.Now().UnixMilli())
		return err
	})
}

// DocNames lists documents whose names start with prefix.
func (d *DB) DocNames(ctx context.Context, prefix string) ([]string, error) {
	rows, err := d.Query(ctx, `SELECT name FROM docs WHERE name LIKE ? ESCAPE '\' ORDER BY name`, escapeLike(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
