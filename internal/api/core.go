package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/lost"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// NG911 Core Services: the ECRF's service boundaries (DATA/core/boundaries.json) and the LoST
// endpoint that answers from them.

type boundaryStore struct {
	mu   sync.RWMutex
	list []lost.Boundary
	read bool
}

func (s *Server) boundaryFile() string { return filepath.Join(s.DataDir, "core", "boundaries.json") }

func (s *Server) boundaries() []lost.Boundary {
	b := &s.core
	b.mu.RLock()
	if b.read {
		defer b.mu.RUnlock()
		return b.list
	}
	b.mu.RUnlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.read {
		if data, err := os.ReadFile(s.boundaryFile()); err == nil {
			_ = json.Unmarshal(data, &b.list)
		}
		b.read = true
	}
	return b.list
}

func (s *Server) saveBoundaries(list []lost.Boundary) error {
	if err := os.MkdirAll(filepath.Dir(s.boundaryFile()), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(list)
	if err := os.WriteFile(s.boundaryFile()+".tmp", data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(s.boundaryFile()+".tmp", s.boundaryFile()); err != nil {
		return err
	}
	s.core.mu.Lock()
	s.core.list, s.core.read = list, true
	s.core.mu.Unlock()
	return nil
}

func (s *Server) lostServer(cfg config.Config) *lost.Server {
	return &lost.Server{Source: firstNonEmpty(cfg.System.Core.Source, elementID(cfg)), Boundaries: s.boundaries, Addresses: s.addresses,
		ForestGuide: cfg.System.Core.ForestGuide}
}

// allowed says whether a client address is in the ECRF's allow list.
func allowed(list []string, remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, a := range list {
		if p, err := netip.ParsePrefix(a); err == nil && p.Contains(ip) {
			return true
		}
		if x, err := netip.ParseAddr(a); err == nil && x == ip {
			return true
		}
	}
	return false
}

// boundaryInfo is a boundary as the screen lists it (without its corners).
type boundaryInfo struct {
	lost.Boundary
	Corners int `json:"corners"`
}

func (s *Server) coreRoutes() {
	// The LoST endpoint itself: no sign-in (other NG911 elements call it), only the allowed addresses.
	s.mux.HandleFunc("POST /lost", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		if !cfg.System.Core.ECRF {
			http.NotFound(w, r)
			return
		}
		if !allowed(cfg.System.Core.AllowFrom, r.RemoteAddr) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/lost+xml")
		w.Write(s.lostServer(cfg).Respond(http.MaxBytesReader(w, r.Body, 1<<20)))
	})
	s.handle("GET /api/core/boundaries", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []boundaryInfo{}
		for _, b := range s.boundaries() {
			n := 0
			for _, ring := range b.Polygons {
				n += len(ring)
			}
			b.Polygons = nil
			out = append(out, boundaryInfo{b, n})
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	// Upload a GeoJSON file of boundaries for one service; replace=true drops that service's old ones.
	s.handle("POST /api/core/boundaries", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
		if err != nil {
			writeError(w, 413, "the file is larger than 64 MB")
			return
		}
		service := r.URL.Query().Get("service")
		if service == "" {
			service = "urn:service:sos"
		}
		got, err := lost.ParseGeoJSON(data, service, r.URL.Query().Get("uri"))
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		for _, b := range got {
			if b.URI == "" {
				writeError(w, 422, fmt.Sprintf("area %s (%s) says nowhere to send calls: give each a uri property, or a default", b.ID, b.Name))
				return
			}
		}
		var keep []lost.Boundary
		for _, b := range s.boundaries() {
			if r.URL.Query().Get("replace") == "true" && b.Service == service {
				continue
			}
			keep = append(keep, b)
		}
		seen := map[string]bool{}
		for _, b := range keep {
			seen[b.ID] = true
		}
		for i := range got {
			for seen[got[i].ID] {
				got[i].ID += "-2"
			}
			seen[got[i].ID] = true
		}
		if err := s.saveBoundaries(append(keep, got...)); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.boundaries", fmt.Sprintf("%d areas for %s", len(got), service))
		writeJSON(w, 200, map[string]any{"added": len(got)})
	})
	s.handle("PUT /api/core/boundaries/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in lost.Boundary
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		list := append([]lost.Boundary(nil), s.boundaries()...)
		for i := range list {
			if list[i].ID == r.PathValue("id") {
				list[i].Name, list[i].URI, list[i].Number, list[i].Service = in.Name, in.URI, in.Number, in.Service
				list[i].Cities, list[i].Counties, list[i].States = in.Cities, in.Counties, in.States
				if err := s.saveBoundaries(list); err != nil {
					writeErr(w, err)
					return
				}
				s.audit(r, u.Username, "core.boundary", in.Name)
				writeJSON(w, 200, map[string]any{"ok": true})
				return
			}
		}
		writeError(w, 404, "no such area")
	})
	s.handle("DELETE /api/core/boundaries/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var keep []lost.Boundary
		for _, b := range s.boundaries() {
			if b.ID != r.PathValue("id") {
				keep = append(keep, b)
			}
		}
		if err := s.saveBoundaries(keep); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
	// Try a place: which area answers it, as LoST would.
	s.handle("GET /api/core/find", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		q := lost.Query{Service: firstNonEmpty(r.URL.Query().Get("service"), "urn:service:sos"),
			State: r.URL.Query().Get("state"), County: r.URL.Query().Get("county"), City: r.URL.Query().Get("city")}
		if la, err := strconv.ParseFloat(r.URL.Query().Get("lat"), 64); err == nil {
			if lo, err := strconv.ParseFloat(r.URL.Query().Get("lon"), 64); err == nil {
				q.Lat, q.Lon = &la, &lo
			}
		}
		b, code := s.lostServer(s.config()).Find(q)
		if b == nil {
			writeJSON(w, 200, map[string]any{"found": false, "why": code})
			return
		}
		c := *b
		c.Polygons = nil
		writeJSON(w, 200, map[string]any{"found": true, "area": c})
	})
}

// FindService is the ESRP's ECRF: this server's own boundaries, or an outside ECRF by LoST.
func (s *Server) FindService(service string, loc *i3.Location) (string, string) {
	cfg := s.config()
	q := lost.Query{Service: service}
	if loc.HasPoint() {
		q.Lat, q.Lon = loc.Lat, loc.Lon
	} else if loc.Civic != nil {
		q.State, q.County, q.City = loc.Civic.State, loc.Civic.County, loc.Civic.City
	}
	if u := cfg.System.Core.LoSTURL; u != "" {
		uri, err := lost.Ask(u, q)
		if err != nil {
			return "", err.Error()
		}
		return uri, ""
	}
	b, code := s.lostServer(cfg).Find(q)
	if b == nil {
		return "", code
	}
	return b.URI, ""
}
