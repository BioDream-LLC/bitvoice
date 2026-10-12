package api

import (
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/dbtest"
	"os"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestDatabaseMove(t *testing.T) {
	s, ts := newServer(t)
	s.DataDir = s.Store.Dir()
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	_, info := admin.do("GET", "/api/database", nil)
	if os.Getenv("BITVOICE_TEST_POSTGRES") != "" {
		return // this test server itself runs on PostgreSQL; the move from SQLite is tested below on SQLite runs
	}
	if info["kind"] != "sqlite" || info["counts"].(map[string]any)["revisions"].(float64) < 1 {
		t.Fatalf("info %v", info)
	}
	if code, out := admin.do("POST", "/api/database/move", map[string]any{"url": ""}); code != 409 {
		t.Fatalf("moved into the file it is in: %d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/database/test", map[string]any{"url": "mysql://x"}); code != 422 || !strings.Contains(fmt.Sprint(out), "PostgreSQL address") {
		t.Fatalf("bad address: %d %v", code, out)
	}
}

// TestDatabaseMoveToPostgres moves a SQLite installation into PostgreSQL and checks a store opened on
// the new database sees the same data.
func TestDatabaseMoveToPostgres(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_POSTGRES")
	if base == "" {
		t.Skip("set BITVOICE_TEST_POSTGRES")
	}
	os.Setenv("BITVOICE_TEST_POSTGRES", "")
	s, ts := newServer(t) // on SQLite
	os.Setenv("BITVOICE_TEST_POSTGRES", base)
	s.DataDir = s.Store.Dir()
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup/complete", setupBody())
	pg := dbtest.Spec(t)
	if code, out := admin.do("POST", "/api/database/test", map[string]any{"url": pg}); code != 200 || out["empty"] != true {
		t.Fatalf("test %d %v", code, out)
	}
	code, out := admin.do("POST", "/api/database/move", map[string]any{"url": pg})
	if code != 200 {
		t.Fatalf("move %d %v", code, out)
	}
	if got, _ := store.DatabaseSetting(s.DataDir); got != pg {
		t.Fatalf("setting %q", got)
	}
	_, rev := s.Store.Config()
	moved, err := store.OpenWith(t.TempDir(), pg)
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	if _, rev2 := moved.Config(); rev2 != rev || !moved.HasUsers() || !moved.State().SetupComplete {
		t.Fatalf("moved rev %d want %d", rev2, rev)
	}
	if code, _ := admin.do("POST", "/api/database/move", map[string]any{"url": pg}); code != 422 {
		t.Fatal("moved into a database that holds data")
	}
}
