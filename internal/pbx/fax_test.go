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

func imageOf(t *testing.T, body []byte) *sdp.Media {
	s, err := sdp.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return imageMedia(s)
}

// A gateway switching a call to T.38: the other side is switched first, fax packets (not RTP) pass
// both ways, and going back to sound switches both back. A side without T.38 keeps both on sound.
func TestT38Fax(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	b := registered(t, e, "101")[0]
	call := func() *dialog {
		done := make(chan *dialog, 1)
		go func() { d, _ := a.dial("101"); done <- d }()
		tx := b.waitRing(3 * time.Second)
		b.answer(tx)
		d := <-done
		if d == nil {
			t.Fatal("not answered")
		}
		return d
	}
	reinvite := func(d *dialog, body []byte) *sip.Message {
		m := d.request(sip.INVITE)
		m.Set("Content-Type", "application/sdp")
		m.Set("Contact", a.contact())
		m.Body = body
		r := a.send(m)
		if r != nil && r.StatusCode == 200 {
			ack := d.request(sip.ACK)
			n, _ := r.CSeq()
			ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
			_ = a.stack.Stateless(ack, a.pbx)
		}
		return r
	}

	// b cannot do T.38: a is refused and both stay on sound.
	d := call()
	ag := videoSocket(t)
	if r := reinvite(d, t38Offer(ag)); r == nil || r.StatusCode != 488 {
		t.Fatalf("switch without T.38 on the other side: %v", r)
	}
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)
	<-b.reinvite

	// b can: both switch.
	b.t38 = videoSocket(t)
	d = call()
	r := reinvite(d, t38Offer(ag))
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("switch to T.38: %v", r)
	}
	ai := imageOf(t, r.Body)
	var bi *sdp.Media
	select {
	case m := <-b.reinvite:
		bi = imageOf(t, m.Body)
	case <-time.After(15 * time.Second):
		t.Fatal("b was not switched")
	}
	if ai == nil || bi == nil || ai.Port == bi.Port {
		t.Fatalf("image streams: a %+v, b %+v", ai, bi)
	}
	// UDPTL is not RTP: a packet starting 0x00 must pass as it is.
	pass := func(from, to *net.UDPConn, port int) bool {
		pkt := []byte{0x00, 0x01, 0x06, 0x80, 0x11, 0x22, 0x33}
		for i := 0; i < 5; i++ {
			_, _ = from.WriteToUDPAddrPort(pkt, netip.MustParseAddrPort("127.0.0.1:"+strconv.Itoa(port)))
			_ = to.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			buf := make([]byte, 64)
			if n, _, err := to.ReadFromUDPAddrPort(buf); err == nil && string(buf[:n]) == string(pkt) {
				return true
			}
		}
		return false
	}
	if !pass(ag, b.t38, ai.Port) || !pass(b.t38, ag, bi.Port) {
		t.Fatal("fax packets did not pass")
	}
	// Back to sound: b is switched back too.
	if r := reinvite(d, a.offer("sendrecv")); r == nil || r.StatusCode != 200 {
		t.Fatalf("back to sound: %v", r)
	}
	select {
	case m := <-b.reinvite:
		if s, _ := sdp.Parse(m.Body); s == nil || s.Audio() == nil || s.Audio().Port == 0 || imageMedia(s) != nil {
			t.Fatalf("b not switched back to sound: %s", m.Body)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("b was not switched back")
	}
	d.media = mediaOf(r.Body)
	a.send(d.request(sip.BYE))
	b.waitBye(t, 2*time.Second)
}
