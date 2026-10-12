package db

import (
	"context"
	"encoding/json"
	"github.com/biodream-llc/bitvoice/internal/dbtest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Each test runs on SQLite, and on PostgreSQL too when BITVOICE_TEST_POSTGRES names one
// (postgres://user:pass@127.0.0.1:55432/bitvoice); the schema is dropped first.
func each(t *testing.T, f func(t *testing.T, d *DB)) {
	t.Run("sqlite", func(t *testing.T) {
		d, err := Open(filepath.Join(t.TempDir(), "bitvoice.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		f(t, d)
	})
	pg := os.Getenv("BITVOICE_TEST_POSTGRES")
	if pg == "" {
		return
	}
	t.Run("postgres", func(t *testing.T) {
		dbtest.Reset(t, pg)
		d, err := Open(pg)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		f(t, d)
	})
}

func TestDocs(t *testing.T) {
	each(t, func(t *testing.T, d *DB) {
		ctx := context.Background()
		if _, ok, err := d.GetDoc(ctx, "users.json"); ok || err != nil {
			t.Fatal("found a document that was never written", err)
		}
		if err := d.PutDoc(ctx, "users.json", []byte(`["a"]`)); err != nil {
			t.Fatal(err)
		}
		d.PutDoc(ctx, "users.json", []byte(`["a","b"]`))
		d.PutDoc(ctx, "voicemail_100.json", []byte(`{}`))
		d.PutDoc(ctx, "voicemailx.json", []byte(`{}`))
		if b, ok, _ := d.GetDoc(ctx, "users.json"); !ok || string(b) != `["a","b"]` {
			t.Fatalf("got %s", b)
		}
		if n, _ := d.DocNames(ctx, "voicemail_"); len(n) != 1 || n[0] != "voicemail_100.json" {
			t.Fatalf("names %v", n) // _ is not a wildcard
		}
		// Forty changes at once, none lost.
		var wg sync.WaitGroup
		for range 40 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := d.MutateDoc(ctx, "count", func(old []byte) ([]byte, error) {
					var n int
					json.Unmarshal(old, &n)
					return json.Marshal(n + 1)
				})
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if b, _, _ := d.GetDoc(ctx, "count"); string(b) != "40" {
			t.Fatalf("count %s", b)
		}
	})
}

func TestPlaceholders(t *testing.T) {
	d := &DB{Postgres: true}
	if got := d.Q(`SELECT a FROM t WHERE b = ? AND c = '?' AND d = ?`); got != `SELECT a FROM t WHERE b = $1 AND c = '?' AND d = $2` {
		t.Fatal(got)
	}
	if got := (&DB{}).Q("x = ?"); got != "x = ?" {
		t.Fatal(got)
	}
}

func TestReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bitvoice.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.PutDoc(context.Background(), "x", []byte("1"))
	d.Close()
	d, err = Open(path) // migrations do not run twice
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if b, _, _ := d.GetDoc(context.Background(), "x"); string(b) != "1" {
		t.Fatal("lost on reopen")
	}
}

// Everything copies from SQLite into PostgreSQL (or another SQLite), and only into an empty database.
func TestCopy(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.PutDoc(ctx, "users.json", []byte(`[1]`))
	src.Exec(ctx, `INSERT INTO cdrs (id, start_ms, direction, num_from, num_to, caller_id, recording, body) VALUES ('c1', 5, 'inbound', '1', '2', '', '', '{}')`)
	src.Exec(ctx, `INSERT INTO revisions (rev, at_ms, actor, summary, body) VALUES (1, 5, 'system', 'New', '{}')`)
	each(t, func(t *testing.T, dst *DB) {
		if err := src.CopyTo(ctx, dst, nil); err != nil {
			t.Fatal(err)
		}
		c, _ := dst.Counts(ctx)
		if c["docs"] != 1 || c["cdrs"] != 1 || c["revisions"] != 1 {
			t.Fatalf("counts %v", c)
		}
		if err := src.CopyTo(ctx, dst, nil); err == nil {
			t.Fatal("copied into a database that holds data")
		}
	})
}

func TestPostgresProblems(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_POSTGRES")
	if base == "" {
		t.Skip("set BITVOICE_TEST_POSTGRES")
	}
	u, _ := url.Parse(base)
	bad := *u
	bad.User = url.UserPassword(u.User.Username(), "not-the-password")
	missing := *u
	missing.Path = "/no_such_db"
	for spec, want := range map[string]string{bad.String(): "refused the user name or password", missing.String(): `no database "no_such_db"`,
		"postgres://x:y@127.0.0.1:1/z": "nothing answers"} {
		_, err := Open(spec)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "not-the-password") {
			t.Errorf("%s: %v", want, err)
		}
	}
}
