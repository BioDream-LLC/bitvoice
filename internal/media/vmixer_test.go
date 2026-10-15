package media

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGridLayout(t *testing.T) {
	for _, c := range []struct{ n, cols, rows, tw, th int }{{1, 1, 1, 1280, 720}, {2, 2, 1, 640, 720}, {3, 2, 2, 640, 360}, {4, 2, 2, 640, 360}, {5, 3, 2, 426, 360}, {9, 3, 3, 426, 240}} {
		cols, rows, tw, th := GridLayout(c.n, 1280, 720)
		if cols != c.cols || rows != c.rows || tw != c.tw || th != c.th {
			t.Errorf("%d: %d %d %d %d", c.n, cols, rows, tw, th)
		}
	}
	if f := gridFilter(3, 1280, 720, 15); !strings.Contains(f, "xstack=inputs=3:layout=0_0|640_0|0_360") {
		t.Fatal(f)
	}
}

// Two cameras (ffmpeg's test picture) into a grid: the grid comes out as one stream.
func TestVideoMixer(t *testing.T) {
	if !FFmpegHas("") {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got atomic.Int32
	start := time.Now()
	var first atomic.Int64
	m, err := StartVideoMixer(ctx, "", VideoCodec{Name: "H264"}, 3, func(pkt []byte) {
		if pkt[1]&0x7f == gridOutPT {
			first.CompareAndSwap(0, int64(time.Since(start)))
			got.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for i := range 3 {
		src, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		defer src.Close()
		go func() {
			buf := make([]byte, 2048)
			for {
				n, err := src.Read(buf)
				if err != nil {
					return
				}
				m.Write(i, buf[:n])
			}
		}()
		cam := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x240:rate=15,hue=h=%d", i*120),
			"-t", "8", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-g", "15",
			"-payload_type", "100", "-f", "rtp", fmt.Sprintf("rtp://127.0.0.1:%d", src.LocalAddr().(*net.UDPAddr).Port))
		if err := cam.Start(); err != nil {
			t.Fatal(err)
		}
		defer cam.Wait()
	}
	deadline := time.Now().Add(8 * time.Second)
	for got.Load() < 50 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("first grid packet after %v", time.Duration(first.Load()))
	if got.Load() < 50 || m.LastOut().IsZero() {
		t.Fatalf("the grid sent %d packets", got.Load())
	}
}
