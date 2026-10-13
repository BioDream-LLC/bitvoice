package pbx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// phone is a minimal SIP phone for tests: it registers, places and answers calls, and sends and receives
// RTP on a real socket, so tests exercise the engine exactly as a desk phone would.
type phone struct {
	domain string            // the SIP domain the phone uses; 127.0.0.1 when empty
	extra  map[string]string // headers put on every INVITE this phone sends
	t      *testing.T
	stack  *sip.Stack
	user   string
	secret string
	pbx    sip.Peer
	rtp    *net.UDPConn

	incoming chan *sip.ServerTx
	byes     chan string
	notifies chan string
	messages chan string
	reinvite chan *sip.Message
	// srtpKey, when set, makes the phone offer SRTP with this 30-byte master key.
	srtpKey []byte
	// codecs, when set, replaces the phone's codec list (PCMU only by default).
	codecs []sdp.Codec
	// video, when set, makes the phone offer and accept H.264 video at this socket, as payload type
	// videoPT (96 by default).
	video   *net.UDPConn
	videoPT int
	vmu     sync.Mutex // video may change during a call
	// t38, when set, makes the phone take T.38 fax at this socket when offered it.
	t38 *net.UDPConn
}

// t38Offer is a fax gateway's switch to T.38 at a socket.
func t38Offer(c *net.UDPConn) []byte {
	port := c.LocalAddr().(*net.UDPAddr).Port
	return []byte("v=0\r\no=gw 1 2 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=image " + strconv.Itoa(port) + " udptl t38\r\na=T38FaxVersion:0\r\na=T38MaxBitRate:14400\r\n" +
		"a=T38FaxRateManagement:transferredTCF\r\na=T38FaxUdpEC:t38UDPRedundancy\r\n")
}

func (p *phone) setVideo(c *net.UDPConn) {
	p.vmu.Lock()
	p.video = c
	p.vmu.Unlock()
}

func newPhone(t *testing.T, pbxPort int, user, secret string) *phone {
	t.Helper()
	p := &phone{t: t, user: user, secret: secret, incoming: make(chan *sip.ServerTx, 8), byes: make(chan string, 8),
		notifies: make(chan string, 8), messages: make(chan string, 8), reinvite: make(chan *sip.Message, 8),
		pbx: sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(pbxPort))}}
	p.stack = sip.NewStack(p.handle)
	p.stack.Host = "127.0.0.1"
	p.stack.T1, p.stack.T2 = 50*time.Millisecond, 200*time.Millisecond
	p.stack.TxExpire = 1500 * time.Millisecond
	if err := p.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.stack.Serve(ctx)
	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	p.rtp = rtp
	t.Cleanup(func() { cancel(); p.stack.Close(); rtp.Close() })
	return p
}

