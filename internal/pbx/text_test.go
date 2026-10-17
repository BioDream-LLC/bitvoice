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
	"github.com/biodream-llc/bitvoice/internal/msrp"
	"github.com/biodream-llc/bitvoice/internal/rtt"
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
	return placeCall(t, extra, true, setup...)
}

// placeCall is placeSOS for a call from the ESInet (emergency true) or an ordinary carrier call.
func placeCall(t *testing.T, extra string, emergency bool, setup ...func(*Engine)) *sosCall {
	t.Helper()
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].ESInet = emergency
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
	if !emergency {
		inv.RequestURI = "sip:2125550100@127.0.0.1"
	}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag=sos1")
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	if emergency {
		inv.Set("Geolocation", "<cid:loc@esinet.test>")
	} else {
		inv.Set("Content-Type", "application/sdp")
		inv.Body = []byte(sd)
	}
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
	waitFor(t, "the TTY text is not in the conversation", func() bool {
		c := s.conversation()
		return len(c) == 1 && c[0].Text == "HELP MY FATHER FELL GA"
	})
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

// Real-time text beside the voice of a 911 call (RFC 4103): BitVoice answers the text stream itself,
// what the caller types shows on the call as it is typed, and the call-taker's reply goes back as text.
func TestRTTOn911(t *testing.T) {
	tc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	port := tc.LocalAddr().(*net.UDPAddr).Port
	s := placeSOS(t, "m=text "+strconv.Itoa(port)+" RTP/AVP 100 98\r\na=rtpmap:98 t140/1000\r\na=rtpmap:100 red/1000\r\na=fmtp:100 98/98/98\r\n")
	var tm *sdp.Media
	for _, m := range s.answer.Media {
		if m.Type == "text" {
			tm = m
		}
	}
	if tm == nil || tm.Port == 0 || !strings.Contains(string(s.answer.Bytes()), "t140/1000") || !strings.Contains(string(s.answer.Bytes()), "red/1000") {
		t.Fatalf("text not answered:\n%s", s.answer.Bytes())
	}
	to := &net.UDPAddr{IP: net.ParseIP(s.answer.Connection(tm)), Port: tm.Port}
	out := &rtt.Sender{T140: 98, Red: 100, SSRC: 5, Write: func(p []byte) { _, _ = tc.WriteToUDP(p, to) }}
	defer out.Close()
	for _, w := range []string{"I CAN", "T TALK", "\u2028", "HE HAS A GUN"} {
		out.Send(w)
		time.Sleep(350 * time.Millisecond)
	}
	waitFor(t, "the RTT lines are not in the conversation", func() bool { return len(s.conversation()) == 2 })
	txt := s.conversation()
	if len(txt) != 2 || txt[0].Text != "I CANT TALK" || txt[1].Text != "HE HAS A GUN" || txt[0].Via != "rtt" {
		t.Fatalf("conversation %+v", txt)
	}
	l, err := s.e.SendCallText(s.id, "", "Are you safe where you are?", "101", "Pat")
	if err != nil || l.Via != "rtt" {
		t.Fatalf("sent %+v %v", l, err)
	}
	in := &rtt.Receiver{T140: 98, Red: 100}
	var got strings.Builder
	buf := make([]byte, 1500)
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) && !strings.Contains(got.String(), "\u2028") {
		_ = tc.SetReadDeadline(end)
		n, _, err := tc.ReadFromUDP(buf)
		if err != nil {
			break
		}
		got.WriteString(in.Packet(buf[:n]))
	}
	if got.String() != "Are you safe where you are?\u2028" {
		t.Fatalf("the caller read %q", got.String())
	}
}

// A text stream on an ordinary call from a carrier is refused (port 0): only 911 calls take text, at
// the call-taker's screen.
func TestTextRefusedOnOtherCalls(t *testing.T) {
	s := placeCall(t, "m=text 40100 RTP/AVP 98\r\na=rtpmap:98 t140/1000\r\n", false)
	for _, m := range s.answer.Media {
		if m.Type == "text" && m.Port != 0 {
			t.Fatalf("text answered on an ordinary call:\n%s", s.answer.Bytes())
		}
	}
	if len(s.answer.Media) != 2 {
		t.Fatalf("the answer must keep every offered line:\n%s", s.answer.Bytes())
	}
}

