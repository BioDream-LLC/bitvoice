package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Retention: files older than MediaDays are deleted, held ones kept, and so is the exposure log's
// recent part.
func TestCallMediaRetention(t *testing.T) {
	s, _ := newServer(t)
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "retention", rev, func(c *config.Config) error { c.System.PSAP.MediaDays = 7; return nil }); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mk := func(call string, age time.Duration, held bool) {
		os.MkdirAll(s.mediaDir(call), 0o700)
		os.WriteFile(filepath.Join(s.mediaDir(call), "1"), []byte("jpeg"), 0o600)
		s.writeIndex(mediaIndex{Call: call, Held: held, Items: []MediaItem{{N: 1, Name: "a.jpg", Received: now.Add(-age)}}})
	}
	mk("oldcall", 10*24*time.Hour, false)
	mk("heldcall", 10*24*time.Hour, true)
	mk("newcall", time.Hour, false)
	for _, x := range []Exposure{{At: now.Add(-9 * 24 * time.Hour), Who: "Old"}, {At: now.Add(-time.Hour), Who: "New"}} {
		b, _ := json.Marshal(x)
		f, _ := os.OpenFile(filepath.Join(s.DataDir, "callmedia", "exposure.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		f.Write(append(b, '\n'))
		f.Close()
	}
	s.sweepCallMedia(now)
	for call, want := range map[string]bool{"oldcall": false, "heldcall": true, "newcall": true} {
		if _, err := os.Stat(s.mediaDir(call)); (err == nil) != want {
			t.Errorf("%s kept %v, want %v", call, err == nil, want)
		}
	}
	if xs := s.exposures(now.Add(-30*24*time.Hour), now.Add(time.Hour)); len(xs) != 1 || xs[0].Who != "New" {
		t.Fatalf("exposure log %+v", xs)
	}
}