func (p *phone) handle(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	if tx == nil {
		return // ACK
	}
	to, _ := sip.ParseAddress(m.Get("To"))
	switch m.Method {
	case sip.INVITE:
		if to.Tag() != "" {
			p.reinvite <- m
			if strings.Contains(string(m.Body), "m=image") && p.t38 == nil {
				tx.Respond(sip.NewResponse(m, 488, ""))
				return
			}
			r := sip.NewResponse(m, 200, "")
			r.Set("Content-Type", "application/sdp")
			r.Body = p.offer("sendrecv")
			if strings.Contains(string(m.Body), "m=image") {
				r.Body = t38Offer(p.t38)
			}
			tx.Respond(r)
			return
		}
		r := sip.NewResponse(m, 180, "")
		r.Set("To", m.Get("To")+";tag="+p.user+"tag")
		tx.Respond(r)
		p.incoming <- tx
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if inv := p.stack.FindInvite(m); inv != nil {
			r := sip.NewResponse(inv.Req, 487, "")
			r.Set("To", inv.Req.Get("To")+";tag="+p.user+"tag")
			inv.Respond(r)
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		p.byes <- m.CallID()
	case sip.NOTIFY:
		tx.Respond(sip.NewResponse(m, 200, ""))
		p.notifies <- string(m.Body)
	case sip.MESSAGE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		p.messages <- string(m.Body)
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

func (p *phone) rtpPort() int { return p.rtp.LocalAddr().(*net.UDPAddr).Port }

func (p *phone) offer(dir string) []byte {
	codecs := p.codecs
	if codecs == nil {
		codecs = []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}
	}
	s := sdp.New("127.0.0.1", p.rtpPort(), 1, codecs, 101)
	setDirection(s.Audio(), dir)
	if p.srtpKey != nil {
		s.Audio().Proto = "RTP/SAVP"
		s.Audio().SetCrypto([]sdp.Crypto{{Tag: 1, Suite: "AES_CM_128_HMAC_SHA1_80", Key: p.srtpKey}})
	}
	p.vmu.Lock()
	video := p.video
	p.vmu.Unlock()
	if video != nil {
		pt := p.videoPT
		if pt == 0 {
			pt = 96
		}
		n := strconv.Itoa(pt)
		s.Media = append(s.Media, &sdp.Media{Type: "video", Port: video.LocalAddr().(*net.UDPAddr).Port, Proto: "RTP/AVP", Formats: []string{n},
			Lines: []sdp.Line{{Type: 'a', Value: "rtpmap:" + n + " H264/90000"}, {Type: 'a', Value: "fmtp:" + n + " profile-level-id=42e01f"}, {Type: 'a', Value: "sendrecv"}}})
	}
	return s.Bytes()
}

func (p *phone) contact() string {
	return fmt.Sprintf("<sip:%s@127.0.0.1:%d>", p.user, p.stack.Port())
}

// send runs a client transaction, answering one digest challenge, and returns the final response.
func (p *phone) send(m *sip.Message) *sip.Message {
	resp := finalOf(p.stack.Request(m.Clone(), p.pbx))
	if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 407) && p.secret != "" {
		h, hdr := resp.Get("WWW-Authenticate"), "Authorization"
		if resp.StatusCode == 407 {
			h, hdr = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
		}
		ch, err := sip.ParseChallenge(h)
		if err != nil {
			p.t.Fatal(err)
		}
		cr, _ := sip.Answer(ch, p.user, p.secret, m.Method, m.RequestURI)
		next := m.Clone()
		n, method := m.CSeq()
		next.Set("CSeq", strconv.FormatUint(uint64(n+1), 10)+" "+method)
		next.Set(hdr, cr.String())
		resp = finalOf(p.stack.Request(next, p.pbx))
	}
	return resp
}

func (p *phone) host() string {
	if p.domain != "" {
		return p.domain
	}
	return "127.0.0.1"
}

func (p *phone) register() int {
	m := &sip.Message{IsRequest: true, Method: sip.REGISTER, RequestURI: "sip:" + p.host()}
	m.Set("From", "<sip:"+p.user+"@"+p.host()+">;tag="+sip.NewTag())
	m.Set("To", "<sip:"+p.user+"@"+p.host()+">")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 REGISTER")
	m.Set("Contact", p.contact())
	m.Set("Expires", "300")
	r := p.send(m)
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// dialog is the phone's view of an established call.
type dialog struct {
	p                   *phone
	callID              string
	localURI, remoteURI string
	target              string
	seq                 uint32
	media               netip.AddrPort // where to send RTP
	body                []byte         // the other side's description
}

func (d *dialog) request(method string) *sip.Message {
	d.seq++
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: d.target}
	m.Set("From", d.localURI)
	m.Set("To", d.remoteURI)
	m.Set("Call-ID", d.callID)
	m.Set("CSeq", strconv.FormatUint(uint64(d.seq), 10)+" "+method)
	m.Set("Contact", d.p.contact())
	return m
}

func mediaOf(body []byte) netip.AddrPort {
	s, err := sdp.Parse(body)
	if err != nil || s.Audio() == nil {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(netip.MustParseAddr(s.Connection(s.Audio())), uint16(s.Audio().Port))
}

// dial calls a number and returns the dialog when answered, or the final status.
func (p *phone) dial(number string) (*dialog, int) {
	tag := sip.NewTag()
	m := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + number + "@" + p.host()}
	from := "<sip:" + p.user + "@" + p.host() + ">;tag=" + tag
	m.Set("From", from)
	m.Set("To", "<sip:"+number+"@"+p.host()+">")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 INVITE")
	m.Set("Contact", p.contact())
	m.Set("Content-Type", "application/sdp")
	for k, v := range p.extra {
		m.Set(k, v)
	}
	m.Body = p.offer("sendrecv")
	r := p.send(m)
	if r == nil {
		return nil, 0
	}
	if r.StatusCode >= 300 {
		return nil, r.StatusCode
	}
	ct, _ := sip.ParseAddress(r.Get("Contact"))
	n, _ := r.CSeq()
	d := &dialog{p: p, callID: m.CallID(), localURI: from, remoteURI: r.Get("To"), target: ct.URI.String(), seq: n,
		media: mediaOf(r.Body), body: r.Body}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: d.target}
	ack.Set("From", d.localURI)
	ack.Set("To", d.remoteURI)
	ack.Set("Call-ID", d.callID)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = p.stack.Stateless(ack, p.pbx)
	return d, r.StatusCode
}

// answer answers a ringing INVITE and returns the dialog.
func (p *phone) answer(tx *sip.ServerTx) *dialog {
	r := sip.NewResponse(tx.Req, 200, "")
	to := tx.Req.Get("To") + ";tag=" + p.user + "tag"
	r.Set("To", to)
	r.Set("Contact", p.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = p.offer("sendrecv")
	tx.Respond(r)
	ct, _ := sip.ParseAddress(tx.Req.Get("Contact"))
	return &dialog{p: p, callID: tx.Req.CallID(), localURI: to, remoteURI: tx.Req.Get("From"), target: ct.URI.String(),
		seq: 100, media: mediaOf(tx.Req.Body)}
}

func (p *phone) waitRing(timeout time.Duration) *sip.ServerTx {
	select {
	case tx := <-p.incoming:
		return tx
	case <-time.After(timeout):
		return nil
	}
}

// sendRTP sends a few packets to the dialog's media address.
func (d *dialog) sendRTP(n int) {
	for i := 0; i < n; i++ {
		pkt := make([]byte, 172)
		pkt[0] = 0x80
		pkt[3] = byte(i)
		pkt[7] = byte(i)
		_, _ = d.p.rtp.WriteToUDPAddrPort(pkt, d.media)
		time.Sleep(5 * time.Millisecond)
	}
}

// gotRTP reports whether RTP arrives within the timeout.
func (p *phone) gotRTP(timeout time.Duration) bool {
	_ = p.rtp.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 2048)
	for {
		n, _, err := p.rtp.ReadFromUDP(buf)
		if err != nil {
			return false
		}
		if n >= 12 && buf[0]>>6 == 2 {
			return true
		}
	}
}

// drainRTP discards queued packets so the next gotRTP sees only new media.
func (p *phone) drainRTP() {
	_ = p.rtp.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	buf := make([]byte, 2048)
	for {
		if _, _, err := p.rtp.ReadFromUDP(buf); err != nil {
			return
		}
	}
}

var _ = sync.Mutex{}
