package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/lost"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The LVF's address data (DATA/core/addresses.json): NENA GIS road centerlines and address points.

type addressStore struct {
	mu      sync.RWMutex
	data    *lost.Addresses
	read    bool
	updated time.Time
}

func (s *Server) addressFile() string { return filepath.Join(s.DataDir, "core", "addresses.json") }

func (s *Server) addresses() *lost.Addresses {
	a := &s.lvf
	a.mu.RLock()
	if a.read {
		defer a.mu.RUnlock()
		return a.data
	}
	a.mu.RUnlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.read {
		if raw, err := os.ReadFile(s.addressFile()); err == nil {
			var d lost.Addresses
			if json.Unmarshal(raw, &d) == nil {
				a.data = &d
			}
			if st, err := os.Stat(s.addressFile()); err == nil {
				a.updated = st.ModTime()
			}
		}
		a.read = true
	}
	return a.data
}

func (s *Server) saveAddresses(d *lost.Addresses) error {
	if d == nil {
		if err := os.Remove(s.addressFile()); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(s.addressFile()), 0o700); err != nil {
			return err
		}
		raw, _ := json.Marshal(d)
		if err := os.WriteFile(s.addressFile()+".tmp", raw, 0o600); err != nil {
			return err
		}
		if err := os.Rename(s.addressFile()+".tmp", s.addressFile()); err != nil {
			return err
		}
	}
	s.lvf.mu.Lock()
	s.lvf.data, s.lvf.read, s.lvf.updated = d, true, time.Now()
	s.lvf.mu.Unlock()
	return nil
}

func (s *Server) lvfRoutes() {
	s.handle("GET /api/core/addresses", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		d := s.addresses()
		out := map[string]any{"streets": 0, "points": 0}
		if d != nil {
			out["streets"], out["points"] = len(d.Streets), len(d.Points)
			s.lvf.mu.RLock()
			out["updated"] = s.lvf.updated
			s.lvf.mu.RUnlock()
		}
		writeJSON(w, 200, out)
	})
	// Upload NENA GIS address data; replace=true drops what was there, otherwise it is added to.
	s.handle("POST /api/core/addresses", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<20))
		if err != nil {
			writeError(w, 413, "the file is larger than 256 MB")
			return
		}
		got, err := lost.ParseAddressGeoJSON(raw)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		next := got
		if old := s.addresses(); old != nil && r.URL.Query().Get("replace") != "true" {
			next = lost.Addresses{Streets: append(append([]lost.Street(nil), old.Streets...), got.Streets...),
				Points: append(append([]lost.Point(nil), old.Points...), got.Points...)}
		}
		if err := s.saveAddresses(&next); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.addresses", fmt.Sprintf("%d road segments, %d address points", len(got.Streets), len(got.Points)))
		writeJSON(w, 200, map[string]any{"streets": len(got.Streets), "points": len(got.Points)})
	})
	s.handle("DELETE /api/core/addresses", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.saveAddresses(nil); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.addresses", "removed")
		w.WriteHeader(204)
	})
	s.handle("GET /api/core/bcf", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.BCFLog()})
	})
	// Check one civic address (PIDF-LO tokens: A1, A3, RD, HNO ...).
	s.handle("POST /api/core/validate", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in lost.Civic
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		d := s.addresses()
		if d == nil {
			writeError(w, 409, "no address data is loaded")
			return
		}
		writeJSON(w, 200, d.Validate(in))
	})
}
