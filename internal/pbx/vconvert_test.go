package pbx

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// An H.264 phone calls a VP8-only one: the offer carries VP8 too, the caller is answered in H.264,
// and its camera reaches the other phone as VP8.
func TestVideoConvertedBetweenCodecs(t *testing.T) {
	if !media.FFmpegHas("") {
		t.Skip("ffmpeg is not installed")
	}
	cfg := testConfig()
	cfg.System.VideoTranscode = true
	e, _ := startEngine(t, cfg)
	a := registered(t, e, "100")[0]
	b := registered(t, e, "101")[0]
	a.video, b.video, b.videoPT, b.videoCodec = videoSocket(t), videoSocket(t), 100, "VP8"
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	tx := b.waitRing(3 * time.Second)
	if tx == nil {
		t.Fatal("no ring")
	}
	if off := string(tx.Req.Body); !strings.Contains(off, "VP8/90000") || !strings.Contains(off, "H264/90000") {
		t.Fatalf("the offer should carry VP8 beside H.264:\n%s", off)
	}
	b.answer(tx)
	d := <-done
	ans := videoOf(t, d.body)
	if ans == nil || ans.Port == 0 || !strings.Contains(string(d.body), "H264/90000") || strings.Contains(string(d.body), "VP8") {
		t.Fatalf("the caller should be answered in H.264:\n%s", d.body)
	}
	// The caller's camera: H.264 RTP into BitVoice's port for the caller.
	cam := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-t", "4", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-g", "15",
		"-payload_type", "96", "-f", "rtp", fmt.Sprintf("rtp://127.0.0.1:%d", ans.Port))
	go cam.Run()
	buf := make([]byte, 2048)
	got := 0
	_ = b.video.SetReadDeadline(time.Now().Add(8 * time.Second))
	for got < 10 {
		n, err := b.video.Read(buf)
		if err != nil {
			t.Fatalf("VP8 packets at the callee: %d (%v)", got, err)
		}
		if n > 12 && buf[1]&0x7f == 100 {
			got++
		}
	}
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)
}
