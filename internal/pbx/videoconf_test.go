package pbx

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// speak sends a second and a half of a loud tone, so the room hears this member talking.
func speak(d *dialog, ssrc uint32) {
	for i := 0; i < 75; i++ {
		pkt := make([]byte, 172)
		pkt[0] = 0x80
		binary.BigEndian.PutUint16(pkt[2:], uint16(i))
		binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
		binary.BigEndian.PutUint32(pkt[8:], ssrc)
		for j := 0; j < 160; j++ {
			pkt[12+j] = media.LinearToUlaw(int16(12000 * math.Sin(2*math.Pi*400*float64(i*160+j)/8000)))
		}
		_, _ = d.p.rtp.WriteToUDPAddrPort(pkt, d.media)
		time.Sleep(20 * time.Millisecond)
	}
}

// pictures sends video packets marked with who sent them (byte 12) to BitVoice, from each phone in turn.
type vphone struct {
	p    *phone
	port int // BitVoice's video port for this phone
}

func (v vphone) send(mark byte, seq int) {
	pkt := make([]byte, 600)
	pkt[0], pkt[1] = 0x80, byte(v.p.videoPT)
	binary.BigEndian.PutUint16(pkt[2:], uint16(seq))
	binary.BigEndian.PutUint32(pkt[4:], uint32(seq*3000))
	binary.BigEndian.PutUint32(pkt[8:], uint32(mark)<<8|7)
	pkt[12] = mark
	_, _ = v.p.video.WriteToUDPAddrPort(pkt, netip.MustParseAddrPort("127.0.0.1:"+strconv.Itoa(v.port)))
}

// seen reads what arrives at a phone's video socket for a moment: the marks seen, and the SSRCs and
// payload types they came with.
func (v vphone) seen() (marks map[byte]int, ssrcs map[uint32]bool, pts map[int]bool) {
	marks, ssrcs, pts = map[byte]int{}, map[uint32]bool{}, map[int]bool{}
	buf := make([]byte, 2048)
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = v.p.video.SetReadDeadline(deadline)
		n, _, err := v.p.video.ReadFromUDPAddrPort(buf)
		if err != nil {
			break
		}
		if n == 600 {
			marks[buf[12]]++
			ssrcs[binary.BigEndian.Uint32(buf[8:])] = true
			pts[int(buf[1]&0x7f)] = true
		}
	}
	return
}

