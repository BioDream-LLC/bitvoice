package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/vrec"
)

// A caller's live video, recorded: kept with its hash, listed as a recording, and played in the
// browser from a copy ffmpeg makes without converting the pictures.
func TestCallVideoRecording(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	s, _ := newServer(t)
	for _, c := range []struct{ codec, enc, ct string }{{"VP8", "libvpx", "video/webm"}, {"H264", "libx264", "video/mp4"}} {
		call := "rec" + strings.ToLower(c.codec) + "01"
		w, done := s.recordCallVideo(call)
		if w == nil {
			t.Fatal("no recording")
		}
		r, _ := vrec.New(w, c.codec)
		for _, p := range ffmpegRTP(t, ff, "-c:v", c.enc, "-pix_fmt", "yuv420p", "-g", "15") {
			r.Write(p)
		}
		r.Close()
		done(r.Frames(), r.Err())
		ix, _ := s.readIndex(call)
		if len(ix.Items) != 1 || !ix.Items[0].Recording || ix.Items[0].Type != "video/x-ivf" {
			t.Fatalf("%s: %+v", c.codec, ix)
		}
		it := ix.Items[0]
		b, _ := os.ReadFile(filepath.Join(s.mediaDir(call), "1"))
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != it.SHA256 || int64(len(b)) != it.Size {
			t.Fatalf("%s: hash or size of the recording is wrong", c.codec)
		}
		req := httptest.NewRequest("GET", "/x?view=1", nil)
		rec := httptest.NewRecorder()
		if !s.serveMedia(rec, req, call, 1, Exposure{Who: "test"}) || rec.Code != 200 || rec.Header().Get("Content-Type") != c.ct {
			t.Fatalf("%s: viewing copy %d %q %s", c.codec, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		view := filepath.Join(t.TempDir(), "v")
		os.WriteFile(view, rec.Body.Bytes(), 0o600)
		if out, err := exec.Command(ff, "-v", "error", "-xerror", "-i", view, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
			t.Fatalf("%s: viewing copy does not play: %v %s", c.codec, err, out)
		}
		// the original is untouched by making the copy
		if b2, _ := os.ReadFile(filepath.Join(s.mediaDir(call), "1")); !bytes.Equal(b, b2) {
			t.Fatal("the original changed")
		}
	}
}

func ffmpegRTP(t *testing.T, ff string, enc ...string) [][]byte {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=2"}, enc...)
	args = append(args, "-an", "-f", "rtp", "rtp://127.0.0.1:"+strconv.Itoa(c.LocalAddr().(*net.UDPAddr).Port)+"?pkt_size=1200")
	cmd := exec.CommandContext(ctx, ff, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	var pkts [][]byte
	b := make([]byte, 2000)
	for {
		c.SetReadDeadline(time.Now().Add(15 * time.Second))
		n, err := c.Read(b)
		if err != nil {
			return pkts
		}
		pkts = append(pkts, append([]byte(nil), b[:n]...))
	}
}
