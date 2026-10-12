package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/db"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) databaseRoutes() {
	s.handle("GET /api/database", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		d := s.Store.DB()
		out := map[string]any{"kind": "sqlite", "where": d.Where, "fixed": s.DatabaseFixed}
		if d.Postgres {
			out["kind"] = "postgres"
		} else if fi, err := os.Stat(d.Where); err == nil {
			size := fi.Size()
			if wal, err := os.Stat(d.Where + "-wal"); err == nil {
				size += wal.Size()
			}
			out["bytes"] = size
		}
		if c, err := d.Counts(r.Context()); err == nil {
			out["counts"] = c
		}
		writeJSON(w, 200, out)
	})
	// Try a PostgreSQL address: can BitVoice reach it, and is it empty (ready to move into)?
	s.handle("POST /api/database/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			URL string `json:"url"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		target, err := openPostgresTarget(in.URL)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		defer target.Close()
		empty, err := target.Empty(r.Context())
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"where": target.Where, "empty": empty})
	})
	// Move everything into another database and restart on it: a PostgreSQL address, or "" for the
	// data directory's own SQLite file.
	s.handle("POST /api/database/move", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			URL string `json:"url"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if s.DatabaseFixed {
			writeError(w, 409, "the database was chosen when BitVoice started (-database or BITVOICE_DATABASE): change it there")
			return
		}
		if s.Engine != nil && len(s.Engine.Calls()) > 0 {
			writeError(w, 409, "calls are in progress: move the data when no calls are going on")
			return
		}
		cur := s.Store.DB()
		in.URL = strings.TrimSpace(in.URL)
		var target *db.DB
		var err error
		if in.URL == "" {
			if !cur.Postgres {
				writeError(w, 409, "the data is already in this server's own file")
				return
			}
			path := store.SQLitePath(s.DataDir)
			if _, err := os.Stat(path); err == nil {
				// An older file is kept aside, never overwritten.
				_ = os.Rename(path, path+"."+time.Now().Format("20060102-150405"))
				_ = os.Remove(path + "-wal")
				_ = os.Remove(path + "-shm")
			}
			target, err = db.Open(path)
		} else {
			target, err = openPostgresTarget(in.URL)
		}
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		defer target.Close()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		s.audit(r, u.Username, "database.move", "to "+target.Where)
		if err := cur.CopyTo(ctx, target, nil); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := store.SetDatabaseSetting(s.DataDir, in.URL); err != nil {
			writeErr(w, err)
			return
		}
		counts, _ := target.Counts(ctx)
		writeJSON(w, 200, map[string]any{"where": target.Where, "counts": counts, "restarting": s.Restart != nil})
		if s.Restart != nil {
			go func() {
				if s.Bus != nil {
					s.Bus.Publish("system.restarting", map[string]string{"action": "database"})
				}
				time.Sleep(500 * time.Millisecond) // let the answer reach the browser
				s.Restart()
			}()
		}
	})
}

func openPostgresTarget(raw string) (*db.DB, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return nil, errors.New("enter a PostgreSQL address such as postgres://bitvoice:password@db.example.com:5432/bitvoice")
	}
	return db.Open(raw)
}
