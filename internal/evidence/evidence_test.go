package evidence

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Files made by ffmpeg and tagged by ExifTool (BITVOICE_TEST_EVIDENCE names their folder): photo.jpg
// with GPS and DateTimeOriginal; videos with a creation time and a position in each of the three
// ways phones and tools write it (3GPP loci, QuickTime keys, ©xyz).
func TestAgainstExifTool(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_EVIDENCE")
	if dir == "" {
		t.Skip("set BITVOICE_TEST_EVIDENCE to the folder of the tagged test files")
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-4 }
	for name, want := range map[string]struct {
		kind  string
		taken time.Time
	}{
		"photo.jpg":    {"photo", time.Date(2026, 10, 15, 14, 5, 30, 0, time.UTC)},
		"clip.mp4":     {"video", time.Date(2026, 10, 15, 14, 6, 0, 0, time.UTC)},
		"clipfast.mp4": {"video", time.Date(2026, 10, 15, 14, 7, 0, 0, time.UTC)},
		"clipkeys.mov": {"video", time.Date(2026, 10, 15, 14, 8, 0, 0, time.UTC)}, // the QuickTime keys an iPhone writes
		"clipxyz.mp4":  {"video", time.Date(2026, 10, 15, 14, 6, 0, 0, time.UTC)}, // ©xyz, as Android writes it
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		m := Read(bytes.NewReader(b), int64(len(b)))
		if m.Kind != want.kind || !m.Taken.Equal(want.taken) || !m.HasGPS || !near(m.Lat, 40.2633) || !near(m.Lon, -76.8823) {
			t.Errorf("%s: %+v", name, m)
		}
	}
}

// Damaged or unknown files never panic.
func FuzzRead(f *testing.F) {
	f.Add([]byte("\xff\xd8\xff\xe1\x00\x10Exif\x00\x00MM\x00*\x00\x00\x00\x08"))
	f.Add([]byte("\x00\x00\x00\x10ftypisom\x00\x00\x00\x08moov"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_ = Read(bytes.NewReader(b), int64(len(b)))
	})
}
