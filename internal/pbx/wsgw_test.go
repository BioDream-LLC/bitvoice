package pbx

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/wsgw"
)

// wsPhone is a softphone over SIP over WebSocket, as JsSIP is: masked frames, subprotocol sip.
type wsPhone struct {
	t    *testing.T
	c    net.Conn
	r    *bufio.Reader
	user string
	in   chan *sip.Message
}

func dialWS(t *testing.T, url, user string) *wsPhone {
	t.Helper()
	host := strings.TrimPrefix(url, "http://")
	c, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	keyb := make([]byte, 16)
	rand.Read(keyb)
	key := base64.StdEncoding.EncodeToString(keyb)
	io.WriteString(c, "GET /ws HTTP/1.1\r\nHost: "+host+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "+key+
		"\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: sip\r\n\r\n")
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) || resp.Header.Get("Sec-WebSocket-Protocol") != "sip" {
		t.Fatalf("handshake headers %v", resp.Header)
	}
	p := &wsPhone{t: t, c: c, r: r, user: user, in: make(chan *sip.Message, 32)}
	go p.read()
	t.Cleanup(func() { c.Close() })
	return p
}

func (p *wsPhone) read() {
	for {
		var h [2]byte
		if _, err := io.ReadFull(p.r, h[:]); err != nil {
			close(p.in)
			return
		}
		n := int(h[1] & 0x7F)
		if n == 126 {
			var e [2]byte
			io.ReadFull(p.r, e[:])
			n = int(binary.BigEndian.Uint16(e[:]))
		}
		b := make([]byte, n)
		io.ReadFull(p.r, b)
		if h[0]&0x0F != 1 {
			continue
		}
		if m, err := sip.Parse(b); err == nil {
			p.in <- m
		}
	}
}

func (p *wsPhone) send(m *sip.Message) {
	b := m.Bytes()
	h := []byte{0x81}
	if len(b) < 126 {
		h = append(h, 0x80|byte(len(b)))
	} else {
		h = append(h, 0x80|126)
		h = binary.BigEndian.AppendUint16(h, uint16(len(b)))
	}
	mask := []byte{1, 2, 3, 4}
	h = append(h, mask...)
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ mask[i%4]
	}
	if _, err := p.c.Write(append(h, out...)); err != nil {
		p.t.Fatal(err)
	}
}

func (p *wsPhone) next(timeout time.Duration, want func(*sip.Message) bool) *sip.Message {
	p.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-p.in:
			if !ok {
				p.t.Fatal("websocket closed")
			}
			if want(m) {
				return m
			}
		case <-deadline:
			p.t.Fatal("timed out waiting for a message")
		}
	}
}

func (p *wsPhone) request(method, ruri, callID string, cseq int, extra func(*sip.Message)) *sip.Message {
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: ruri}
	m.Set("Via", "SIP/2.0/WSS df7jal23ls0d.invalid;branch="+sip.NewBranch())
	m.Set("Max-Forwards", "69")
	m.Set("From", "<sip:"+p.user+"@127.0.0.1>;tag=ws"+p.user)
	m.Set("To", "<sip:"+p.user+"@127.0.0.1>")
	m.Set("Call-ID", callID)
	m.Set("CSeq", strconv.Itoa(cseq)+" "+method)
	m.Set("Contact", "<sip:r8k3@df7jal23ls0d.invalid;transport=ws>")
	if extra != nil {
		extra(m)
	}
	return m
}

// A browser-style WebRTC offer: ICE credentials, a DTLS fingerprint, SAVPF, Opus and PCMU.
const browserOffer = "v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\n" +
	"m=audio 9 UDP/TLS/RTP/SAVPF 111 0 126\r\nc=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\na=ice-ufrag:abcd\r\na=ice-pwd:abcdefghijklmnopqrstuvwx\r\n" +
	"a=fingerprint:sha-256 4A:AD:B9:B1:3F:82:18:3B:54:02:12:DF:3E:5D:49:6B:19:E5:7C:AB:4E:87:D6:F7:E3:4D:16:F8:32:49:7E:6F\r\n" +
	"a=setup:actpass\r\na=mid:0\r\na=sendrecv\r\na=rtcp-mux\r\na=rtpmap:111 opus/48000/2\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:126 telephone-event/8000\r\n"

func browserAnswerTo(offer string) string {
	return strings.NewReplacer("a=setup:actpass", "a=setup:active", "m=audio 9 UDP/TLS/RTP/SAVPF 111 0 126", "m=audio 9 UDP/TLS/RTP/SAVPF 0 126").Replace(browserOffer)
}

