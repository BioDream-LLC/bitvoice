package pbx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// An internet radio stream plays to everyone on hold with its class over one connection; it closes
// when nobody listens; while the stream is down, the class's tracks play.
func TestHoldMusicStream(t *testing.T) {
	mp3, err := os.ReadFile("../media/mp3/testdata/m2_22k.mp3")
	if err != nil {
		t.Fatal(err)
	}
	var conns, up atomic.Int32
	up.Store(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if up.Load() == 0 {
			w.WriteHeader(503)
			return
		}
		conns.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		for i := 0; i < 50; i++ { // like a radio: about real time, after a first burst
			for p := 0; p < len(mp3); p += 3000 {
				if _, err := w.Write(mp3[p:min(p+3000, len(mp3))]); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				if p > 12000 {
					time.Sleep(50 * time.Millisecond)
				}
				select {
				case <-r.Context().Done():
					return
				default:
				}
			}
		}
	}))
	defer srv.Close()
	cfg := testConfig()
	cfg.System.Music = []config.MusicClass{{ID: "radio", Name: "Radio", Stream: srv.URL + "/live"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Music: fakeMusic{"a": make([]int16, 800)}, Log: slog.Default()}
	fallback := func() []int16 { return []int16{7, 7, 7} }
	next1, leave1 := e.listen("radio", cfg.System.Music[0].Stream, fallback)
	next2, leave2 := e.listen("radio", cfg.System.Music[0].Stream, fallback)
	got := func(next func() []int16) int {
		deadline := time.Now().Add(3 * time.Second)
		n := 0
		for time.Now().Before(deadline) && n < 8000 {
			for _, s := range next() {
				if s != 0 && s != 7 {
					n++
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		return n
	}
	if a, b := got(next1), got(next2); a < 8000 || b < 8000 {
		t.Fatalf("listeners heard %d and %d samples of the stream", a, b)
	}
	if conns.Load() != 1 {
		t.Fatalf("%d connections for two listeners", conns.Load())
	}
	if st := e.MusicStreams(); len(st) != 1 || st[0].Listening != 2 || !st[0].Live {
		t.Fatalf("status %+v", st)
	}
	leave1()
	leave2()
	// Down: the tracks (here the fallback) play.
	up.Store(0)
	srv.CloseClientConnections()
	next3, leave3 := e.listen("radio", cfg.System.Music[0].Stream, fallback)
	defer leave3()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b := next3(); len(b) > 0 && b[0] == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tracks did not play while the stream was down")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if bad := cfg; true {
		bad.System.Music = []config.MusicClass{{ID: "x", Name: "X", Stream: "ftp://radio"}}
		if bad.Validate() == nil {
			t.Fatal("a non-http stream accepted")
		}
	}
}
