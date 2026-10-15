package pbx

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/tty"
)

// sosCall is a 911 call from the ESInet, answered by call-taker 101: the caller's media socket and
// where the engine wants the caller's media sent.
type sosCall struct {
	e      *Engine
	id     string
	caller *net.UDPConn
	to     *net.UDPAddr
	taker  *phone
	seq    uint16

	carrier *carrier
	invite  *sip.Message
	final   *sip.Message
	answer  *sdp.Session
	pbx     sip.Peer
}

// placeSOS places a 911 call from the ESInet with extra SDP media lines, and has 101 answer it.
func placeSOS(t *testing.T, extra string, setup ...func(*Engine)) *sosCall {
	t.Helper()
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].ESInet = true
	cfg.Trunks[0].Register = false
	cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
	cfg.InboundRoutes[0].DID = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg, setup...)
	taker := registered(t, e, "101")[0]
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	port := conn.LocalAddr().(*net.UDPAddr).Port
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sd := string(sdp.New("127.0.0.1", port, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()) + extra
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + sd + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag=sos1")
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	tx := c.stack.Request(inv, pbx)
	ring := taker.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the 911 call did not reach the call-taker")
	}
	taker.answer(ring)
	r := finalOf(tx)
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("ESInet got %v", r)
	}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: "sip:" + pbx.Addr.String()}
	ack.Set("From", inv.Get("From"))
	ack.Set("To", r.Get("To"))
	ack.Set("Call-ID", inv.CallID())
	ack.Set("CSeq", "1 ACK")
	_ = c.stack.Stateless(ack, pbx)
	ans, err := sdp.Parse(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	a := ans.Audio()
	calls := e.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls %+v", calls)
	}
	return &sosCall{e: e, id: calls[0].ID, caller: conn, taker: taker, carrier: c, invite: inv, final: r, answer: ans, pbx: pbx, to: &net.UDPAddr{IP: net.ParseIP(ans.Connection(a)), Port: a.Port}}
}

// play sends audio from the caller at the pace a phone sends it.
func (s *sosCall) play(pcm []int16) {
	next := time.Now()
	for i := 0; i < len(pcm); i += 160 {
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, 0
		binary.BigEndian.PutUint16(pkt[2:], s.seq)
		binary.BigEndian.PutUint32(pkt[4:], uint32(s.seq)*160)
		s.seq++
		for _, v := range pcm[i:min(i+160, len(pcm))] {
			pkt = append(pkt, media.LinearToUlaw(v))
		}
		_, _ = s.caller.WriteToUDP(pkt, s.to)
		next = next.Add(20 * time.Millisecond)
		time.Sleep(time.Until(next))
	}
}

// heard collects what the caller hears for d as audio.
func (s *sosCall) heard(d time.Duration) []int16 {
	var out []int16
	buf := make([]byte, 2048)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		_ = s.caller.SetReadDeadline(end)
		n, _, err := s.caller.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n > 12 && buf[1]&0x7f == 0 {
			for _, b := range buf[12:n] {
				out = append(out, media.UlawToLinear(b))
			}
		}
	}
	return out
}

func (s *sosCall) conversation() []TextLine {
	for _, c := range s.e.Calls() {
		if c.ID == s.id && c.Emergency != nil {
			return c.Emergency.Text
		}
	}
	return nil
}

// A deaf caller's TTY on a 911 call: what it types shows on the call, a pause ends the line, and the
// call-taker's answer goes back as TTY tones the caller's TTY reads.
func TestTTYOn911(t *testing.T) {
	s := placeSOS(t, "")
	s.play(make([]int16, 4000)) // a moment of silence first, as a call starts
	s.play(tty.Encode("HELP MY FATHER FELL GA"))
	time.Sleep(200 * time.Millisecond)
	txt := s.conversation()
	if len(txt) != 1 || txt[0].From != "caller" || txt[0].Via != "tty" || txt[0].Text != "HELP MY FATHER FELL GA" {
		t.Fatalf("conversation %+v", txt)
	}
	got := make(chan []int16, 1)
	go func() { got <- s.heard(6 * time.Second) }()
	l, err := s.e.SendCallText(s.id, "", "Is he breathing? GA", "101", "Pat")
	if err != nil || l.Text != "IS HE BREATHING? GA" || l.Via != "tty" {
		t.Fatalf("sent %+v, %v", l, err)
	}
	if text := tty.NewDecoder().Feed(<-got); !strings.Contains(text, "IS HE BREATHING? GA") {
		t.Fatalf("the caller's TTY read %q", text)
	}
	txt = s.conversation()
	if len(txt) != 2 || txt[1].From != "taker" || txt[1].Name != "Pat" || txt[0].Open {
		t.Fatalf("conversation %+v", txt)
	}
	if _, err := s.e.SendCallText(s.id, "tty", "✓✓", "101", ""); err == nil {
		t.Fatal("text with nothing a TTY can show was sent")
	}
}

// Speech on a 911 call is never taken for a TTY.
func TestNoTTYFromVoice(t *testing.T) {
	s := placeSOS(t, "")
	pcm := make([]int16, 3*8000)
	for i := range pcm {
		pcm[i] = int16(3000 * (float64(i%57)/57 - 0.5))
	}
	s.play(pcm)
	if txt := s.conversation(); len(txt) != 0 {
		t.Fatalf("conversation %+v", txt)
	}
}
