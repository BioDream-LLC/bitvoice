// Package whisper runs speech to text on this server: whisper.cpp's whisper-server (MIT licence), shipped
// beside BitVoice in its downloads, started, watched and restarted by BitVoice itself, with its model
// file downloaded on first use and checked against its SHA-256. BitVoice stays pure Go: whisper runs as
// its own process on 127.0.0.1, so a fault in it can never take calls down, and callers' audio never
// leaves the computer.
package whisper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/config"
)

// Model is one whisper model file BitVoice can download.
type Model struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"-"`
}

// Models are the models offered, smallest first. Every one understands about 100 languages; the larger
// ones are more accurate and slower. Sizes and hashes are those Hugging Face publishes for
// ggerganov/whisper.cpp (each checked after download).
var Models = []Model{
	{"tiny", "Tiny: fastest, least accurate", "ggml-tiny-q5_1.bin", 32152673, "818710568da3ca15689e31a743197b520007872ff9576237bda97bd1b469c3d7"},
	{"base", "Base: fast, good on clear speech", "ggml-base-q5_1.bin", 59707625, "422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898"},
	{"small", "Small: more accurate, needs a faster computer", "ggml-small-q5_1.bin", 190085487, "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb"},
	{"medium", "Medium: accurate, slow without a GPU", "ggml-medium-q5_0.bin", 539212467, "19fea4b380c3a618ec4723c3eef2eb785ffba0d0538cf43f8f235e7b3b34220f"},
	{"large-v3-turbo", "Large v3 turbo: most accurate, for a GPU or a big server", "ggml-large-v3-turbo-q5_0.bin", 574041195, "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2"},
}

// ModelFor returns the model named (the default for "").
func ModelFor(name string) (Model, bool) {
	if name == "" {
		name = config.DefaultSpeechModel
	}
	for _, m := range Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// ProgramName is whisper.cpp's server program.
func ProgramName() string {
	if runtime.GOOS == "windows" {
		return "whisper-server.exe"
	}
	return "whisper-server"
}

// Status is where speech to text on this server stands.
type Status struct {
	// State: off, no-program (whisper-server is not installed), downloading, starting, running, failed.
	State      string    `json:"state"`
	Model      string    `json:"model,omitempty"`
	Program    string    `json:"program,omitempty"`
	Downloaded int64     `json:"downloaded,omitempty"`
	Total      int64     `json:"total,omitempty"`
	Error      string    `json:"error,omitempty"`
	Since      time.Time `json:"since"`
	Restarts   int       `json:"restarts,omitempty"`
	Models     []Model   `json:"models"`
}

// Manager keeps whisper-server running while speech to text on this server is on.
type Manager struct {
	Dir string // where models and the log go (DATA/whisper)
	Log *slog.Logger
	// Find returns whisper-server's path; by default it looks beside BitVoice, then on the PATH.
	Find func() (string, error)
	// ModelURL is where a model file is downloaded from.
	ModelURL func(file string) string
	HTTP     *http.Client
	// OnChange is told every change of status.
	OnChange func(Status)

	mu     sync.Mutex
	on     bool
	model  string
	st     Status
	url    string
	cancel context.CancelFunc
	done   chan struct{}
}

// New makes a manager keeping its files in dir.
func New(dir string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{Dir: dir, Log: log, st: Status{State: "off", Since: time.Now()}}
}

// Apply turns speech to text on this server on or off, with this model; nothing happens when neither
// changed.
func (m *Manager) Apply(on bool, model string) {
	if model == "" {
		model = config.DefaultSpeechModel
	}
	m.mu.Lock()
	if m.on == on && (!on || m.model == model) {
		m.mu.Unlock()
		return
	}
	m.on, m.model = on, model
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.url = nil, nil, ""
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if !on {
		m.set(Status{State: "off"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done = cancel, done
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.run(ctx, model)
	}()
}

// Close stops whisper-server.
func (m *Manager) Close() { m.Apply(false, "") }

// Status is the current status.
func (m *Manager) Status() Status {
	if m == nil {
		return Status{State: "off", Models: Models}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.Models = Models
	return s
}

// URL is whisper-server's address while it runs, or why it cannot be used now.
func (m *Manager) URL() (string, error) {
	if m == nil {
		return "", errors.New("speech to text on this server is not available here")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.url != "":
		return m.url, nil
	case !m.on:
		return "", errors.New("speech to text on this server is off")
	case m.st.State == "downloading":
		return "", fmt.Errorf("speech to text on this server is still downloading its model (%d of %d MB)", m.st.Downloaded>>20, m.st.Total>>20)
	case m.st.Error != "":
		return "", errors.New("speech to text on this server is not working: " + m.st.Error)
	}
	return "", errors.New("speech to text on this server is starting")
}

// Client is the AI client for the settings: with speech to text on this server, transcripts go to
// whisper-server here and nowhere else (when it is not ready, transcribing fails and says why, never
// falling back to an outside service).
func (m *Manager) Client(a config.AI, allow func() error) *ai.Client {
	c := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel,
		TranscribeURL: a.TranscribeURL, TranscribeKey: a.TranscribeKey, TranscribeModel: a.TranscribeModel, Allow: allow}
	if a.LocalSpeech {
		c.TranscribeKey, c.TranscribeModel = "", ""
		c.TranscribeURL, c.TranscribeErr = m.URL()
	}
	return c
}

func (m *Manager) set(s Status) {
	s.Since = time.Now()
	m.mu.Lock()
	if s.Model == "" && s.State != "off" {
		s.Model = m.model
	}
	if s.State == m.st.State && s.Error == m.st.Error && s.Downloaded == m.st.Downloaded {
		s.Since = m.st.Since
	}
	m.st = s
	m.mu.Unlock()
	if m.OnChange != nil {
		m.OnChange(m.Status())
	}
}

// find looks for whisper-server beside this program, then beside the installed one, then on the PATH.
func (m *Manager) find() (string, error) {
	if m.Find != nil {
		return m.Find()
	}
	if p := os.Getenv("BITVOICE_WHISPER_SERVER"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", errors.New(p + " is not installed")
		}
		return p, nil
	}
	var dirs []string
	if self, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(self); err == nil {
			self = real
		}
		dirs = append(dirs, filepath.Dir(self))
	}
	switch runtime.GOOS {
	case "windows":
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			dirs = append(dirs, filepath.Join(pf, "BitVoice"))
		}
	default:
		dirs = append(dirs, "/usr/local/bin", "/opt/bitvoice")
	}
	for _, d := range dirs {
		p := filepath.Join(d, ProgramName())
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath(ProgramName()); err == nil {
		return p, nil
	}
	return "", errors.New(ProgramName() + " is not installed beside BitVoice (it comes in BitVoice's downloads, or install whisper.cpp)")
}

// run downloads the model if needed and keeps whisper-server running until ctx ends.
func (m *Manager) run(ctx context.Context, name string) {
	model, ok := ModelFor(name)
	if !ok {
		m.set(Status{State: "failed", Error: "no model called " + name})
		return
	}
	prog, err := m.find()
	if err != nil {
		m.set(Status{State: "no-program", Error: err.Error()})
		m.Log.Warn("speech to text on this server cannot start", "err", err)
		return
	}
	path, err := m.ensureModel(ctx, model, prog)
	if err != nil {
		if ctx.Err() == nil {
			m.set(Status{State: "failed", Program: prog, Error: err.Error()})
			m.Log.Warn("speech to text on this server: the model could not be fetched", "model", model.Name, "err", err)
		}
		return
	}
	backoff, restarts := time.Second, 0
	for ctx.Err() == nil {
		up := time.Now()
		err := m.serve(ctx, prog, path, restarts)
		if ctx.Err() != nil {
			return
		}
		if time.Since(up) > time.Minute {
			backoff = time.Second // it ran fine for a while
		}
		restarts++
		m.set(Status{State: "failed", Program: prog, Error: err.Error(), Restarts: restarts})
		m.Log.Warn("speech to text on this server stopped; starting it again", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// ensureModel returns the model file's path, downloading it first when it is missing or damaged.
func (m *Manager) ensureModel(ctx context.Context, model Model, prog string) (string, error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(m.Dir, model.File)
	if fi, err := os.Stat(path); err == nil && fi.Size() == model.Size {
		if sum, err := fileSHA256(path); err == nil && sum == model.SHA256 {
			return path, nil
		}
		m.Log.Warn("speech to text model file is damaged; fetching it again", "file", path)
	}
	url := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + model.File
	if m.ModelURL != nil {
		url = m.ModelURL(model.File)
	}
	m.set(Status{State: "downloading", Program: prog, Total: model.Size})
	m.Log.Info("speech to text on this server: fetching its model", "model", model.Name, "MB", model.Size>>20)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	hc := m.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Hour}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching the model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching the model: the server answered %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(m.Dir, model.File+".*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	var got int64
	buf := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if got+int64(n) > model.Size {
				tmp.Close()
				return "", errors.New("fetching the model: the file is larger than it should be")
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return "", fmt.Errorf("saving the model: %w", err)
			}
			h.Write(buf[:n])
			got += int64(n)
			if time.Since(last) > 500*time.Millisecond {
				m.set(Status{State: "downloading", Program: prog, Downloaded: got, Total: model.Size})
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			return "", fmt.Errorf("fetching the model: %w", rerr)
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if got != model.Size || hex.EncodeToString(h.Sum(nil)) != model.SHA256 {
		return "", errors.New("the model file downloaded is not the published one (its SHA-256 differs); not used")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// serve runs whisper-server once, until it exits or ctx ends.
func (m *Manager) serve(ctx context.Context, prog, model string, restarts int) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	threads := max(1, min(8, runtime.NumCPU()/2))
	args := []string{"-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "-l", "auto", "-t", strconv.Itoa(threads)}
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = m.Dir // whisper-server writes temporary files where it runs
	logf, err := os.OpenFile(filepath.Join(m.Dir, "whisper-server.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err == nil {
		defer logf.Close()
		cmd.Stdout, cmd.Stderr = logf, logf
	}
	cmd.WaitDelay = 5 * time.Second
	tieToBitVoice(cmd)
	m.set(Status{State: "starting", Program: prog, Restarts: restarts})
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", prog, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(3 * time.Minute) // a large model on a slow disk takes a while to load
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("whisper-server ended while starting (%v); its log is %s", err, filepath.Join(m.Dir, "whisper-server.log"))
		case <-time.After(200 * time.Millisecond):
		}
		if healthy(ctx, base) {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-exited
			return errors.New("whisper-server did not come up within 3 minutes")
		}
	}
	m.mu.Lock()
	m.url = base + "/inference"
	m.mu.Unlock()
	m.set(Status{State: "running", Program: prog, Restarts: restarts})
	m.Log.Info("speech to text on this server is running", "program", prog, "model", filepath.Base(model), "threads", threads)
	err = <-exited
	m.mu.Lock()
	m.url = ""
	m.mu.Unlock()
	if err == nil {
		err = errors.New("exited")
	}
	return fmt.Errorf("whisper-server stopped: %v", err)
}

func healthy(ctx context.Context, base string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
