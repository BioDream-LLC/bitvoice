package pbx

import (
	"encoding/binary"
	"math"
	"net/netip"
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
