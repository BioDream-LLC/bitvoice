package whisper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// The test binary stands in for whisper-server when BV_FAKE_WHISPER is set: it serves /health and
// /inference on the port it is given, and with BV_FAKE_WHISPER_DIE=1 it exits after a moment once.
func TestMain(m *testing.M) {
	if os.Getenv("BV_FAKE_WHISPER") == "" {
		os.Exit(m.Run())
	}
	fs := flag.NewFlagSet("whisper-server", flag.ContinueOnError)
	model := fs.String("m", "", "")
	host := fs.String("host", "", "")
	port := fs.String("port", "", "")
	fs.String("l", "", "")
	fs.String("t", "", "")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if b, err := os.ReadFile(*model); err != nil || !strings.HasPrefix(string(b), "fake model") {
		fmt.Fprintln(os.Stderr, "bad model")
		os.Exit(3)
	}
	if mark := os.Getenv("BV_FAKE_WHISPER_DIE"); mark != "" {
		if _, err := os.Stat(mark); err != nil {
			os.WriteFile(mark, nil, 0o600)
			go func() { time.Sleep(300 * time.Millisecond); os.Exit(1) }()
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("POST /inference", func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, "no file", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"text": " Help, my house is on fire.\n"})
	})
	fmt.Fprintln(os.Stderr, http.ListenAndServe(*host+":"+*port, mux))
	os.Exit(1)
}

// fakeModel replaces the "base" model with a small one served by a stand-in of Hugging Face.
func fakeModel(t *testing.T, body string, served *atomic.Int32) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	old := Models
	Models = slices.Clone(Models)
	for i := range Models {
		if Models[i].Name == config.DefaultSpeechModel {
			Models[i].File, Models[i].Size, Models[i].SHA256 = "ggml-test.bin", int64(len(body)), hex.EncodeToString(sum[:])
		}
	}
	t.Cleanup(func() { Models = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if served != nil {
			served.Add(1)
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func manager(t *testing.T, models *httptest.Server) *Manager {
	t.Helper()
	self, _ := os.Executable()
	t.Setenv("BV_FAKE_WHISPER", "1")
	m := New(t.TempDir(), nil)
	m.Find = func() (string, error) { return self, nil }
	m.ModelURL = func(f string) string { return models.URL + "/" + f }
	t.Cleanup(m.Close)
	return m
}

func waitState(t *testing.T, m *Manager, want string) Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s := m.Status()
		if s.State == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %q, want %q (%s)", s.State, want, s.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The model is fetched once and checked, whisper-server is started on 127.0.0.1, and transcripts go
// to it and nowhere else; turned off, it stops.
func TestRunsWhisperServer(t *testing.T) {
	var served atomic.Int32
	models := fakeModel(t, "fake model weights", &served)
	m := manager(t, models)
	var changes atomic.Int32
	m.OnChange = func(Status) { changes.Add(1) }
	m.Apply(true, "")
	s := waitState(t, m, "running")
	if s.Model != "base" || served.Load() != 1 || changes.Load() < 2 {
		t.Fatalf("%+v served=%d changes=%d", s, served.Load(), changes.Load())
	}
	u, err := m.URL()
	if err != nil || !strings.HasPrefix(u, "http://127.0.0.1:") || !strings.HasSuffix(u, "/inference") {
		t.Fatalf("%q %v", u, err)
	}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("the cloud service was asked") }))
	defer cloud.Close()
	c := m.Client(config.AI{Enabled: true, BaseURL: cloud.URL, Model: "m", LocalSpeech: true}, nil)
	text, err := c.Transcribe(context.Background(), []byte("RIFF"))
	if err != nil || text != "Help, my house is on fire." {
		t.Fatalf("%q %v", text, err)
	}
	// Started again, the model already there is checked, not fetched.
	m.Apply(false, "")
	if s := m.Status(); s.State != "off" {
		t.Fatalf("off: %+v", s)
	}
	if resp, err := http.Get(strings.TrimSuffix(u, "/inference") + "/health"); err == nil {
		resp.Body.Close()
		t.Fatal("whisper-server still answers after being turned off")
	}
	m.Apply(true, "")
	waitState(t, m, "running")
	if served.Load() != 1 {
		t.Fatalf("the model was fetched %d times", served.Load())
	}
}

