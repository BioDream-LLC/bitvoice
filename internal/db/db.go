// Package db is BitVoice's own database: SQLite in one file by default, or PostgreSQL when several
// servers share one or reporting tools need to reach it. Both run the same schema and the same
// queries, written once with ? placeholders.
//
// SQLite is opened the way a server needs it: one connection for writes and several for reads. SQLite
// in WAL mode lets readers run alongside the one writer, but Go's pool sits above SQLite, so a pool of
// one would make a long call-history report hold the connection every new call record waits for.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"; pure Go
	_ "modernc.org/sqlite"             // registers "sqlite"; pure Go, so BitVoice stays one static binary
)

// DB is an open database.
type DB struct {
	// Write takes the writes (one connection on SQLite, which allows one writer anyway).
	Write *sql.DB
	// Read takes the reports; on PostgreSQL it is the same pool as Write.
	Read *sql.DB
	// Postgres is true on PostgreSQL, false on SQLite.
	Postgres bool
	// Where says where the data is, for people: a file path, or a PostgreSQL host and database
	// without its password.
	Where string
}

// Open opens the database named by spec: a PostgreSQL URL (postgres://user:pass@host/db), or a
// SQLite file path (":memory:" for tests). The schema is created or brought up to date.
func Open(spec string) (*DB, error) {
	var d *DB
	var err error
	if strings.HasPrefix(spec, "postgres://") || strings.HasPrefix(spec, "postgresql://") {
		d, err = openPostgres(spec)
	} else {
		d, err = openSQLite(spec)
	}
	if err != nil {
		return nil, err
	}
	if err := d.migrate(context.Background()); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func openPostgres(spec string) (*DB, error) {
	u, err := url.Parse(spec)
	if err != nil || u.Host == "" {
		return nil, errors.New("db: the PostgreSQL address should look like postgres://user:password@host:5432/bitvoice")
	}
	pool, err := sql.Open("pgx", spec)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(16)
	pool.SetMaxIdleConns(4)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, pgProblem(u, err)
	}
	return &DB{Write: pool, Read: pool, Postgres: true, Where: "PostgreSQL at " + u.Host + u.Path}, nil
}

func openSQLite(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("db: no database file")
	}
	if path == ":memory:" {
		// One connection, or each would see its own empty database.
		p, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
		if err != nil {
			return nil, err
		}
		p.SetMaxOpenConns(1)
		p.SetConnMaxLifetime(0)
		return &DB{Write: p, Read: p, Where: "memory"}, nil
	}
	dsn := func(readOnly bool) string {
		q := url.Values{}
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "busy_timeout(5000)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "synchronous(NORMAL)") // safe with WAL: a power cut loses at most the last moment, never the file
		if readOnly {
			q.Set("mode", "ro")
		}
		return "file:" + path + "?" + q.Encode()
	}
	w, err := sql.Open("sqlite", dsn(false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxLifetime(0)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("db: cannot open %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", dsn(true))
	if err != nil {
		w.Close()
		return nil, err
	}
	n := min(max(runtime.NumCPU(), 2), 8)
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	r.SetConnMaxLifetime(0)
	return &DB{Write: w, Read: r, Where: path}, nil
}

// Close closes both pools.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	err := d.Write.Close()
	if d.Read != d.Write {
		if e := d.Read.Close(); err == nil {
			err = e
		}
	}
	return err
}

// Q turns a query written with ? placeholders into the database's own form ($1, $2 on PostgreSQL).
// A ? inside a quoted string is left alone.
func (d *DB) Q(query string) string {
	if !d.Postgres || !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	n, quoted := 0, false
	for _, c := range query {
		switch {
		case c == '\'':
			quoted = !quoted
		case c == '?' && !quoted:
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Exec runs a write.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.Write.ExecContext(ctx, d.Q(query), args...)
}

// Query runs a read on the read pool.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.Read.QueryContext(ctx, d.Q(query), args...)
}

// QueryRow runs a one-row read on the read pool.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.Read.QueryRowContext(ctx, d.Q(query), args...)
}

// Tx runs f in a write transaction, committed when f returns nil.
func (d *DB) Tx(ctx context.Context, f func(*Tx) error) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(&Tx{tx: tx, d: d}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Tx is a write transaction that takes ? placeholders too.
type Tx struct {
	tx *sql.Tx
	d  *DB
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.d.Q(query), args...)
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.d.Q(query), args...)
}

func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.d.Q(query), args...)
}

// ForUpdate is the clause that locks a row read in a transaction, so two servers sharing PostgreSQL
// cannot both change the same thing at once (SQLite's single writer already guarantees it).
func (t *Tx) ForUpdate() string {
	if t.d.Postgres {
		return " FOR UPDATE"
	}
	return ""
}

// pgProblem says in plain words why PostgreSQL could not be used.
func pgProblem(u *url.URL, err error) error {
	m := err.Error()
	db := strings.TrimPrefix(u.Path, "/")
	switch {
	case strings.Contains(m, "28P01"), strings.Contains(m, "password authentication failed"):
		return fmt.Errorf("PostgreSQL at %s refused the user name or password", u.Host)
	case strings.Contains(m, "3D000"):
		return fmt.Errorf("PostgreSQL at %s has no database %q: create it first (CREATE DATABASE %s)", u.Host, db, db)
	case strings.Contains(m, "42501"):
		return fmt.Errorf("the user may not use database %q on %s: make the user its owner", db, u.Host)
	case strings.Contains(m, "connection refused"):
		return fmt.Errorf("nothing answers at %s: check the address and port, and that PostgreSQL accepts connections from this server", u.Host)
	case strings.Contains(m, "no such host"):
		return fmt.Errorf("cannot find %s: check the server name", u.Hostname())
	case strings.Contains(m, "timeout"), strings.Contains(m, "deadline"):
		return fmt.Errorf("PostgreSQL at %s did not answer in time: a firewall may be in the way", u.Host)
	}
	return fmt.Errorf("cannot use PostgreSQL at %s: %w", u.Host, err)
}