// textSession offers a text-to-911 session from the ESInet (no voice), as a TCC does, and returns the
// call's ID and BitVoice's answer.
func textSession(t *testing.T, e *Engine, c *carrier, msrpLines string) (string, *sdp.Session) {
	t.Helper()
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sd := "v=0\r\no=tcc 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" + msrpLines
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + sd + "\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550123@tcc.esinet.test>;tag=txt1")
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:tcc@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Geolocation", "<cid:loc@esinet.test>")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	r := finalOf(c.stack.Request(inv, pbx))
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("TCC got %v", r)
	}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: "sip:" + pbx.Addr.String()}
	ack.Set("From", inv.Get("From"))
	ack.Set("To", r.Get("To"))
	ack.Set("Call-ID", inv.CallID())
	ack.Set("CSeq", "1 ACK")
	_ = c.stack.Stateless(ack, pbx)
	ans, err := sdp.Parse(r.Body)
	if err != nil || len(ans.Media) != 1 || ans.Media[0].Type != "message" {
		t.Fatalf("answer %v:\n%s", err, r.Body)
	}
	for _, ci := range e.Calls() {
		if ci.Emergency != nil && ci.Emergency.TextOnly {
			return ci.ID, ans
		}
	}
	t.Fatal("the text session is not on the 911 screen")
	return "", nil
}

func textEngine(t *testing.T) (*Engine, *carrier, chan EmergencyAnswer) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].ESInet = true
	cfg.Trunks[0].Register = false
	cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
	cfg.InboundRoutes[0].DID = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	answers := make(chan EmergencyAnswer, 2)
	e, _ := startEngine(t, cfg, func(e *Engine) { e.OnEmergencyAnswer = func(a EmergencyAnswer) { answers <- a } })
	return e, c, answers
}

func conversationOf(e *Engine, id string) []TextLine {
	for _, c := range e.Calls() {
		if c.ID == id && c.Emergency != nil {
			return c.Emergency.Text
		}
	}
	return nil
}