// A model file that is not the published one is never used.
func TestModelChecked(t *testing.T) {
	models := fakeModel(t, "fake model weights", nil)
	for i := range Models {
		if Models[i].Name == "base" {
			Models[i].SHA256 = strings.Repeat("0", 64)
		}
	}
	m := manager(t, models)
	m.Apply(true, "base")
	s := waitState(t, m, "failed")
	if !strings.Contains(s.Error, "SHA-256") {
		t.Fatalf("%+v", s)
	}
	if left, _ := filepath.Glob(filepath.Join(m.Dir, "ggml-test.bin*")); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	if _, err := m.URL(); err == nil || !strings.Contains(err.Error(), "not working") {
		t.Fatalf("URL error %v", err)
	}
}

// whisper-server that dies is started again.
func TestRestartsWhisperServer(t *testing.T) {
	models := fakeModel(t, "fake model weights", nil)
	m := manager(t, models)
	t.Setenv("BV_FAKE_WHISPER_DIE", filepath.Join(t.TempDir(), "died"))
	m.Apply(true, "")
	waitState(t, m, "running")
	deadline := time.Now().Add(20 * time.Second)
	for m.Status().Restarts == 0 {
		if time.Now().After(deadline) {
			t.Fatal("never restarted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := waitState(t, m, "running")
	if s.Restarts != 1 {
		t.Fatalf("%+v", s)
	}
}

// With no whisper-server installed, transcribing says why and never falls back to the cloud model.
func TestNoProgram(t *testing.T) {
	m := New(t.TempDir(), nil)
	m.Find = func() (string, error) { return "", fmt.Errorf("whisper-server is not installed") }
	defer m.Close()
	m.Apply(true, "")
	waitState(t, m, "no-program")
	asked := false
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked = true }))
	defer cloud.Close()
	_, err := m.Client(config.AI{Enabled: true, BaseURL: cloud.URL, Model: "m", AudioModel: "m", LocalSpeech: true}, nil).Transcribe(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "not installed") || asked {
		t.Fatalf("%v asked=%v", err, asked)
	}
	// A nil manager (a server without it) says so too.
	var none *Manager
	if _, err := none.Client(config.AI{LocalSpeech: true}, nil).Transcribe(context.Background(), nil); err == nil {
		t.Fatal("no error")
	}
}

// The settings and this package offer the same models.
func TestModelsMatchSettings(t *testing.T) {
	var names []string
	for _, m := range Models {
		names = append(names, m.Name)
	}
	if !slices.Equal(names, config.SpeechModels) {
		t.Fatalf("%v vs %v", names, config.SpeechModels)
	}
	if _, ok := ModelFor(""); !ok {
		t.Fatal("no default model")
	}
}

// Against the real whisper-server: BITVOICE_TEST_WHISPER_SERVER=its path, BITVOICE_TEST_WHISPER_MODEL=a
// base model file (copied in, so nothing is downloaded), BITVOICE_TEST_WHISPER_WAV=a recording and
// BITVOICE_TEST_WHISPER_SAYS=words it holds.
func TestRealWhisperServer(t *testing.T) {
	prog, model, wavPath := os.Getenv("BITVOICE_TEST_WHISPER_SERVER"), os.Getenv("BITVOICE_TEST_WHISPER_MODEL"), os.Getenv("BITVOICE_TEST_WHISPER_WAV")
	if prog == "" || model == "" {
		t.Skip("set BITVOICE_TEST_WHISPER_SERVER and BITVOICE_TEST_WHISPER_MODEL")
	}
	m := New(t.TempDir(), nil)
	m.Find = func() (string, error) { return prog, nil }
	m.ModelURL = func(string) string { return "http://127.0.0.1:1/never" }
	defer m.Close()
	b, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := ModelFor("base")
	if err := os.WriteFile(filepath.Join(m.Dir, base.File), b, 0o600); err != nil {
		t.Fatal(err)
	}
	m.Apply(true, "base")
	deadline := time.Now().Add(3 * time.Minute)
	for m.Status().State != "running" {
		if s := m.Status(); s.State == "failed" || s.State == "no-program" || time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(m.Dir, "whisper-server.log"))
			t.Fatalf("%+v\n%s", s, log)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Without a recording: two seconds of phone-line hiss, which must come back as no speech at all.
	wav := media.EncodeWAV(make([]int16, 16000))
	if wavPath != "" {
		if wav, err = os.ReadFile(wavPath); err != nil {
			t.Fatal(err)
		}
	}
	text, err := m.Client(config.AI{Enabled: true, LocalSpeech: true}, nil).Transcribe(context.Background(), wav)
	if err != nil || !strings.Contains(strings.ToLower(text), strings.ToLower(os.Getenv("BITVOICE_TEST_WHISPER_SAYS"))) {
		t.Fatalf("%q %v", text, err)
	}
	t.Logf("whisper-server heard %q", text)
}
