package api

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/agi"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// agiRoutes serve what AGI scripts use: the sound library (uploads and the scripts' recordings), the
// programs folder (listed only: programs are put there on the server itself, never uploaded), and
// Asterisk's database.
func (s *Server) agiRoutes() {
	ready := func(w http.ResponseWriter) bool {
		if s.AGI == nil || s.AGI.Library == nil {
			writeError(w, http.StatusServiceUnavailable, "AGI is not available on this server")
			return false
		}
		return true
	}
	s.handle("GET /api/agi/sounds", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		items := s.AGI.Library.List()
		if items == nil {
			items = []agi.Sound{}
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	s.handle("GET /api/agi/sounds/{name...}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		b, err := s.AGI.Library.Open(r.PathValue("name"), r.URL.Query().Get("recorded") == "1")
		if err != nil {
			writeError(w, http.StatusNotFound, "no such sound")
			return
		}
		serveWAV(w, r, filepath.Base(r.PathValue("name"))+".wav", b)
	})
	// PUT a file's bytes; ?filename= says its format when that is one of Asterisk's raw ones (.gsm ...).
	s.handle("PUT /api/agi/sounds/{name...}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 40<<20))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "the file is larger than 40 MB")
			return
		}
		name := r.PathValue("name")
		if err := s.AGI.Library.Upload(name, r.URL.Query().Get("filename"), raw); err != nil {
			code := http.StatusUnprocessableEntity
			if errors.Is(err, agi.ErrFull) {
				code = http.StatusInsufficientStorage
			}
			writeError(w, code, err.Error())
			return
		}
		s.audit(r, u.Username, "agi.sound.upload", name)
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("DELETE /api/agi/sounds/{name...}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		name := r.PathValue("name")
		if err := s.AGI.Library.Remove(name, r.URL.Query().Get("recorded") == "1"); err != nil {
			writeError(w, http.StatusNotFound, "no such sound")
			return
		}
		s.audit(r, u.Username, "agi.sound.delete", name)
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("GET /api/agi/programs", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		type program struct {
			Name       string    `json:"name"`
			Bytes      int64     `json:"bytes"`
			Executable bool      `json:"executable"`
			Modified   time.Time `json:"modified"`
		}
		items := []program{}
		entries, _ := os.ReadDir(s.AGI.Programs)
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			items = append(items, program{Name: e.Name(), Bytes: fi.Size(), Modified: fi.ModTime().UTC(),
				Executable: runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		dir, _ := filepath.Abs(s.AGI.Programs)
		writeJSON(w, 200, map[string]any{"folder": dir, "items": items, "eagi": runtime.GOOS != "windows"})
	})
	s.handle("GET /api/agi/db", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		type entry struct {
			Family string `json:"family"`
			Key    string `json:"key"`
			Value  string `json:"value"`
		}
		items := []entry{}
		for f, kv := range s.AGI.DB.All() {
			for k, v := range kv {
				items = append(items, entry{f, k, v})
			}
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Family != items[j].Family {
				return items[i].Family < items[j].Family
			}
			return items[i].Key < items[j].Key
		})
		writeJSON(w, 200, map[string]any{"items": items})
	})
	s.handle("PUT /api/agi/db/{family}/{key...}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		var in struct {
			Value string `json:"value"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		f, k := strings.TrimSpace(r.PathValue("family")), strings.TrimSpace(r.PathValue("key"))
		if f == "" || k == "" || len(f)+len(k)+len(in.Value) > 4096 || strings.ContainsAny(f+k+in.Value, "\r\n") {
			writeError(w, http.StatusUnprocessableEntity, "a family, a key and a value of one line each")
			return
		}
		if err := s.AGI.DB.Put(f, k, in.Value); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "agi.db.put", f+"/"+k)
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("DELETE /api/agi/db/{family}/{key...}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		f, k := r.PathValue("family"), r.PathValue("key")
		if !s.AGI.DB.Del(f, k) {
			writeError(w, http.StatusNotFound, "no such value")
			return
		}
		s.audit(r, u.Username, "agi.db.delete", f+"/"+k)
		w.WriteHeader(http.StatusNoContent)
	})
	// test tries an address as a call would: it connects to a FastAGI server (and closes at once,
	// without sending a call), or checks that a program is in the folder and may be run.
	s.handle("POST /api/agi/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !ready(w) {
			return
		}
		var in struct {
			Address string `json:"address"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		tg, err := agi.ParseTarget(in.Address)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		if tg.Program != "" {
			fi, err := os.Stat(filepath.Join(s.AGI.Programs, tg.Program))
			switch {
			case errors.Is(err, fs.ErrNotExist):
				writeJSON(w, 200, map[string]any{"ok": false, "message": tg.Program + " is not in the AGI programs folder yet"})
			case err != nil || !fi.Mode().IsRegular():
				writeJSON(w, 200, map[string]any{"ok": false, "message": tg.Program + " is not a file"})
			case runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0:
				writeJSON(w, 200, map[string]any{"ok": false, "message": tg.Program + " is not executable: chmod +x it"})
			default:
				writeJSON(w, 200, map[string]any{"ok": true, "message": tg.Program + " is there and may be run"})
			}
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		started := time.Now()
		c, err := agi.Dial(ctx, tg, 5*time.Second)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "message": "could not connect: " + err.Error()})
			return
		}
		addr := c.RemoteAddr().String()
		c.Close()
		writeJSON(w, 200, map[string]any{"ok": true, "message": "connected to " + addr + " in " + time.Since(started).Round(time.Millisecond).String()})
	})
}
