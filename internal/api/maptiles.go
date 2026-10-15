package api

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// Map tiles: an .mbtiles file (SQLite, the MBTiles 1.3 layout) of raster tiles is served from this
// server, so the call-takers' map works with no internet; or BitVoice fetches them from a tile server
// for the browser, so browsers only ever talk to BitVoice (its security policy stays 'self').

type tileFile struct {
	mu     sync.Mutex
	path   string
	db     *sql.DB
	format string // png, jpg, webp
	max    int
	err    error
	cache  map[string][]byte // tiles fetched from the tile server lately
	order  []string
}

func (f *tileFile) open(path string) (*sql.DB, string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path == path && (f.db != nil || f.err != nil) {
		return f.db, f.format, f.max, f.err
	}
	if f.db != nil {
		f.db.Close()
	}
	f.path, f.db, f.err, f.format, f.max = path, nil, nil, "", 0
	if _, err := os.Stat(path); err != nil {
		f.err = err
		return nil, "", 0, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		f.err = err
		return nil, "", 0, err
	}
	format := "png"
	_ = db.QueryRow(`SELECT value FROM metadata WHERE name='format'`).Scan(&format)
	switch format {
	case "png", "jpg", "jpeg", "webp":
	default:
		db.Close()
		f.err = errTiles("the map file holds " + format + " tiles; BitVoice serves raster tiles (png, jpg or webp)")
		return nil, "", 0, f.err
	}
	maxZ := 0
	_ = db.QueryRow(`SELECT MAX(zoom_level) FROM tiles`).Scan(&maxZ)
	f.db, f.format, f.max = db, format, maxZ
	return db, format, maxZ, nil
}

type errTiles string

func (e errTiles) Error() string { return string(e) }

func (s *Server) mapRoutes() {
	s.handle("GET /api/map", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		m := s.config().System.Map
		out := map[string]any{"attribution": m.Attribution}
		switch {
		case m.File != "":
			_, _, maxZ, err := s.tiles.open(m.File)
			if err != nil {
				out["problem"] = "the map file cannot be read: " + err.Error()
				break
			}
			out["tiles"], out["maxZoom"] = "./api/map/tiles/{z}/{x}/{y}", maxZ
		case m.URL != "":
			out["tiles"], out["maxZoom"] = "./api/map/tiles/{z}/{x}/{y}", 19
		}
		writeJSON(w, 200, out)
	})
	s.handle("GET /api/map/tiles/{z}/{x}/{y}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		m := s.config().System.Map
		if m.File == "" && m.URL == "" {
			writeError(w, 404, "no map is set")
			return
		}
		z, ez := strconv.Atoi(r.PathValue("z"))
		x, ex := strconv.Atoi(r.PathValue("x"))
		y, ey := strconv.Atoi(r.PathValue("y"))
		if ez != nil || ex != nil || ey != nil || z < 0 || z > 24 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
			writeError(w, 400, "no such tile")
			return
		}
		if m.File == "" {
			s.proxyTile(w, r, m.URL, z, x, y)
			return
		}
		db, format, _, err := s.tiles.open(m.File)
		if err != nil {
			writeError(w, 503, err.Error())
			return
		}
		var b []byte
		// MBTiles rows count from the bottom (TMS); the web counts from the top.
		err = db.QueryRowContext(r.Context(), `SELECT tile_data FROM tiles WHERE zoom_level=? AND tile_column=? AND tile_row=?`, z, x, (1<<z)-1-y).Scan(&b)
		if err == sql.ErrNoRows {
			w.WriteHeader(204) // the area is not in the file: the map shows nothing there
			return
		}
		if err != nil {
			writeError(w, 500, "the map file cannot be read")
			return
		}
		if format == "jpg" {
			format = "jpeg"
		}
		w.Header().Set("Content-Type", "image/"+format)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(b)
	})
}

// maxTile bounds one tile from a tile server; tileCache the tiles kept in memory.
const (
	maxTile   = 1 << 20
	tileCache = 512
)

var tileClient = &http.Client{Timeout: 10 * time.Second}

// proxyTile fetches one tile from the tile server (only images, at most maxTile), keeping the last
// tileCache in memory.
func (s *Server) proxyTile(w http.ResponseWriter, r *http.Request, tmpl string, z, x, y int) {
	u := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(tmpl)
	f := &s.tiles
	f.mu.Lock()
	b, ok := f.cache[u]
	f.mu.Unlock()
	if !ok {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
		if err != nil {
			writeError(w, 502, "the tile address is not valid")
			return
		}
		req.Header.Set("User-Agent", "BitVoice map (911 call-taker position)")
		resp, err := tileClient.Do(req)
		if err != nil {
			writeError(w, 502, "the tile server did not answer")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 204 {
			w.WriteHeader(204)
			return
		}
		ct := resp.Header.Get("Content-Type")
		if resp.StatusCode != 200 || !strings.HasPrefix(ct, "image/") {
			writeError(w, 502, "the tile server sent no image")
			return
		}
		b, err = io.ReadAll(io.LimitReader(resp.Body, maxTile+1))
		if err != nil || len(b) > maxTile {
			writeError(w, 502, "the tile is too large")
			return
		}
		f.mu.Lock()
		if f.cache == nil {
			f.cache = map[string][]byte{}
		}
		if _, had := f.cache[u]; !had {
			f.order = append(f.order, u)
			if len(f.order) > tileCache {
				delete(f.cache, f.order[0])
				f.order = f.order[1:]
			}
		}
		f.cache[u] = b
		f.mu.Unlock()
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(b)
}
