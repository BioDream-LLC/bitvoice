package store

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/dbtest"
)

// OpenTest opens a store for a test: a temporary data directory, with its data in SQLite, or in a
// fresh PostgreSQL database when BITVOICE_TEST_POSTGRES is set. It is closed when the test ends.
func OpenTest(t testing.TB) (*Store, error) {
	t.Helper()
	s, err := OpenWith(t.TempDir(), dbtest.Spec(t))
	if err == nil {
		t.Cleanup(func() { s.Close() })
	}
	return s, err
}
