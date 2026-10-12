// Package web serves the built front end from inside the binary.
//
// The assets are embedded rather than shipped alongside, so deployment stays one
// file: copy the binary, run it, and the interface is there.
//
// The built output is committed so that `go build` works on a checkout without
// Node installed. Rebuild it with `make web`.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// A pattern that matches nothing is a compile error, which is the behaviour worth having: if the front end
// has not been built, the build stops rather than producing a server that 404s its own interface.
//
//go:embed dist/index.html dist/favicon.svg dist/assets
var embedded embed.FS

// Assets returns the built front end as a filesystem.
func Assets() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}

// Available reports whether a real front end was built into this binary, as
// opposed to the placeholder that keeps the package compiling.
func Available() bool {
	assets, err := Assets()
	if err != nil {
		return false
	}
	entries, err := fs.ReadDir(assets, "assets")
	return err == nil && len(entries) > 0
}

// Handler serves the front end.
//
// Anything that is not a real file falls back to index.html, because the
// interface is a single page application: a browser reloaded on a deep link must
// get the app rather than a 404.
func Handler() (http.Handler, error) {
	assets, err := Assets()
	if err != nil {
		return nil, err
	}

	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			serveIndex(w, r, assets)
			return
		}

		f, err := assets.Open(clean)
		if err != nil {
			// A missing asset is a 404, not the app: serving index.html for a script makes the browser report a
			// confusing MIME type error instead of the real problem.
			if isAssetPath(clean) {
				http.NotFound(w, r)

				return
			}
			serveIndex(w, r, assets)

			return
		}
		defer f.Close()

		if info, err := f.Stat(); err == nil && info.IsDir() {
			serveIndex(w, r, assets)
			return
		}

		// Hashed asset filenames change whenever their contents change, so they
		// can be cached hard. index.html must not be, or a browser keeps loading
		// an old app against a new API.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "the web interface was not built into this binary", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// assetExtensions are the file types a browser fetches as a subresource rather than as a page.
//
// A request for one of these that does not exist is a broken build, and saying so plainly beats serving the application and letting
// the browser produce a MIME type error about it.
var assetExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".wasm": true, ".map": true,
	".json": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".ico": true, ".webp": true, ".woff": true, ".woff2": true,
	".ttf": true, ".otf": true, ".eot": true, ".webmanifest": true,
}

// isAssetPath reports whether a path names a subresource rather than an application route.
//
// Application routes are things like /extensions/new, which have no extension. A dotted segment in the middle of a route would
// confuse this, but the front end has no such routes and a test pins that.
func isAssetPath(clean string) bool {
	return assetExtensions[strings.ToLower(path.Ext(clean))]
}
