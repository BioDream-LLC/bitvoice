package media

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// H.264 from one party becomes VP8 for the other, through ffmpeg (skipped where it is not installed).
func TestVideoTranscoder(t *testing.T) {
	if !FFmpegHas("") {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got atomic.Int32
	var badPT atomic.Int32
	tr, err := StartVideoTranscoder(ctx, "", VideoCodec{Name: "H264", PT: 96, Fmtp: "packetization-mode=1"}, VideoCodec{Name: "VP8", PT: 100},
		func(pkt []byte) {
			if pkt[1]&0x7f != 100 {
				badPT.Add(1)
			}
			got.Add(1)
		})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	// A phone's H.264 camera: ffmpeg's test picture, sent as RTP to a port we relay into the transcoder.
	src, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer src.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			tr.Write(buf[:n])
		}
	}()
	time.Sleep(300 * time.Millisecond) // ffmpeg is listening
	cam := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-t", "4", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-g", "15",
		"-payload_type", "96", "-f", "rtp", fmt.Sprintf("rtp://127.0.0.1:%d", src.LocalAddr().(*net.UDPAddr).Port))
	if err := cam.Run(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for got.Load() < 20 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got.Load() < 20 || badPT.Load() != 0 {
		t.Fatalf("VP8 packets out: %d (wrong type %d)", got.Load(), badPT.Load())
	}
}
