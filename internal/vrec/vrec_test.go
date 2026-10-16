package vrec

import (
	"bytes"
	"context"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rtpFrom has ffmpeg encode a test picture and send it as RTP; it returns the packets.
func rtpFrom(t *testing.T, ff string, args ...string) [][]byte {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	port := c.LocalAddr().(*net.UDPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	full := append([]string{"-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=4"}, args...)
	full = append(full, "-f", "rtp", "rtp://127.0.0.1:"+strconv.Itoa(port)+"?pkt_size=1200")
	cmd := exec.CommandContext(ctx, ff, full...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var pkts [][]byte
	b := make([]byte, 2000)
	for {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(b)
		if err != nil {
			break
		}
		pkts = append(pkts, append([]byte(nil), b[:n]...))
	}
	if err := <-done; err != nil {
		t.Fatalf("ffmpeg: %v %s", err, errb.String())
	}
	return pkts
}

func probeFrames(t *testing.T, ff, file string) (int, string) {
	probe := filepath.Join(filepath.Dir(ff), "ffprobe")
	out, err := exec.Command(probe, "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "csv=p=0", file).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v %s", err, out)
	}
	f := strings.Split(strings.TrimSpace(string(out)), ",")
	n, _ := strconv.Atoi(f[len(f)-1])
	// and decoded to the end without an error
	if out, err := exec.Command(ff, "-v", "error", "-xerror", "-i", file, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("ffmpeg could not decode %s cleanly: %v %s", file, err, out)
	}
	return n, strings.Join(f[:len(f)-1], ",")
}

func TestRecordAgainstFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	for _, c := range []struct {
		codec, want string
		args        []string
	}{
		{"H264", "h264,320,240", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-g", "15", "-an"}},
		{"VP8", "vp8,320,240", []string{"-c:v", "libvpx", "-g", "15", "-b:v", "300k"}},
	} {
		t.Run(c.codec, func(t *testing.T) {
			pkts := rtpFrom(t, ff, c.args...)
			if len(pkts) < 60 {
				t.Fatalf("only %d packets", len(pkts))
			}
			// in order
			var buf bytes.Buffer
			r, _ := New(&buf, c.codec)
			for _, p := range pkts {
				r.Write(p)
			}
			r.Close()
			file := filepath.Join(t.TempDir(), "v"+r.Extension())
			os.WriteFile(file, buf.Bytes(), 0o600)
			n, info := probeFrames(t, ff, file)
			if info != c.want || n != 60 || r.Frames() != 60 {
				t.Fatalf("in order: %s, %d frames read, %d written", info, n, r.Frames())
			}
			// the pictures keep their time: 60 frames at 15 a second last four seconds
			probe := filepath.Join(filepath.Dir(ff), "ffprobe")
			out, _ := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", file).Output()
			times := strings.Fields(string(out))
			last, _ := strconv.ParseFloat(strings.TrimSuffix(times[len(times)-1], ","), 64)
			if last < 3.8 || last > 4.1 {
				t.Fatalf("last picture at %.3fs, want about 3.93s", last)
			}
			// shuffled a little (as the internet does): the same file
			rng := rand.New(rand.NewSource(1))
			sh := append([][]byte(nil), pkts...)
			for i := 0; i+3 < len(sh); i += 4 {
				j := i + rng.Intn(4)
				sh[i], sh[j] = sh[j], sh[i]
			}
			var buf2 bytes.Buffer
			r2, _ := New(&buf2, c.codec)
			for _, p := range sh {
				r2.Write(p)
			}
			r2.Close()
			if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
				t.Fatal("reordered packets gave a different file")
			}
			// lost packets: whole pictures are dropped, never damaged ones, and what is kept decodes
			var buf3 bytes.Buffer
			r3, _ := New(&buf3, c.codec)
			for i, p := range pkts {
				if i == len(pkts)/3 || i == len(pkts)/3+1 {
					continue
				}
				r3.Write(p)
			}
			r3.Close()
			file3 := filepath.Join(t.TempDir(), "lost"+r.Extension())
			os.WriteFile(file3, buf3.Bytes(), 0o600)
			n3, _ := probeFrames(t, ff, file3)
			if n3 >= 60 || n3 < 30 || n3 != r3.Frames() {
				t.Fatalf("with loss: %d read, %d written", n3, r3.Frames())
			}
		})
	}
}

func FuzzRecorder(f *testing.F) {
	f.Add([]byte{0x80, 0x60, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0x7c, 0x85, 1, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, c := range []string{"H264", "VP8"} {
			r, _ := New(&bytes.Buffer{}, c)
			for i := 0; i+12 < len(b); i += 7 {
				r.Write(b[i:min(len(b), i+40)])
			}
			r.Close()
		}
	})
}
