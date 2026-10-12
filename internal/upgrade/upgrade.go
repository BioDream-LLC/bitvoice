// Package upgrade replaces the running BitVoice program with another version and back again. A new
// version is checked (it must run and report its version, and be signed when this build carries a
// release key), kept beside the old one, and started in its place; if it fails to come up twice, the
// old version is started instead. The program you installed stays where it is and acts as a launcher,
// so service managers (systemd, launchd, Windows services) keep working unchanged.
package upgrade

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// State is kept in upgrade.json in the data directory.
type State struct {
	// Current is the program to run, and its version; empty runs the installed program.
	Current        string `json:"current"`
	CurrentVersion string `json:"currentVersion"`
	// Previous is what Current replaced: the one-click way back.
	Previous        string `json:"previous"`
	PreviousVersion string `json:"previousVersion"`
	// Staged is a checked new version waiting to be started.
	Staged        string `json:"staged"`
	StagedVersion string `json:"stagedVersion"`
	StagedSigned  bool   `json:"stagedSigned"`
	// Pending is true from the switch until the new version has run for a while; Attempts counts starts.
	Pending  bool      `json:"pending"`
	Attempts int       `json:"attempts"`
	At       time.Time `json:"at,omitzero"`
	History  []Entry   `json:"history"`
}

// Entry is one thing that happened, for the screen.
type Entry struct {
	At   time.Time `json:"at"`
	What string    `json:"what"`
}

func (s *State) note(format string, a ...any) {
	s.History = append([]Entry{{time.Now().UTC(), fmt.Sprintf(format, a...)}}, s.History...)
	if len(s.History) > 50 {
		s.History = s.History[:50]
	}
}

func path(data string) string { return filepath.Join(data, "upgrade.json") }

// Load reads the state; a missing file is an empty state.
func Load(data string) State {
	var s State
	b, err := os.ReadFile(path(data))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Save writes the state atomically.
func Save(data string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path(data) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path(data))
}

// Action is what the program should do as it starts.
type Action struct {
	Exec   string // start this program instead, with the same arguments
	Reason string
}

// Decide is the choice at start-up. self is the running program's path; launched is true when this
// process was started by the launcher (so it serves rather than looking again, and never loops). The
// process that will serve counts the attempt; after two failed starts of a new version it starts the
// previous one instead.
func Decide(data, self string, launched bool) Action {
	s := Load(data)
	if !launched {
		if a := target(s.Current, self, "upgraded"); a.Exec != "" {
			return a
		}
	}
	if !s.Pending {
		return Action{}
	}
	s.Attempts++
	if s.Attempts > 2 && s.Previous != "" {
		s.note("version %s did not start properly; went back to %s", s.CurrentVersion, s.PreviousVersion)
		s.Current, s.CurrentVersion = s.Previous, s.PreviousVersion
		s.Previous, s.PreviousVersion = "", ""
		s.Pending, s.Attempts = false, 0
		_ = Save(data, s)
		if a := target(s.Current, self, "rolled back"); a.Exec != "" {
			return a
		}
		return Action{}
	}
	_ = Save(data, s)
	return Action{}
}

// target starts path unless it is the running program (or missing, or empty: the installed program).
func target(p, self, reason string) Action {
	if p == "" || p == "installed" {
		return Action{}
	}
	if same(p, self) {
		return Action{}
	}
	if _, err := os.Stat(p); err != nil {
		return Action{}
	}
	return Action{Exec: p, Reason: reason}
}

func same(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// Confirm marks a pending switch as good, once the new version has stayed up.
func Confirm(data, version string) {
	s := Load(data)
	if !s.Pending {
		return
	}
	s.Pending, s.Attempts = false, 0
	s.note("version %s is running", version)
	_ = Save(data, s)
}

var versionRe = regexp.MustCompile(`^bitvoice (\S+)\s*$`)

// Stage checks a new program and keeps it in data/bin. key is this build's release public key
// (base64 ed25519), empty in development builds; with a key, the signature is required.
func Stage(ctx context.Context, data string, program, signature []byte, key string) (State, error) {
	s := Load(data)
	signed := false
	if key != "" || len(signature) > 0 {
		pub, err := base64.StdEncoding.DecodeString(key)
		if key == "" || err != nil || len(pub) != ed25519.PublicKeySize {
			if key != "" {
				return s, errors.New("this build's release key is damaged")
			}
		} else {
			sig := bytes.TrimSpace(signature)
			if d, err := base64.StdEncoding.DecodeString(string(sig)); err == nil {
				sig = d
			}
			if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, program, sig) {
				return s, errors.New("the signature does not match: this is not a BioDream release, or the file is damaged")
			}
			signed = true
		}
		if key != "" && !signed {
			return s, errors.New("this server only accepts signed releases: add the .sig file that came with it")
		}
	}
	dir := filepath.Join(data, "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return s, err
	}
	tmp, err := os.CreateTemp(dir, "incoming-*")
	if err != nil {
		return s, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(program); err != nil {
		tmp.Close()
		return s, err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return s, err
	}
	// It must run here and say it is BitVoice.
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, tmp.Name(), "-version").Output()
	m := versionRe.FindStringSubmatch(strings.TrimSpace(string(out)) + "\n")
	if err != nil || m == nil {
		return s, fmt.Errorf("that file is not a BitVoice program for this computer (%s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	ver := m[1]
	name := "bitvoice-" + regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(ver, "_")
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	final := filepath.Join(dir, name)
	if same(final, s.Current) {
		return s, fmt.Errorf("version %s is already running", ver)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return s, err
	}
	if s.Staged != "" && s.Staged != final && s.Staged != s.Current && s.Staged != s.Previous {
		_ = os.Remove(s.Staged)
	}
	s.Staged, s.StagedVersion, s.StagedSigned = final, ver, signed
	s.note("version %s was uploaded and checked%s", ver, map[bool]string{true: ", signed by BioDream", false: ", unsigned"}[signed])
	return s, Save(data, s)
}

// Switch makes the staged version current; running is the running program and its version. The caller
// then restarts.
func Switch(data, running, runningVersion string) (State, error) {
	s := Load(data)
	if s.Staged == "" {
		return s, errors.New("upload a new version first")
	}
	if s.Current == "" {
		s.Current, s.CurrentVersion = running, runningVersion
	}
	old := s.Previous
	s.Previous, s.PreviousVersion = s.Current, s.CurrentVersion
	s.Current, s.CurrentVersion = s.Staged, s.StagedVersion
	s.Staged, s.StagedVersion, s.StagedSigned = "", "", false
	s.Pending, s.Attempts, s.At = true, 0, time.Now().UTC()
	s.note("switching from %s to %s", s.PreviousVersion, s.CurrentVersion)
	if old != "" && old != s.Previous && old != s.Current && strings.HasPrefix(old, filepath.Join(data, "bin")) {
		_ = os.Remove(old) // keep one version to go back to, not every one ever installed
	}
	return s, Save(data, s)
}

// Rollback makes the previous version current again. The caller then restarts.
func Rollback(data string) (State, error) {
	s := Load(data)
	if s.Previous == "" {
		return s, errors.New("there is no earlier version to go back to")
	}
	s.note("went back from %s to %s", s.CurrentVersion, s.PreviousVersion)
	s.Current, s.Previous = s.Previous, s.Current
	s.CurrentVersion, s.PreviousVersion = s.PreviousVersion, s.CurrentVersion
	s.Pending, s.Attempts, s.At = false, 0, time.Now().UTC()
	return s, Save(data, s)
}
