package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The database itself cannot say where the database is, so that one setting lives in the data
// directory as database.json (readable by BitVoice alone: it holds the PostgreSQL password).

const databaseFile = "database.json"

type databaseSetting struct {
	Postgres string `json:"postgres"`
}

// DatabaseSetting reads which database the data directory uses: a PostgreSQL address, or "" for its
// own SQLite file.
func DatabaseSetting(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, databaseFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var s databaseSetting
	if err := json.Unmarshal(b, &s); err != nil {
		return "", errors.New("store: " + databaseFile + " is damaged: " + err.Error())
	}
	return strings.TrimSpace(s.Postgres), nil
}

// SetDatabaseSetting records which database to use from the next start; "" means the SQLite file.
func SetDatabaseSetting(dir, postgres string) error {
	p := filepath.Join(dir, databaseFile)
	if postgres == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(databaseSetting{Postgres: postgres}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(p, append(b, '\n')) // written 0600, atomically
}

// SQLitePath is where the data directory's own database file is.
func SQLitePath(dir string) string { return filepath.Join(dir, "bitvoice.db") }
