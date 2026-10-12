package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cloud"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// ArchiveStatus is what the archive screen shows.
type ArchiveStatus struct {
	Target   string    `json:"target"`
	LastRun  time.Time `json:"lastRun,omitzero"`
	LastOK   time.Time `json:"lastOk,omitzero"`
	Error    string    `json:"error,omitempty"`
	Uploaded int       `json:"uploaded"` // files copied, all time
	Pending  int       `json:"pending"`
	Running  bool      `json:"running"`
}

type archiver struct {
	mu     sync.Mutex
	status ArchiveStatus
	done   map[string]bool // uploaded files, kept in archive.json
	wake   chan struct{}
}

func targetFor(a config.Archive) cloud.Target {
	switch a.Target {
	case "s3":
		return &cloud.S3{Region: a.S3.Region, Bucket: a.S3.Bucket, Prefix: a.S3.Prefix, Endpoint: a.S3.Endpoint,
			Creds: cloud.Credentials{AccessKey: a.S3.AccessKey, SecretKey: a.S3.SecretKey}}
	case "azure":
		return &cloud.AzureBlob{ContainerSAS: a.Azure.ContainerSAS, Prefix: a.Azure.Prefix}
	}
	return nil
}

func (s *Server) archiveState() *archiver {
	s.arOnce.Do(func() {
		s.ar = &archiver{done: map[string]bool{}, wake: make(chan struct{}, 1)}
		if b, err := os.ReadFile(filepath.Join(s.Store.Dir(), "archive.json")); err == nil {
			_ = json.Unmarshal(b, &s.ar.done)
		}
		s.ar.status.Uploaded = len(s.ar.done)
	})
	return s.ar
}

// RunArchive copies new recordings and voicemail, and a nightly configuration backup, to the archive.
func (s *Server) RunArchive(ctx context.Context) {
	ar := s.archiveState()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		s.archiveOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ar.wake:
		}
	}
}

func (s *Server) archiveOnce(ctx context.Context) {
	ar := s.archiveState()
	cfg := s.config() // vault references resolved
	a := cfg.System.Archive
	tgt := targetFor(a)
	ar.mu.Lock()
	ar.status.Target = ""
	if tgt != nil {
		ar.status.Target = tgt.Describe()
	}
	if tgt == nil || ar.status.Running {
		ar.mu.Unlock()
		return
	}
	ar.status.Running, ar.status.LastRun = true, time.Now().UTC()
	ar.mu.Unlock()

	// What to send: today's configuration, and every recording and message not sent yet.
	type item struct{ name, path, ctype string }
	var todo []item
	today := "config/" + time.Now().UTC().Format("2006-01-02") + ".json"
	todo = append(todo, item{name: today, ctype: "application/json"})
	walk := func(dir string, want bool) {
		if !want {
			return
		}
		root := filepath.Join(s.Store.Dir(), dir)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, _ := filepath.Rel(s.Store.Dir(), p)
			rel = filepath.ToSlash(rel)
			ct := "application/json"
			if strings.HasSuffix(rel, ".wav") {
				ct = "audio/wav"
			}
			todo = append(todo, item{name: rel, path: p, ctype: ct})
			return nil
		})
	}
	walk("recordings", a.Recordings)
	walk("voicemail", a.Voicemail)

	var failure error
	sent, pending := 0, 0
	for _, it := range todo {
		ar.mu.Lock()
		done := ar.done[it.name]
		ar.mu.Unlock()
		if done || ctx.Err() != nil {
			continue
		}
		var body []byte
		var err error
		if it.path == "" {
			c, rev := s.Store.Config()
			body, err = json.MarshalIndent(map[string]any{"rev": rev, "config": c}, "", "  ")
		} else {
			body, err = os.ReadFile(it.path)
		}
		if err == nil {
			err = tgt.Put(ctx, it.name, body, it.ctype)
		}
		if err != nil {
			failure = err
			pending++
			if errors.Is(err, context.Canceled) {
				break
			}
			continue
		}
		ar.mu.Lock()
		// A voicemail's metadata changes (heard, transcript), so only audio and dated backups are final.
		if it.path == "" || strings.HasSuffix(it.name, ".wav") {
			ar.done[it.name] = true
		}
		ar.mu.Unlock()
		sent++
	}
	ar.mu.Lock()
	ar.status.Running = false
	ar.status.Pending = pending
	ar.status.Uploaded = len(ar.done)
	if failure != nil {
		ar.status.Error = failure.Error()
	} else {
		ar.status.Error, ar.status.LastOK = "", time.Now().UTC()
	}
	b, _ := json.Marshal(ar.done)
	ar.mu.Unlock()
	_ = os.WriteFile(filepath.Join(s.Store.Dir(), "archive.json"), b, 0o600)
	if sent > 0 || failure != nil {
		s.Bus.Publish("archive.ran", map[string]any{"sent": sent, "error": errString(failure)})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) archiveRoutes() {
	s.handle("GET /api/archive", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		ar := s.archiveState()
		ar.mu.Lock()
		st := ar.status
		ar.mu.Unlock()
		writeJSON(w, 200, st)
	})
	// test writes a small file to the configured place, so mistakes show at once.
	s.handle("POST /api/archive/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var a config.Archive
		if err := readJSON(r, &a); err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		if a.S3.SecretKey == "" && a.S3.AccessKey == cfg.System.Archive.S3.AccessKey {
			a.S3.SecretKey = cfg.System.Archive.S3.SecretKey
		}
		if cloud.IsReference(a.S3.SecretKey) {
			a.S3.SecretKey, _ = s.vault(cfg.System.Vault).Resolve(r.Context(), a.S3.SecretKey)
		}
		if a.Azure.ContainerSAS == "" {
			a.Azure.ContainerSAS = cfg.System.Archive.Azure.ContainerSAS
		}
		// Check the form first, so a missing bucket is reported under its field, not as a network error.
		check := cfg
		check.System.Archive = a
		if err := check.Validate(); err != nil {
			var all config.Problems
			if errors.As(err, &all) {
				var mine config.Problems
				for _, p := range all {
					if strings.HasPrefix(p.Field, "system.archive") {
						mine = append(mine, p)
					}
				}
				if len(mine) > 0 {
					writeErr(w, mine)
					return
				}
			}
		}
		tgt := targetFor(a)
		if tgt == nil {
			writeError(w, http.StatusBadRequest, "choose where to archive first")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		body := []byte("BitVoice archive test " + time.Now().UTC().Format(time.RFC3339) + "\n")
		if err := tgt.Put(ctx, "bitvoice-test.txt", body, "text/plain"); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"wrote": "bitvoice-test.txt in " + tgt.Describe()})
	})
	s.handle("POST /api/archive/run", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		select {
		case s.archiveState().wake <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
