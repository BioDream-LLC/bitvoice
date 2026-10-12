package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/upgrade"
)

// upgradeWait is a switch waiting for calls to finish.
type upgradeWait struct {
	mu     sync.Mutex
	action string // "switch" or "rollback"; empty when nothing waits
	since  time.Time
	cancel context.CancelFunc
}

type upgradeStatus struct {
	Running     string        `json:"running"`
	SignedBuild bool          `json:"signedBuild"`
	State       upgrade.State `json:"state"`
	Waiting     string        `json:"waiting"` // switch, rollback or ""
	Since       time.Time     `json:"since,omitzero"`
	Calls       int           `json:"calls"`
	Possible    bool          `json:"possible"` // false when this server cannot restart itself
}

func (s *Server) upgradeStatus() upgradeStatus {
	s.upWait.mu.Lock()
	defer s.upWait.mu.Unlock()
	st := upgrade.Load(s.DataDir)
	if st.History == nil {
		st.History = []upgrade.Entry{}
	}
	return upgradeStatus{Running: s.Version, SignedBuild: s.ReleaseKey != "", State: st, Waiting: s.upWait.action,
		Since: s.upWait.since, Calls: len(s.Engine.Calls()), Possible: s.Restart != nil && s.DataDir != ""}
}

func (s *Server) getUpgrade(w http.ResponseWriter, r *http.Request, u store.User) {
	writeJSON(w, 200, s.upgradeStatus())
}

// postUpgrade receives a new version: the program, and its .sig file for signed builds.
func (s *Server) postUpgrade(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.DataDir == "" {
		writeError(w, http.StatusServiceUnavailable, "upgrades are not possible on this server")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 400<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "send the program as a file (at most 400 MB)")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("program")
	if err != nil {
		writeErr(w, config.Problems{{Field: "program", Message: "choose the BitVoice program file"}})
		return
	}
	program, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "the upload was cut short")
		return
	}
	var sig []byte
	if sf, _, err := r.FormFile("signature"); err == nil {
		sig, _ = io.ReadAll(io.LimitReader(sf, 4096))
		sf.Close()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := upgrade.Stage(ctx, s.DataDir, program, sig, s.ReleaseKey)
	if err != nil {
		s.audit(r, u.Username, "upgrade.refused", err.Error())
		writeErr(w, config.Problems{{Field: "program", Message: err.Error()}})
		return
	}
	s.audit(r, u.Username, "upgrade.uploaded", st.StagedVersion)
	writeJSON(w, 200, s.upgradeStatus())
}

// applyUpgrade switches to the uploaded version (or back to the previous one), now or once no calls
// are in progress.
func (s *Server) applyUpgrade(action string) func(w http.ResponseWriter, r *http.Request, u store.User) {
	return func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			When string `json:"when"` // now or idle
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if s.Restart == nil || s.DataDir == "" {
			writeError(w, http.StatusServiceUnavailable, "this server cannot restart itself")
			return
		}
		st := upgrade.Load(s.DataDir)
		if action == "switch" && st.Staged == "" {
			writeError(w, http.StatusConflict, "upload a new version first")
			return
		}
		if action == "rollback" && st.Previous == "" {
			writeError(w, http.StatusConflict, "there is no earlier version to go back to")
			return
		}
		s.upWait.mu.Lock()
		if s.upWait.cancel != nil {
			s.upWait.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.upWait.action, s.upWait.since, s.upWait.cancel = action, time.Now(), cancel
		s.upWait.mu.Unlock()
		s.audit(r, u.Username, "upgrade."+action, "when "+in.When)
		go s.waitAndRestart(ctx, action, in.When != "now")
		writeJSON(w, 200, s.upgradeStatus())
	}
}

func (s *Server) cancelUpgrade(w http.ResponseWriter, r *http.Request, u store.User) {
	s.upWait.mu.Lock()
	if s.upWait.cancel != nil {
		s.upWait.cancel()
	}
	s.upWait.action, s.upWait.cancel = "", nil
	s.upWait.mu.Unlock()
	s.audit(r, u.Username, "upgrade.cancelled", "")
	writeJSON(w, 200, s.upgradeStatus())
}

func (s *Server) waitAndRestart(ctx context.Context, action string, idle bool) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for idle && len(s.Engine.Calls()) > 0 {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	var err error
	self, _ := os.Executable()
	if action == "switch" {
		_, err = upgrade.Switch(s.DataDir, self, s.Version)
	} else {
		_, err = upgrade.Rollback(s.DataDir)
	}
	if err != nil {
		s.Log.Warn("upgrade could not switch", "err", err)
		s.upWait.mu.Lock()
		s.upWait.action = ""
		s.upWait.mu.Unlock()
		return
	}
	s.Log.Info("restarting for "+action, "from", s.Version)
	if s.Bus != nil {
		s.Bus.Publish("system.restarting", map[string]string{"action": action})
	}
	time.Sleep(300 * time.Millisecond) // let the answer and the event reach the browser
	s.Restart()
}
