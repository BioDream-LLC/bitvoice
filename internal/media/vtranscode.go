package media

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// VideoCodec is a video codec BitVoice can convert with ffmpeg, as RTP names it.
type VideoCodec struct {
	Name string // H264 or VP8
	PT   int    // the payload number on the side it is sent to or received from
	Fmtp string
}

// VideoTranscodable names the video codecs converted, best first.
var VideoTranscodable = []string{"H264", "VP8"}

// CanTranscodeVideo says whether a video codec can be converted.
func CanTranscodeVideo(name string) bool {
	for _, n := range VideoTranscodable {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// VideoTranscoder converts one direction of a video stream by running ffmpeg: RTP in on one local
// port, the other codec's RTP out on another. BitVoice keeps the network side (addresses, SRTP); ffmpeg
// only sees plain RTP on the loopback address, and is stopped with the call.
type VideoTranscoder struct {
	in   *net.UDPConn // what BitVoice sends ffmpeg
	out  *net.UDPConn // what ffmpeg sends back
	to   *net.UDPAddr
	cmd  *exec.Cmd
	dir  string
	once sync.Once
	done chan struct{}
}

// sdpFor describes the stream ffmpeg reads.
func sdpFor(c VideoCodec, port int) string {
	name := strings.ToUpper(c.Name)
	s := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=BitVoice\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=video %d RTP/AVP %d\r\na=rtpmap:%d %s/90000\r\n", port, c.PT, c.PT, name)
	if c.Fmtp != "" {
		s += fmt.Sprintf("a=fmtp:%d %s\r\n", c.PT, c.Fmtp)
	}
	return s
}

func encoderArgs(to VideoCodec) []string {
	switch strings.ToUpper(to.Name) {
	case "VP8":
		return []string{"-c:v", "libvpx", "-pix_fmt", "yuv420p", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "800k", "-g", "30", "-error-resilient", "1"}
	}
	return []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline",
		"-b:v", "800k", "-g", "30", "-bsf:v", "h264_mp4toannexb"}
}

// StartVideoTranscoder starts ffmpeg converting from one codec to the other. deliver is called with
// each RTP packet ffmpeg makes (payload number to.PT), to send to the far side.
func StartVideoTranscoder(ctx context.Context, ffmpeg string, from, to VideoCodec, deliver func(pkt []byte)) (*VideoTranscoder, error) {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return nil, errors.New("video conversion needs ffmpeg, which is not installed")
	}
	// ffmpeg's input port: found free, then given up for ffmpeg to bind.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	inPort := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	out, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	in, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		out.Close()
		return nil, err
	}
	dir, err := os.MkdirTemp("", "bitvoice-video-")
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	sdpPath := filepath.Join(dir, "in.sdp")
	if err := os.WriteFile(sdpPath, []byte(sdpFor(from, inPort)), 0o600); err != nil {
		os.RemoveAll(dir)
		in.Close()
		out.Close()
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-protocol_whitelist", "file,udp,rtp", "-fflags", "nobuffer", "-flags", "low_delay",
		"-probesize", "32", "-analyzeduration", "0", "-reorder_queue_size", "0",
		"-i", sdpPath, "-an"}
	args = append(args, encoderArgs(to)...)
	args = append(args, "-payload_type", fmt.Sprint(to.PT), "-f", "rtp", "-pkt_size", "1200",
		fmt.Sprintf("rtp://127.0.0.1:%d", out.LocalAddr().(*net.UDPAddr).Port))
	cmd := exec.CommandContext(ctx, path, args...)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		in.Close()
		out.Close()
		return nil, err
	}
	t := &VideoTranscoder{in: in, out: out, to: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: inPort}, cmd: cmd, dir: dir, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		t.Close()
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := out.Read(buf)
			if err != nil {
				return
			}
			if n >= 12 {
				deliver(append([]byte(nil), buf[:n]...))
			}
		}
	}()
	return t, nil
}

// Write gives ffmpeg one RTP packet of the stream it converts.
func (t *VideoTranscoder) Write(pkt []byte) {
	select {
	case <-t.done:
		return
	default:
	}
	_ = t.in.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = t.in.WriteToUDP(pkt, t.to)
}

// Done is closed when the transcoder stopped (ffmpeg ended or Close).
func (t *VideoTranscoder) Done() <-chan struct{} { return t.done }

// Close stops ffmpeg and frees its ports.
func (t *VideoTranscoder) Close() {
	t.once.Do(func() {
		close(t.done)
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		t.in.Close()
		t.out.Close()
		os.RemoveAll(t.dir)
	})
}

// FFmpegHas says whether ffmpeg is installed, for the screens.
func FFmpegHas(ffmpeg string) bool {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	_, err := exec.LookPath(ffmpeg)
	return err == nil
}
