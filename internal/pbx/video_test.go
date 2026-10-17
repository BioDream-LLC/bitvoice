package pbx

import (
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

func videoSocket(t *testing.T) *net.UDPConn {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func videoOf(t *testing.T, body []byte) *sdp.Media {
	s, err := sdp.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Media {
		if m.Type == "video" {
			return m
		}
	}
	return nil
}

// sendVideo sends one RTP packet of payload type pt to BitVoice's video port, and reports the payload
// type the other phone received it as (-1: nothing arrived).
func sendVideo(t *testing.T, from, to *net.UDPConn, port int, pt byte) int {
	pkt := make([]byte, 1200) // a video packet is far bigger than a sound one
	pkt[0], pkt[1] = 0x80, pt
	for i := 0; i < 5; i++ {
		_, _ = from.WriteToUDPAddrPort(pkt, netip.MustParseAddrPort("127.0.0.1:"+strconv.Itoa(port)))
		_ = to.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 2048)
		if n, _, err := to.ReadFromUDPAddrPort(buf); err == nil && n == 1200 {
			return int(buf[1] & 0x7f)
		}
	}
	return -1
}

// Two video phones see each other through BitVoice, which renumbers the payload type each chose; a
// phone without video gets sound only; with video switched off, nobody gets video.
func TestVideoCall(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	callers := registered(t, e, "100")
	callees := registered(t, e, "101")
	a, b := callers[0], callees[0]
	a.video, b.video, b.videoPT = videoSocket(t), videoSocket(t), 99
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	tx := b.waitRing(3 * time.Second)
	if tx == nil {
		t.Fatal("no ring")
	}
	offered := videoOf(t, tx.Req.Body)
	if offered == nil || offered.Port == 0 || offered.Port == a.video.LocalAddr().(*net.UDPAddr).Port {
		t.Fatalf("video not offered through BitVoice: %+v", offered)
	}
	b.answer(tx)
	d := <-done
	if d == nil {
		t.Fatal("not answered")
	}
	answered := videoOf(t, d.body)
	if answered == nil || answered.Port == 0 {
		t.Fatalf("the caller's answer has no video: %+v", answered)
	}
	// a sends H.264 as 96; b chose 99 and must receive 99, and the other way round.
	if got := sendVideo(t, a.video, b.video, answered.Port, 96); got != 99 {
		t.Fatalf("callee received payload type %d", got)
	}
	if got := sendVideo(t, b.video, a.video, offered.Port, 99); got != 96 {
		t.Fatalf("caller received payload type %d", got)
	}
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)

	// A phone without video: the caller's video is refused in the answer.
	c := registered(t, e, "102")[0]
	go func() { d, _ := a.dial("102"); done <- d }()
	tx = c.waitRing(3 * time.Second)
	if m := videoOf(t, tx.Req.Body); m == nil || m.Port == 0 {
		t.Fatal("video was not offered to the phone")
	}
	c.answer(tx)
	d = <-done
	if m := videoOf(t, d.body); m != nil && m.Port != 0 {
		t.Fatalf("the caller was told video goes on: %+v", m)
	}
	a.send(d.request(sip.BYE))
	c.waitBye(t, 2*time.Second)

	// Video switched off: not even offered.
	cfg := *e.Config()
	cfg.System.VideoOff = true
	e.Apply(cfg)
	go func() { d, _ := a.dial("101"); done <- d }()
	tx = b.waitRing(3 * time.Second)
	if m := videoOf(t, tx.Req.Body); m != nil && m.Port != 0 {
		t.Fatalf("video offered while switched off: %+v", m)
	}
	b.answer(tx)
	d = <-done
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)
}

// A party that switches its camera on during a call gets video when the other party takes it, and
// sound only when it does not.
func TestVideoAddedDuringCall(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	b := registered(t, e, "101")[0]
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	tx := b.waitRing(3 * time.Second)
	b.answer(tx)
	d := <-done
	if d == nil {
		t.Fatal("not answered")
	}
	reinvite := func() *sdp.Media {
		m := d.request(sip.INVITE)
		m.Set("Content-Type", "application/sdp")
		m.Set("Contact", a.contact())
		m.Body = a.offer("sendrecv")
		r := a.send(m)
		if r == nil || r.StatusCode != 200 {
			t.Fatalf("re-INVITE: %v", r)
		}
		ack := d.request(sip.ACK)
		n, _ := r.CSeq()
		ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
		_ = a.stack.Stateless(ack, a.pbx)
		return videoOf(t, r.Body)
	}
	// b has no camera: a's video is refused, the call goes on.
	a.setVideo(videoSocket(t))
	if m := reinvite(); m != nil && m.Port != 0 {
		t.Fatalf("video accepted for a party without it: %+v", m)
	}
	select {
	case <-b.reinvite:
	case <-time.After(15 * time.Second):
		t.Fatal("the other party was not asked")
	}
	// Now b has one: a switches on again and gets b's picture.
	b.setVideo(videoSocket(t))
	m := reinvite()
	if m == nil || m.Port == 0 {
		t.Fatal("video refused although both have it")
	}
	var bm *sdp.Media
	select {
	case r := <-b.reinvite:
		bm = videoOf(t, r.Body)
	case <-time.After(15 * time.Second):
		t.Fatal("the other party was not asked")
	}
	if bm == nil || bm.Port == 0 {
		t.Fatalf("b was not offered video: %+v", bm)
	}
	if got := sendVideo(t, a.video, b.video, m.Port, 96); got != 96 {
		t.Fatalf("b received %d", got)
	}
	if got := sendVideo(t, b.video, a.video, bm.Port, 96); got != 96 {
		t.Fatalf("a received %d", got)
	}
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)
}
