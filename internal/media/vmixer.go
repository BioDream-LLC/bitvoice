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
	"sync/atomic"
	"time"
)

// MaxGridTiles is the most pictures a grid shows at once (3 by 3).
const MaxGridTiles = 9

// GridLayout gives the grid for n pictures: columns and rows, each tile's size, inside w by h.
func GridLayout(n, w, h int) (cols, rows, tw, th int) {
	cols = 1
	for cols*cols < n {
		cols++
	}
	rows = (n + cols - 1) / cols
	even := func(x int) int { return x &^ 1 }
	return cols, rows, even(w / cols), even(h / rows)
}

// gridFilter is ffmpeg's filter graph: each picture scaled into its tile (kept in proportion, black
// around it) and the tiles stacked.
func gridFilter(n, w, h, fps int) string {
	cols, _, tw, th := GridLayout(n, w, h)
	var b strings.Builder
	for i := range n {
		// Each frame timed by the clock as it passes, so the pictures of different phones line up
		// however their own clocks run, and a late one never holds the others back.
		fmt.Fprintf(&b, "[%d:v]setpts=(RTCTIME-RTCSTART)/(TB*1000000),fps=%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:-1:-1:color=black,setsar=1[t%d];", i, fps, tw, th, tw, th, i)
	}
	if n == 1 {
		fmt.Fprintf(&b, "[t0]pad=%d:%d:-1:-1:color=black[o]", w, h)
		return b.String()
	}
	var lay []string
	for i := range n {
		lay = append(lay, fmt.Sprintf("%d_%d", (i%cols)*tw, (i/cols)*th))
	}
	for i := range n {
		fmt.Fprintf(&b, "[t%d]", i)
	}
	fmt.Fprintf(&b, "xstack=inputs=%d:layout=%s:fill=black,pad=%d:%d:-1:-1:color=black[o]", n, strings.Join(lay, "|"), w, h)
	return b.String()
}

// VideoMixer puts several video streams side by side in one picture, by running ffmpeg (decoding
// each, scaling, stacking, encoding once): an MCU for video rooms. Each input is plain RTP on its own
// loopback port; the grid comes back as RTP. BitVoice keeps the network, SRTP and the members.
type VideoMixer struct {
	n      int
	ports  []int
	w      *net.UDPConn
	out    *net.UDPConn
	cmd    *exec.Cmd
	dir    string
	once   sync.Once
	done   chan struct{}
	lastIn atomic.Int64
	last   atomic.Int64 // when the last grid packet came, unix ns
}

// gridInPT and gridOutPT are the payload numbers on the loopback side.
const gridInPT, gridOutPT = 96, 97

// StartVideoMixer starts a grid of n pictures in the codec (H264 or VP8), 1280 by 720 at 15 frames a
// second. deliver gets each RTP packet of the grid (payload number 97; the caller rewrites it).
func StartVideoMixer(ctx context.Context, ffmpeg string, codec VideoCodec, n int, deliver func(pkt []byte)) (*VideoMixer, error) {
	if n < 1 || n > MaxGridTiles {
		return nil, errors.New("a grid holds 1 to 9 pictures")
	}
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return nil, errors.New("a video grid needs ffmpeg, which is not installed")
	}
	dir, err := os.MkdirTemp("", "bitvoice-grid-")
	if err != nil {
		return nil, err
	}
	m := &VideoMixer{n: n, dir: dir, done: make(chan struct{})}
	fail := func(err error) (*VideoMixer, error) {
		if m.w != nil {
			m.w.Close()
		}
		if m.out != nil {
			m.out.Close()
		}
		os.RemoveAll(dir)
		return nil, err
	}
	in := VideoCodec{Name: codec.Name, PT: gridInPT, Fmtp: codec.Fmtp}
	if strings.EqualFold(codec.Name, "H264") && in.Fmtp == "" {
		in.Fmtp = "packetization-mode=1"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	for i := range n {
		probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return fail(err)
		}
		port := probe.LocalAddr().(*net.UDPAddr).Port
		probe.Close()
		m.ports = append(m.ports, port)
		p := filepath.Join(dir, fmt.Sprintf("in%d.sdp", i))
		if err := os.WriteFile(p, []byte(sdpFor(in, port)), 0o600); err != nil {
			return fail(err)
		}
		// No probing: the inputs open one after the other, and each picture's size is learnt from
		// its first full frame.
		args = append(args, "-protocol_whitelist", "file,udp,rtp", "-fflags", "nobuffer", "-probesize", "32", "-analyzeduration", "0",
			"-reorder_queue_size", "0", "-i", p)
	}
	if m.w, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
		return fail(err)
	}
	if m.out, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
		return fail(err)
	}
	args = append(args, "-filter_complex", gridFilter(n, 1280, 720, 15), "-map", "[o]", "-r", "15")
	args = append(args, encoderArgs(VideoCodec{Name: codec.Name})...)
	args = append(args, "-payload_type", fmt.Sprint(gridOutPT), "-f", "rtp", "-pkt_size", "1200",
		fmt.Sprintf("rtp://127.0.0.1:%d", m.out.LocalAddr().(*net.UDPAddr).Port))
	m.cmd = exec.CommandContext(ctx, path, args...)
	if err := m.cmd.Start(); err != nil {
		return fail(err)
	}
	go func() {
		_ = m.cmd.Wait()
		m.Close()
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := m.out.Read(buf)
			if err != nil {
				return
			}
			if n >= 12 {
				m.last.Store(time.Now().UnixNano())
				deliver(append([]byte(nil), buf[:n]...))
			}
		}
	}()
	return m, nil
}

// Write gives the mixer one RTP packet of picture i.
func (m *VideoMixer) Write(i int, pkt []byte) {
	if i < 0 || i >= m.n || len(pkt) < 12 {
		return
	}
	select {
	case <-m.done:
		return
	default:
	}
	b := append([]byte(nil), pkt...)
	b[1] = b[1]&0x80 | gridInPT
	m.lastIn.Store(time.Now().UnixNano())
	_ = m.w.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = m.w.WriteToUDP(b, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: m.ports[i]})
}

// LastOut says when the grid last sent a packet (zero before the first).
func (m *VideoMixer) LastOut() time.Time {
	if ns := m.last.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// Done is closed when the mixer stopped.
func (m *VideoMixer) Done() <-chan struct{} { return m.done }

// Close stops ffmpeg and frees its ports.
func (m *VideoMixer) Close() {
	m.once.Do(func() {
		close(m.done)
		if m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		m.w.Close()
		m.out.Close()
		os.RemoveAll(m.dir)
	})
}