// A softphone registers over the WebSocket (digest through the gateway), is called by a desk phone
// (the gateway offers it WebRTC, takes its WebRTC answer and gives the engine plain RTP), and calls
// the desk phone itself (its WebRTC offer reaches the desk phone as plain RTP).
func TestSIPOverWebSocket(t *testing.T) {
	cfg := testConfig()
	e, _ := startEngine(t, cfg)
	g := &wsgw.Gateway{Media: e, EnginePort: e.SIPPort, MediaHost: func(*http.Request) string { return "127.0.0.1" }}
	srv := httptest.NewServer(g)
	defer srv.Close()
	desk := registered(t, e, "101")[0]
	sp := dialWS(t, srv.URL, "100")

	// REGISTER, challenged then accepted.
	reg := sp.request(sip.REGISTER, "sip:127.0.0.1", "reg-"+sip.NewTag(), 1, func(m *sip.Message) { m.Set("Expires", "600") })
	sp.send(reg)
	ch := sp.next(3*time.Second, func(m *sip.Message) bool { return !m.IsRequest })
	if ch.StatusCode != 401 || !strings.HasPrefix(ch.Get("Via"), "SIP/2.0/WSS df7jal23ls0d.invalid") {
		t.Fatalf("challenge %d, Via %s", ch.StatusCode, ch.Get("Via"))
	}
	c, _ := sip.ParseChallenge(ch.Get("WWW-Authenticate"))
	cred, _ := sip.Answer(c, "100", testSecret, sip.REGISTER, "sip:127.0.0.1")
	reg2 := sp.request(sip.REGISTER, "sip:127.0.0.1", reg.CallID(), 2, func(m *sip.Message) {
		m.Set("Expires", "600")
		m.Set("Authorization", cred.String())
	})
	sp.send(reg2)
	if ok := sp.next(3*time.Second, func(m *sip.Message) bool { return !m.IsRequest }); ok.StatusCode != 200 {
		t.Fatalf("register %d", ok.StatusCode)
	}
	if g.Count() != 1 {
		t.Fatalf("connections %d", g.Count())
	}

	// The desk phone calls the softphone.
	d, codeCh := (*dialog)(nil), make(chan int, 1)
	go func() {
		var code int
		d, code = desk.dial("100")
		codeCh <- code
	}()
	inv := sp.next(5*time.Second, func(m *sip.Message) bool { return m.IsRequest && m.Method == sip.INVITE })
	body := string(inv.Body)
	for _, want := range []string{"UDP/TLS/RTP/SAVPF", "a=ice-lite", "a=setup:actpass", "a=fingerprint:sha-256", "PCMU/8000"} {
		if !strings.Contains(body, want) {
			t.Fatalf("offer to the softphone lacks %q:\n%s", want, body)
		}
	}
	ok := sip.NewResponse(inv, 200, "")
	ok.Set("To", inv.Get("To")+";tag=sp100")
	ok.Set("Contact", "<sip:r8k3@df7jal23ls0d.invalid;transport=ws>")
	ok.Set("Content-Type", "application/sdp")
	ok.Body = []byte(browserAnswerTo(body))
	sp.send(ok)
	if code := <-codeCh; code != 200 {
		t.Fatalf("desk phone's call answered %d", code)
	}
	desk.send(d.request(sip.BYE))
	sp.next(5*time.Second, func(m *sip.Message) bool { return m.IsRequest && m.Method == sip.BYE })

	// The softphone calls the desk phone.
	call := sp.request(sip.INVITE, "sip:101@127.0.0.1", "call-"+sip.NewTag(), 1, func(m *sip.Message) {
		m.Set("To", "<sip:101@127.0.0.1>")
		m.Set("Content-Type", "application/sdp")
		m.Body = []byte(browserOffer)
	})
	sp.send(call)
	ch = sp.next(3*time.Second, func(m *sip.Message) bool { return !m.IsRequest && m.StatusCode == 407 })
	c, _ = sip.ParseChallenge(ch.Get("Proxy-Authenticate"))
	cred, _ = sip.Answer(c, "100", testSecret, sip.INVITE, "sip:101@127.0.0.1")
	ack := sp.request(sip.ACK, "sip:101@127.0.0.1", call.CallID(), 1, func(m *sip.Message) { m.Set("To", ch.Get("To")); m.Set("Via", call.Get("Via")) })
	sp.send(ack)
	call2 := sp.request(sip.INVITE, "sip:101@127.0.0.1", call.CallID(), 2, func(m *sip.Message) {
		m.Set("To", "<sip:101@127.0.0.1>")
		m.Set("Proxy-Authorization", cred.String())
		m.Set("Content-Type", "application/sdp")
		m.Body = []byte(browserOffer)
	})
	sp.send(call2)
	ring := desk.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("the softphone's call did not ring the desk phone")
	}
	if b := string(ring.Req.Body); !strings.Contains(b, "RTP/AVP") || strings.Contains(b, "fingerprint") {
		t.Fatalf("the desk phone was offered WebRTC:\n%s", b)
	}
	desk.answer(ring)
	ans := sp.next(5*time.Second, func(m *sip.Message) bool { return !m.IsRequest && m.StatusCode == 200 })
	if b := string(ans.Body); !strings.Contains(b, "a=ice-lite") || !strings.Contains(b, "a=setup:passive") || !strings.Contains(b, "UDP/TLS/RTP/SAVPF") {
		t.Fatalf("the softphone's answer is not WebRTC:\n%s", b)
	}
	if v := ans.GetAll("Via"); len(v) != 1 || !strings.Contains(v[0], ".invalid") {
		t.Fatalf("the gateway's Via leaked to the softphone: %v", v)
	}
}

// The engine counts bans and limits by a gateway's softphone, never by this computer.
func TestClientIPFromGateway(t *testing.T) {
	m := &sip.Message{IsRequest: true, Method: sip.REGISTER}
	m.Set(wsgw.ClientHeader, "203.0.113.7")
	in := &sip.Incoming{Msg: m, From: sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:5000")}}
	if got := clientIP(in); got.String() != "203.0.113.7" {
		t.Fatalf("from loopback: %s", got)
	}
	in.From.Addr = netip.MustParseAddrPort("198.51.100.2:5060")
	if got := clientIP(in); got.String() != "198.51.100.2" {
		t.Fatalf("from outside the header is not trusted: %s", got)
	}
}
