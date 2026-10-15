package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// A map file of raster tiles is served by tile address, with MBTiles' bottom-up rows turned to the
// web's top-down ones; a tile not in the file is empty, and vector files are refused.
func TestMapTiles(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, format string) string {
		f := filepath.Join(dir, name)
		db, err := sql.Open("sqlite", "file:"+f)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, q := range []string{`CREATE TABLE metadata (name text, value text)`, `CREATE TABLE tiles (zoom_level integer, tile_column integer, tile_row integer, tile_data blob)`} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		db.Exec(`INSERT INTO metadata VALUES ('format', ?)`, format)
		db.Exec(`INSERT INTO tiles VALUES (2, 1, 3, ?)`, []byte("\x89PNGtile-z2-x1-y0")) // row 3 of 4 from the bottom: y 0 from the top
		return f
	}
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	setMap := func(file string) {
		_, cfg := c.do("GET", "/api/settings", nil)
		cfg["system"].(map[string]any)["map"] = map[string]any{"file": file, "url": "", "attribution": "Example tiles"}
		if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
			t.Fatalf("save %d %v", code, out)
		}
	}
	setMap(mk("area.mbtiles", "png"))
	_, info := c.do("GET", "/api/map", nil)
	if info["tiles"] != "./api/map/tiles/{z}/{x}/{y}" || info["maxZoom"] != float64(2) || info["attribution"] != "Example tiles" {
		t.Fatalf("map %v", info)
	}
	if code, body := rawGet(t, c, ts.URL+"/api/map/tiles/2/1/0"); code != 200 || body != "\x89PNGtile-z2-x1-y0" {
		t.Fatalf("tile %d %q", code, body)
	}
	if code, _ := rawGet(t, c, ts.URL+"/api/map/tiles/2/1/3"); code != 204 {
		t.Fatalf("a missing tile answered %d", code)
	}
	if code, _ := rawGet(t, c, ts.URL+"/api/map/tiles/2/9/0"); code != http.StatusBadRequest {
		t.Fatalf("an impossible tile answered %d", code)
	}
	setMap(mk("vector.mbtiles", "pbf"))
	_, info = c.do("GET", "/api/map", nil)
	if p, _ := info["problem"].(string); p == "" {
		t.Fatalf("a vector file was taken: %v", info)
	}
	if code, _ := rawGet(t, newClient(t, ts.URL), ts.URL+"/api/map/tiles/2/1/0"); code != 401 {
		t.Fatalf("tiles served without signing in: %d", code)
	}
}

// With a tile server, BitVoice fetches each tile for the browser (only images, kept a while), so the
// browser's security policy stays this server only.
func TestMapTilesFromServer(t *testing.T) {
	hits := 0
	tilesrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/3/2/1.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG\r\n\x1a\nfrom-server"))
		case "/3/2/2.png":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>not a tile</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tilesrv.Close()
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	_, cfg := c.do("GET", "/api/settings", nil)
	cfg["system"].(map[string]any)["map"] = map[string]any{"file": "", "url": tilesrv.URL + "/{z}/{x}/{y}.png", "attribution": ""}
	if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	_, info := c.do("GET", "/api/map", nil)
	if info["tiles"] != "./api/map/tiles/{z}/{x}/{y}" {
		t.Fatalf("map %v", info)
	}
	for range 2 {
		if code, body := rawGet(t, c, ts.URL+"/api/map/tiles/3/2/1"); code != 200 || body != "\x89PNG\r\n\x1a\nfrom-server" {
			t.Fatalf("tile %d %q", code, body)
		}
	}
	if hits != 1 {
		t.Fatalf("the tile server was asked %d times for one tile", hits)
	}
	if code, _ := rawGet(t, c, ts.URL+"/api/map/tiles/3/2/2"); code != 502 {
		t.Fatalf("a page that is not an image answered %d", code)
	}
	if code, _ := rawGet(t, c, ts.URL+"/api/map/tiles/3/2/3"); code != 204 {
		t.Fatalf("a missing tile answered %d", code)
	}
}