// A video room follows the speaker: everyone sees whoever talks, the speaker sees the one before, each
// in its own payload type and as one steady stream (one SSRC); a phone without the room's codec gets
// sound only.
func TestVideoRoomFollowsTheSpeaker(t *testing.T) {
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "700", Name: "Video room", Video: "talker", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Conferences[0].VideoCodec != "H264" {
		t.Fatalf("codec %q", cfg.Conferences[0].VideoCodec)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	pts := []int{96, 99, 102}
	var vs []vphone
	var ds []*dialog
	for i, p := range ps {
		p.video, p.videoPT = videoSocket(t), pts[i]
		d, code := p.dial("700")
		if code != 200 {
			t.Fatalf("%s joined with %d", p.user, code)
		}
		m := videoOf(t, d.body)
		if m == nil || m.Port == 0 {
			t.Fatalf("%s: no video in the room's answer", p.user)
		}
		if f := m.Formats; len(f) != 1 || f[0] != strconv.Itoa(pts[i]) {
			t.Fatalf("%s: answer formats %v", p.user, f)
		}
		vs = append(vs, vphone{p: p, port: m.Port})
		ds = append(ds, d)
		time.Sleep(100 * time.Millisecond)
	}
	pump := func(rounds int) {
		for i := 0; i < rounds; i++ {
			for k, v := range vs {
				v.send(byte('A'+k), i)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	look := func() []map[byte]int {
		done := make(chan struct{})
		go func() { pump(30); close(done) }()
		var out []map[byte]int
		for k, v := range vs {
			marks, ssrcs, got := v.seen()
			if len(ssrcs) > 1 {
				t.Fatalf("%s got %d SSRCs", v.p.user, len(ssrcs))
			}
			for pt := range got {
				if pt != pts[k] {
					t.Fatalf("%s got payload type %d, wants %d", v.p.user, pt, pts[k])
				}
			}
			out = append(out, marks)
		}
		<-done
		return out
	}
	// 100 came first: 101 and 102 see 100; 100 sees the one after it.
	got := look()
	if got[1]['A'] == 0 || got[2]['A'] == 0 || got[0]['A'] != 0 || got[1]['B'] != 0 {
		t.Fatalf("before anyone spoke: %v", got)
	}
	// 102 speaks: everyone sees 102, and 102 sees 100.
	speak(ds[2], 5)
	time.Sleep(400 * time.Millisecond)
	got = look()
	if got[0]['C'] == 0 || got[1]['C'] == 0 || got[2]['A'] == 0 || got[2]['C'] != 0 {
		t.Fatalf("after 102 spoke: %v", got)
	}
	if v := e.Conferences(); len(v) != 1 || v[0].Video != "talker" || v[0].Showing == "" {
		t.Fatalf("view %+v", v)
	}
	// 102 leaves: the picture goes back to the one before.
	ps[2].send(ds[2].request(sip.BYE))
	time.Sleep(300 * time.Millisecond)
	vs = vs[:2]
	got = look()
	if got[1]['A'] == 0 {
		t.Fatalf("after 102 left: %v", got)
	}
	// A phone without H.264 joins with sound only.
	c := registered(t, e, "102")[0]
	c.video, c.videoCodec = videoSocket(t), "VP8"
	d, code := c.dial("700")
	if code != 200 {
		t.Fatal(code)
	}
	if m := videoOf(t, d.body); m != nil && m.Port != 0 {
		t.Fatalf("a VP8 phone was given video in an H.264 room: %+v", m)
	}
}

// A leader room: everyone sees the leader from the moment the leader joins, and the leader sees
// whoever speaks.
func TestVideoRoomShowsTheLeader(t *testing.T) {
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "701", Name: "Briefing", Video: "leader", LeaderPIN: "4321", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	pts := []int{96, 97, 98}
	var vs []vphone
	var ds []*dialog
	for i, p := range ps {
		p.video, p.videoPT = videoSocket(t), pts[i]
		d, code := p.dial("701")
		if code != 200 {
			t.Fatal(code)
		}
		p.gotRTP(time.Second)
		var seq uint16 = 1
		if i == 1 {
			d.press(&seq, "4321#") // 101 is the leader
		} else {
			d.press(&seq, "#")
		}
		vs = append(vs, vphone{p: p, port: videoOf(t, d.body).Port})
		ds = append(ds, d)
		time.Sleep(1500 * time.Millisecond)
	}
	look := func() []map[byte]int {
		done := make(chan struct{})
		go func() {
			for i := 0; i < 30; i++ {
				for k, v := range vs {
					v.send(byte('A'+k), i)
				}
				time.Sleep(10 * time.Millisecond)
			}
			close(done)
		}()
		var out []map[byte]int
		for _, v := range vs {
			m, _, _ := v.seen()
			out = append(out, m)
		}
		<-done
		return out
	}
	got := look()
	if got[0]['B'] == 0 || got[2]['B'] == 0 {
		t.Fatalf("the leader is not on screen: %v", got)
	}
	speak(ds[2], 9)
	time.Sleep(400 * time.Millisecond)
	got = look()
	if got[1]['C'] == 0 || got[0]['B'] == 0 || got[2]['B'] == 0 {
		t.Fatalf("after 102 spoke: %v", got)
	}
}

// A grid room: three phones send real H.264 (ffmpeg's test picture); each gets one grid stream back,
// in its own payload type and one SSRC; a phone whose camera stops leaves the grid. Without ffmpeg the
// room follows the speaker instead.
func TestVideoRoomGrid(t *testing.T) {
	if !media.FFmpegHas("") {
		t.Skip("ffmpeg is not installed")
	}
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "700", Name: "Grid room", Video: "grid", Quiet: true}, {Number: "701", Name: "No ffmpeg", Video: "grid", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	pts := []int{96, 99, 102}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cams := make([]*exec.Cmd, 3)
	for i, p := range ps {
		p.video, p.videoPT = videoSocket(t), pts[i]
		d, code := p.dial("700")
		if code != 200 {
			t.Fatalf("%s joined with %d", p.user, code)
		}
		m := videoOf(t, d.body)
		to := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(m.Port))
		relay, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		defer relay.Close()
		go func(p *phone, pt int) {
			buf := make([]byte, 2048)
			for {
				n, err := relay.Read(buf)
				if err != nil {
					return
				}
				buf[1] = buf[1]&0x80 | byte(pt)
				_, _ = p.video.WriteToUDPAddrPort(buf[:n], to)
			}
		}(p, pts[i])
		cams[i] = exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
			"-t", "30", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-g", "15",
			"-payload_type", "96", "-f", "rtp", "rtp://"+relay.LocalAddr().String())
		if err := cams[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		cancel()
		for _, c := range cams {
			_ = c.Wait()
		}
	}()
	// What a phone receives for a moment: packets, SSRCs, payload types.
	recv := func(p *phone, d time.Duration) (n int, ssrcs map[uint32]bool, got map[int]bool) {
		ssrcs, got = map[uint32]bool{}, map[int]bool{}
		buf := make([]byte, 2048)
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = p.video.SetReadDeadline(end)
			k, _, err := p.video.ReadFromUDPAddrPort(buf)
			if err != nil {
				break
			}
			if k > 12 {
				n++
				ssrcs[binary.BigEndian.Uint32(buf[8:])] = true
				got[int(buf[1]&0x7f)] = true
			}
		}
		return
	}
	grid := func() []string {
		for _, r := range e.Conferences() {
			if r.Number == "700" {
				return r.Grid
			}
		}
		return nil
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(grid()) != 3 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if g := grid(); len(g) != 3 {
		t.Fatalf("grid %v", g)
	}
	started := time.Now()
	for time.Since(started) < 25*time.Second {
		r := e.roomFor("700")
		r.video.mu.Lock()
		g := r.video.grid
		r.video.mu.Unlock()
		if g != nil && !g.LastOut().IsZero() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("the grid's first picture after %v", time.Since(started))
	for i, p := range ps {
		n, ssrcs, got := recv(p, 6*time.Second)
		if n < 30 || len(ssrcs) != 1 || len(got) != 1 || !got[pts[i]] {
			t.Fatalf("%s got %d grid packets, SSRCs %v, payload types %v", p.user, n, ssrcs, got)
		}
	}
	// 102's camera stops: the grid is made again with two.
	_ = cams[2].Process.Kill()
	deadline = time.Now().Add(10 * time.Second)
	for len(grid()) != 2 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if g := grid(); len(g) != 2 {
		t.Fatalf("after a camera stopped: %v", g)
	}
	if n, _, _ := recv(ps[2], 6*time.Second); n < 20 {
		t.Fatalf("102, its camera off, still sees the grid: got %d", n)
	}
}

func TestVideoRoomGridWithoutFFmpeg(t *testing.T) {
	cfg := testConfig()
	cfg.System.FFmpeg = "/nonexistent/ffmpeg"
	cfg.Conferences = []config.Conference{{Number: "700", Name: "Grid room", Video: "grid", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	p := registered(t, e, "100")[0]
	p.video, p.videoPT = videoSocket(t), 96
	if _, code := p.dial("700"); code != 200 {
		t.Fatal(code)
	}
	if v := e.Conferences(); len(v) != 1 || v[0].Video != "talker" {
		t.Fatalf("%+v", v)
	}
}
