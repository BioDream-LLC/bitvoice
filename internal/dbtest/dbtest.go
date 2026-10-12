// Package dbtest gives tests a database: SQLite, or a fresh PostgreSQL database when
// BITVOICE_TEST_POSTGRES names a server.
package dbtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	_ "github.com/jackc/pgx/v5/stdlib"
	"net/url"
	"os"
	"testing"
)

// Spec is the database a test should use: "" (SQLite in the test's directory) unless
// BITVOICE_TEST_POSTGRES names a PostgreSQL server, in which case a new, empty database is made on it
// for this test and dropped when it ends.
func Spec(t testing.TB) string {
	t.Helper()
	base := os.Getenv("BITVOICE_TEST_POSTGRES")
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	name := "bvtest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		admin.Close()
	})
	u.Path = "/" + name
	return u.String()
}

// Reset empties a PostgreSQL test database (its public schema), for tests only.
func Reset(t testing.TB, spec string) {
	t.Helper()
	p, err := sql.Open("pgx", spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
}
