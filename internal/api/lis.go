package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The LIS (NG911 Core Services): location records by telephone number (DATA/core/lis.json), served
// to HELD requests at /held from the allowed addresses.

type lisStore struct {
	mu    sync.RWMutex
	read  bool
	by    map[string]i3.LISRecord
	saved time.Time
}

func (s *Server) lisFile() string { return filepath.Join(s.DataDir, "core", "lis.json") }

func (s *Server) lisRecords() (map[string]i3.LISRecord, time.Time) {
	l := &s.lis
	l.mu.RLock()
	if l.read {
		defer l.mu.RUnlock()
		return l.by, l.saved
	}
	l.mu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.read {
		l.by = map[string]i3.LISRecord{}
		if data, err := os.ReadFile(s.lisFile()); err == nil {
			var list []i3.LISRecord
			if json.Unmarshal(data, &list) == nil {
				for _, r := range list {
					l.by[r.Number] = r
				}
			}
			if st, err := os.Stat(s.lisFile()); err == nil {
				l.saved = st.ModTime()
			}
		}
		l.read = true
	}
	return l.by, l.saved
}

func (s *Server) saveLIS(by map[string]i3.LISRecord) error {
	list := make([]i3.LISRecord, 0, len(by))
	for _, r := range by {
		list = append(list, r)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.lisFile()), 0o700); err != nil {
		return err
	}
	tmp := s.lisFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.lisFile()); err != nil {
		return err
	}
	s.lis.mu.Lock()
	s.lis.by, s.lis.read, s.lis.saved = by, true, time.Now()
	s.lis.mu.Unlock()
	return nil
}

func (s *Server) lisRoutes() {
	s.mux.HandleFunc("POST /held", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		if !cfg.System.Core.LIS {
			http.NotFound(w, r)
			return
		}
		if !allowed(cfg.System.Core.AllowFrom, r.RemoteAddr) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/held+xml;charset=utf-8")
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			w.Write(i3.HELDErrorResponse("badRequest", "the request is too large"))
			return
		}
		n, err := i3.HELDDevice(body)
		var he i3.HELDError
		if errors.As(err, &he) {
			msg := "this LIS locates devices by telephone number only"
			if he == "badRequest" {
				msg = "not a HELD location request"
			}
			w.Write(i3.HELDErrorResponse(he, msg))
			return
		}
		by, _ := s.lisRecords()
		rec, ok := by[i3.NumberKey(n)]
		if !ok {
			w.Write(i3.HELDErrorResponse("locationUnknown", "no location is kept for this number"))
			return
		}
		host := firstNonEmpty(cfg.System.Core.Source, cfg.System.Name, "lis")
		w.Write(i3.HELDResponse(i3.BuildPIDF("pres:"+rec.Number+"@"+host, rec, time.Now())))
	})
	s.handle("GET /api/core/lis", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		by, saved := s.lisRecords()
		out := map[string]any{"records": len(by)}
		if !saved.IsZero() {
			out["updated"] = saved
		}
		if q := i3.NumberKey(r.URL.Query().Get("number")); q != "" {
			if rec, ok := by[q]; ok {
				out["found"] = rec
			}
		}
		writeJSON(w, 200, out)
	})
	// Upload a spreadsheet of location records; replace=true drops the old ones first.
	s.handle("POST /api/core/lis", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		recs, err := i3.ParseLISCSV(http.MaxBytesReader(w, r.Body, 256<<20))
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		old, _ := s.lisRecords()
		by := map[string]i3.LISRecord{}
		if r.URL.Query().Get("replace") != "true" {
			for k, v := range old {
				by[k] = v
			}
		}
		for _, rec := range recs {
			by[rec.Number] = rec
		}
		if err := s.saveLIS(by); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.lis", fmt.Sprintf("%d location records (%d in all)", len(recs), len(by)))
		writeJSON(w, 200, map[string]any{"added": len(recs), "records": len(by)})
	})
	s.handle("DELETE /api/core/lis", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.saveLIS(map[string]i3.LISRecord{}); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.lis", "every location record removed")
		w.WriteHeader(204)
	})
}
