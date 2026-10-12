// Package storetest opens stores for tests.
package storetest

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/dbtest"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Open opens a store for a test: a temporary data directory, with its data in SQLite, or in a fresh
// PostgreSQL database when BITVOICE_TEST_POSTGRES is set. It is closed when the test ends.
func Open(t testing.TB) (*store.Store, error) {
	t.Helper()
	s, err := store.OpenWith(t.TempDir(), dbtest.Spec(t))
	if err == nil {
		t.Cleanup(func() { s.Close() })
	}
	return s, err
}