func waitText(t *testing.T, e *Engine, id string, n int) []TextLine {
	t.Helper()
	for range 100 {
		if txt := conversationOf(e, id); len(txt) >= n {
			return txt
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("conversation %+v", conversationOf(e, id))
	return nil
}

// Text to 911 from a text control centre over MSRP: BitVoice answers the session, the texts wait on
// the 911 screen, a call-taker takes the conversation (CAD is told then) and replies, and the TCC
// closing the session ends the call.
func TestTextTo911(t *testing.T) {
	for _, tccActive := range []bool{true, false} {
		e, c, answers := textEngine(t)
		var tcc *msrp.Session
		got := make(chan string, 4)
		lines := "m=message 9 TCP/MSRP *\r\na=accept-types:text/plain message/cpim\r\n"
		var ln net.Listener
		if tccActive {
			lines += "a=path:msrp://127.0.0.1:9/tcc1;tcp\r\na=setup:active\r\n"
		} else {
			var err error
			if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			tccPath := "msrp://" + ln.Addr().String() + "/tcc1;tcp"
			lines = "m=message " + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port) + " TCP/MSRP *\r\na=accept-types:text/plain\r\na=path:" + tccPath + "\r\na=setup:passive\r\n"
		}
		id, ans := textSession(t, e, c, lines)
		path, _ := ans.Media[0].Attr("path")
		if tccActive {
			hp, err := msrp.HostPort(path)
			if err != nil {
				t.Fatalf("path %q: %v", path, err)
			}
			conn, err := net.Dial("tcp", hp)
			if err != nil {
				t.Fatal(err)
			}
			tcc = msrp.New("msrp://127.0.0.1:9/tcc1;tcp", path)
			go tcc.Serve(conn)
			tcc.Wait(time.Second)
			if err := tcc.Bind(); err != nil {
				t.Fatal(err)
			}
		} else {
			if s, _ := ans.Media[0].Attr("setup"); s != "active" {
				t.Fatalf("setup %q", s)
			}
			_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(15 * time.Second))
			conn, err := ln.Accept()
			if err != nil {
				t.Fatal("BitVoice did not connect to the passive TCC")
			}
			tcc = msrp.New("msrp://"+ln.Addr().String()+"/tcc1;tcp", path)
			go tcc.Serve(conn)
			tcc.Wait(time.Second)
		}
		tcc.OnMessage = func(m msrp.Message) {
			if s, ok := m.Text(); ok {
				got <- s
			}
		}
		if err := tcc.Send("there is someone in my house"); err != nil {
			t.Fatal(err)
		}
		txt := waitText(t, e, id, 1)
		if txt[0].Text != "there is someone in my house" || txt[0].Via != "msrp" || txt[0].Open {
			t.Fatalf("conversation %+v", txt)
		}
		if _, err := e.SendCallText(id, "", "Where are you?", "101", "Pat"); err == nil {
			t.Fatal("text was sent before the call was taken")
		}
		if err := e.TakeTextCall(id, "101", "Pat"); err != nil {
			t.Fatal(err)
		}
		select {
		case a := <-answers:
			if a.Agent != "101" || len(a.PIDF) == 0 {
				t.Fatalf("answer %+v", a)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("CAD was not told when the text was taken")
		}
		if err := e.TakeTextCall(id, "102", "Lee"); err == nil {
			t.Fatal("taken twice")
		}
		if _, err := e.SendCallText(id, "", "Where are you?", "101", "Pat"); err != nil {
			t.Fatal(err)
		}
		select {
		case s := <-got:
			if s != "Where are you?" {
				t.Fatalf("the TCC got %q", s)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the reply did not reach the TCC")
		}
		tcc.Close()
		for range 100 {
			if len(e.Calls()) == 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if n := len(e.Calls()); n != 0 {
			t.Fatalf("%d calls still up after the TCC closed the session", n)
		}
	}
}

// A text session on an ordinary carrier trunk is refused: text is taken only from the ESInet.
func TestTextSessionRefusedOffESInet(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].Register = false
	cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
	cfg.InboundRoutes[0].DID = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:2125550100@127.0.0.1"}
	inv.Set("From", "<sip:+17175550123@carrier.test>;tag=x")
	inv.Set("To", "<sip:2125550100@127.0.0.1>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:x@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = []byte("v=0\r\no=x 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=message 9 TCP/MSRP *\r\na=path:msrp://127.0.0.1:9/x;tcp\r\n")
	if r := finalOf(c.stack.Request(inv, pbx)); r == nil || r.StatusCode != 488 {
		t.Fatalf("got %v", r)
	}
}

// Text by SIP MESSAGE inside a 911 call's dialog (RFC 3428): taken as the caller's text, and the
// call-taker's reply goes back the same way.
func TestSIPMessageOn911(t *testing.T) {
	s := placeSOS(t, "")
	m := &sip.Message{IsRequest: true, Method: sip.MESSAGE, RequestURI: "sip:" + s.pbx.Addr.String()}
	m.Set("From", s.invite.Get("From"))
	m.Set("To", s.final.Get("To"))
	m.Set("Call-ID", s.invite.CallID())
	m.Set("CSeq", "2 MESSAGE")
	m.Set("Content-Type", "text/plain")
	m.Body = []byte("I am hiding in the closet")
	if r := finalOf(s.carrier.stack.Request(m, s.pbx)); r == nil || r.StatusCode != 200 {
		t.Fatalf("MESSAGE got %v", r)
	}
	txt := waitText(t, s.e, s.id, 1)
	if txt[0].Text != "I am hiding in the closet" || txt[0].Via != "sip" {
		t.Fatalf("conversation %+v", txt)
	}
	if _, err := s.e.SendCallText(s.id, "", "Stay where you are. Help is coming.", "101", "Pat"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-s.carrier.messages:
		if string(r.Body) != "Stay where you are. Help is coming." || r.CallID() != s.invite.CallID() {
			t.Fatalf("reply %s", r)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no reply MESSAGE")
	}
}
