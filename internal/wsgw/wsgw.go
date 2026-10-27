// Package wsgw lets WebRTC softphones (JsSIP, SIP.js and the apps built on them) register to BitVoice
// over SIP over WebSocket (RFC 7118). Each WebSocket connection gets its own loopback UDP socket to the
// call engine, so the engine sees an ordinary SIP phone; the gateway passes SIP through, and ends the
// browser's WebRTC media (ICE-lite, DTLS-SRTP) on its own endpoints, bridged to plain RTP for the engine.
package wsgw

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
	"github.com/biodream-llc/bitvoice/internal/ws"
)

// Media supplies endpoints (the engine's pool).
type Media interface {
	NewMediaEndpoint() (*media.Endpoint, error)
}

// Gateway serves the WebSocket and runs each connection.
type Gateway struct {
	EnginePort func() int // the engine's SIP port on loopback
	Media      Media
	// Enabled says whether softphones may connect now; Banned whether an address is banned.
	Enabled func() bool
	Banned  func(netip.Addr) bool
	// MediaHost is the address browsers reach for media, from the request (its host, or the public address).
	MediaHost func(r *http.Request) string
	// ICE gives BitVoice's part in ICE for a browser at client that reached it at host (nil: ICE-lite).
	ICE func(host string, client netip.Addr) webrtc.ICEOptions
	Log *slog.Logger
	// Linger is how long a closed connection's socket keeps answering the engine "not here" (480), so
	// calls do not wait for its registration to run out (default: until the registration would have).
	Linger time.Duration

	conns atomic.Int32
}

// ClientHeader carries the softphone's real address to the engine, which trusts it only from this computer.
const ClientHeader = "X-BitVoice-Client"

func (g *Gateway) iceFor(host string, client netip.Addr) webrtc.ICEOptions {
	if g.ICE == nil {
		return webrtc.ICEOptions{}
	}
	return g.ICE(host, client)
}

// Count is how many softphones are connected.
func (g *Gateway) Count() int { return int(g.conns.Load()) }

// ServeHTTP accepts a softphone's WebSocket (subprotocol sip).
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.Enabled != nil && !g.Enabled() {
		http.Error(w, "SIP over WebSocket is switched off in Settings", http.StatusNotFound)
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, _ := netip.ParseAddr(host)
	ip = ip.Unmap()
	if g.Banned != nil && g.Banned(ip) {
		http.Error(w, "banned", http.StatusForbidden)
		return
	}
	wc, err := ws.Accept(w, r, "sip")
	if err != nil {
		return
	}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		wc.Close()
		return
	}
	c := &conn{g: g, ws: wc, udp: pc, client: ip, host: g.MediaHost(r), legs: map[string]*leg{}, absorb: map[string]bool{},
		engine: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: g.EnginePort()}}
	g.conns.Add(1)
	go c.fromEngine()
	c.fromClient()
	g.conns.Add(-1)
}

type conn struct {
	g      *Gateway
	ws     *ws.Conn
	udp    *net.UDPConn
	engine *net.UDPAddr
	client netip.Addr
	host   string

	mu      sync.Mutex
	legs    map[string]*leg // by Call-ID
	absorb  map[string]bool // ACKs for re-INVITEs the gateway answered itself (Call-ID CSeq)
	gone    bool
	expires time.Time // when the last registration runs out
}

// leg is one call's media: the browser's WebRTC endpoint and the plain one the engine talks to.
type leg struct {
	browser, plain *media.Endpoint
	sess           *webrtc.Session
	answer         *sdp.Session // what the browser is told, when it offered
	engineOffered  bool
	accepted       bool
	contact        string // the softphone's Contact, for answers the gateway gives itself
	dir            string // the softphone's last direction
}

func (l *leg) close() {
	if l.sess != nil {
		l.sess.Close()
	}
	if l.browser != nil {
		l.browser.Close()
	}
	if l.plain != nil {
		l.plain.Close()
	}
}

func (c *conn) log() *slog.Logger {
	if c.g.Log != nil {
		return c.g.Log
	}
	return slog.Default()
}

func (c *conn) toEngine(m *sip.Message) {
	_, _ = c.udp.WriteToUDP(m.Bytes(), c.engine)
}

func (c *conn) toClient(m *sip.Message) {
	_ = c.ws.Write(ws.OpText, m.Bytes())
}

// fromClient reads the softphone's messages until the WebSocket closes.
func (c *conn) fromClient() {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	go func() {
		for range ping.C {
			if c.ws.Ping() != nil {
				return
			}
		}
	}()
	defer c.closed()
	for {
		_, b, err := c.ws.Read(3 * time.Minute)
		if err != nil {
			return
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			continue // a keep-alive
		}
		m, err := sip.Parse(b)
		if err != nil {
			c.log().Debug("websocket: malformed SIP from a softphone", "from", c.client, "err", err)
			continue
		}
		if m.IsRequest {
			c.clientRequest(m)
		} else {
			c.clientResponse(m)
		}
	}
}

// closed ends the softphone's calls and keeps its socket answering "not here" while its registration
// would still have run, so calls to it move on at once.
func (c *conn) closed() {
	c.mu.Lock()
	c.gone = true
	legs := c.legs
	c.legs = map[string]*leg{}
	until := c.expires
	c.mu.Unlock()
	for _, l := range legs {
		l.close()
	}
	linger := time.Until(until)
	if c.g.Linger > 0 {
		linger = min(linger, c.g.Linger)
	}
	if linger <= 0 {
		c.udp.Close()
		return
	}
	time.AfterFunc(min(linger, time.Hour), func() { c.udp.Close() })
}

const branchSuffix = ".ws"

// clientRequest passes a softphone's request to the engine: with the gateway's Via on top (answers
// come back here), its real address, and its WebRTC offer exchanged for a plain one.
func (c *conn) clientRequest(m *sip.Message) {
	m.Del("Route")
	m.Del(ClientHeader)
	m.Set(ClientHeader, c.client.String())
	branch := ""
	if v, err := sip.ParseVia(m.Get("Via")); err == nil {
		branch, _ = v.Params.Get("branch")
	}
	if branch == "" {
		branch = sip.NewBranch()
	}
	via := &sip.Via{Transport: "UDP", Host: "127.0.0.1", Port: c.udp.LocalAddr().(*net.UDPAddr).Port,
		Params: sip.Params{{Name: "branch", Value: branch + branchSuffix}, {Name: "rport", Flag: true}}}
	m.Prepend("Via", via.String())
	id := m.CallID()
	switch m.Method {
	case sip.INVITE:
		if len(m.Body) > 0 {
			if err := c.clientOffer(m); err != nil {
				r := sip.NewResponse(m, 488, "Not Acceptable Here")
				r.Del("Via")
				for _, v := range m.GetAll("Via")[1:] {
					r.Add("Via", v)
				}
				r.Set("Warning", `399 bitvoice "`+err.Error()+`"`)
				c.toClient(r)
				return
			}
		}
	case sip.ACK:
		if len(m.Body) > 0 { // an answer in the ACK, to a late-offer INVITE from the engine
			c.mu.Lock()
			l := c.legs[id]
			c.mu.Unlock()
			if l != nil {
				c.acceptAnswer(l, m)
			}
		}
	}
	c.toEngine(m)
}

// clientOffer handles a WebRTC offer from the softphone: a new call or a re-INVITE.
func (c *conn) clientOffer(m *sip.Message) error {
	offer, err := sdp.Parse(m.Body)
	if err != nil {
		return err
	}
	id := m.CallID()
	c.mu.Lock()
	l := c.legs[id]
	c.mu.Unlock()
	dir := "sendrecv"
	if a := offer.Audio(); a != nil {
		dir = offer.Direction(a)
	}
	if l != nil && l.sess != nil {
		// A re-INVITE (hold, resume): the same media; the engine hears the direction. With new ICE
		// credentials it is an ICE restart (the softphone's network changed): answered with new ones.
		l.dir = dir
		describe := func() (*sdp.Session, error) { return l.sess.Describe(c.host, inverse(dir)) }
		if l.sess.Restarting(offer) {
			describe = func() (*sdp.Session, error) { return l.sess.Restart(offer, c.host) }
		}
		ans, err := describe()
		if err != nil {
			return err
		}
		l.answer = ans
		m.Body = withDirection(plainSDP(l.plain, l.sess.PT), dir).Bytes()
		m.Set("Content-Type", "application/sdp")
		return nil
	}
	b, err := c.g.Media.NewMediaEndpoint()
	if err != nil {
		return errors.New("no media ports free")
	}
	sess, ans, err := webrtc.AnswerWith(b, offer, c.host, c.g.iceFor(c.host, c.client))
	if err != nil {
		b.Close()
		return err
	}
	p, err := c.g.Media.NewMediaEndpoint()
	if err != nil {
		sess.Close()
		b.Close()
		return errors.New("no media ports free")
	}
	l = &leg{browser: b, plain: p, sess: sess, answer: ans, contact: m.Get("Contact"), dir: dir}
	bridge(b, p, sess.EventPT)
	b.Start()
	p.Start()
	c.mu.Lock()
	c.legs[id] = l
	c.mu.Unlock()
	m.Body = plainSDP(p, sess.PT).Bytes()
	m.Set("Content-Type", "application/sdp")
	return nil
}

// clientResponse passes the softphone's answer to an engine request: its WebRTC answer exchanged for a
// plain one.
func (c *conn) clientResponse(m *sip.Message) {
	_, method := m.CSeq()
	id := m.CallID()
	c.mu.Lock()
	l := c.legs[id]
	c.mu.Unlock()
	if method == sip.INVITE && l != nil {
		if len(m.Body) > 0 && l.engineOffered {
			if !c.acceptAnswer(l, m) {
				return
			}
		}
		if m.StatusCode >= 300 {
			c.endLeg(id)
		}
	}
	if method == sip.BYE {
		c.endLeg(id)
	}
	c.toEngine(m)
}

// acceptAnswer takes the softphone's answer to the gateway's offer, and puts a plain answer in its place.
func (c *conn) acceptAnswer(l *leg, m *sip.Message) bool {
	if !l.accepted {
		ans, err := sdp.Parse(m.Body)
		if err == nil {
			err = l.sess.Accept(ans)
		}
		if err != nil {
			c.log().Info("websocket: a softphone's answer could not be used", "from", c.client, "err", err.Error())
			return false
		}
		l.accepted = true
		bridge(l.browser, l.plain, l.sess.EventPT)
		l.browser.Start()
		l.plain.Start()
	}
	m.Body = plainSDP(l.plain, l.sess.PT).Bytes()
	m.Set("Content-Type", "application/sdp")
	return true
}

func (c *conn) endLeg(id string) {
	c.mu.Lock()
	l := c.legs[id]
	delete(c.legs, id)
	c.mu.Unlock()
	if l != nil {
		l.close()
	}
}

// fromEngine reads what the engine sends this softphone.
func (c *conn) fromEngine() {
	buf := make([]byte, 65536)
	for {
		n, from, err := c.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if from.Port != c.engine.Port || !from.IP.IsLoopback() {
			continue
		}
		m, err := sip.Parse(append([]byte(nil), buf[:n]...))
		if err != nil {
			continue // keep-alives
		}
		c.mu.Lock()
		gone := c.gone
		c.mu.Unlock()
		if gone {
			if m.IsRequest && m.Method != sip.ACK {
				code := 481
				if m.Method == sip.INVITE || m.Method == sip.OPTIONS || m.Method == sip.MESSAGE || m.Method == sip.NOTIFY {
					code = 480
				}
				c.toEngine(sip.NewResponse(m, code, ""))
			}
			continue
		}
		if m.IsRequest {
			c.engineRequest(m)
		} else {
			c.engineResponse(m)
		}
	}
}

// engineResponse passes the engine's answer to the softphone, without the gateway's Via, with the
// WebRTC answer in place of the plain one.
func (c *conn) engineResponse(m *sip.Message) {
	vias := m.GetAll("Via")
	if len(vias) > 0 {
		if v, err := sip.ParseVia(vias[0]); err == nil && strings.HasSuffix(paramOf(v.Params, "branch"), branchSuffix) {
			m.Del("Via")
			for _, x := range vias[1:] {
				m.Add("Via", x)
			}
		}
	}
	_, method := m.CSeq()
	if m.StatusCode == 200 && method == sip.REGISTER {
		c.noteExpiry(m)
	}
	if method == sip.INVITE {
		id := m.CallID()
		c.mu.Lock()
		l := c.legs[id]
		c.mu.Unlock()
		if l != nil && !l.engineOffered {
			if len(m.Body) > 0 {
				if s, err := sdp.Parse(m.Body); err == nil && s.Audio() != nil {
					l.plain.SetRemote(s.Connection(s.Audio()), s.Audio().Port)
				}
				m.Body = l.answer.Bytes()
				m.Set("Content-Type", "application/sdp")
			}
			if m.StatusCode >= 300 {
				c.endLeg(id)
			}
		}
	}
	c.toClient(m)
}

// noteExpiry keeps when the softphone's registration runs out.
func (c *conn) noteExpiry(m *sip.Message) {
	secs := 0
	if v, ok := sip.ParseExpires(m.Get("Expires")); ok {
		secs = v
	}
	for _, ct := range m.GetAll("Contact") {
		if i := strings.Index(ct, "expires="); i >= 0 {
			if n, err := strconv.Atoi(strings.TrimRight(strings.SplitN(ct[i+8:], ";", 2)[0], " >")); err == nil && n > secs {
				secs = n
			}
		}
	}
	c.mu.Lock()
	c.expires = time.Now().Add(time.Duration(secs) * time.Second)
	c.mu.Unlock()
}

// engineRequest passes the engine's request to the softphone: a new call gets a WebRTC offer; a
// re-INVITE is answered here (the media is the gateway's, so the softphone need not know).
func (c *conn) engineRequest(m *sip.Message) {
	id := m.CallID()
	switch m.Method {
	case sip.ACK:
		seq, _ := m.CSeq()
		key := id + " " + strconv.FormatUint(uint64(seq), 10)
		c.mu.Lock()
		skip := c.absorb[key]
		delete(c.absorb, key)
		c.mu.Unlock()
		if skip {
			return
		}
	case sip.INVITE:
		to, _ := sip.ParseAddress(m.Get("To"))
		c.mu.Lock()
		l := c.legs[id]
		c.mu.Unlock()
		if to != nil && to.Tag() != "" && l != nil {
			c.answerReinvite(l, m)
			return
		}
		if len(m.Body) > 0 && l == nil {
			if err := c.engineOffer(m); err != nil {
				c.toEngine(sip.NewResponse(m, 488, err.Error()))
				return
			}
		}
	case sip.BYE:
		defer c.endLeg(id)
	}
	c.toClient(m)
}

// engineOffer puts a WebRTC offer, with the codecs the engine offered, in place of its plain one.
func (c *conn) engineOffer(m *sip.Message) error {
	offer, err := sdp.Parse(m.Body)
	if err != nil || offer.Audio() == nil {
		return errors.New("no audio offered")
	}
	var pts []int
	for _, cd := range offer.Audio().Codecs() {
		switch {
		case strings.EqualFold(cd.Name, "opus"):
			pts = append(pts, media.PTOpus)
		case strings.EqualFold(cd.Name, "PCMU"):
			pts = append(pts, 0)
		case strings.EqualFold(cd.Name, "PCMA"):
			pts = append(pts, 8)
		}
	}
	if len(pts) == 0 {
		return errors.New("no codec a browser takes was offered")
	}
	b, err := c.g.Media.NewMediaEndpoint()
	if err != nil {
		return errors.New("no media ports free")
	}
	sess, off, err := webrtc.OfferWith(b, c.host, pts, c.g.iceFor(c.host, c.client))
	if err != nil {
		b.Close()
		return err
	}
	p, err := c.g.Media.NewMediaEndpoint()
	if err != nil {
		sess.Close()
		b.Close()
		return errors.New("no media ports free")
	}
	p.SetRemote(offer.Connection(offer.Audio()), offer.Audio().Port)
	l := &leg{browser: b, plain: p, sess: sess, engineOffered: true, dir: "sendrecv"}
	c.mu.Lock()
	c.legs[m.CallID()] = l
	c.mu.Unlock()
	m.Body = off.Bytes()
	m.Set("Content-Type", "application/sdp")
	return nil
}

// answerReinvite answers the engine's re-INVITE itself, following its new address, and keeps its ACK
// from the softphone.
func (c *conn) answerReinvite(l *leg, m *sip.Message) {
	if len(m.Body) > 0 {
		if s, err := sdp.Parse(m.Body); err == nil && s.Audio() != nil {
			if p := s.Audio().Port; p > 0 {
				l.plain.SetRemote(s.Connection(s.Audio()), p)
			}
		}
	}
	r := sip.NewResponse(m, 200, "")
	if l.contact != "" {
		r.Set("Contact", l.contact)
	} else {
		r.Set("Contact", "<sip:ws@127.0.0.1:"+strconv.Itoa(c.udp.LocalAddr().(*net.UDPAddr).Port)+">")
	}
	pt := 0
	if l.sess != nil && l.sess.PT >= 0 {
		pt = l.sess.PT
	}
	r.Set("Content-Type", "application/sdp")
	r.Body = withDirection(plainSDP(l.plain, pt), l.dir).Bytes()
	seq, _ := m.CSeq()
	c.mu.Lock()
	c.absorb[m.CallID()+" "+strconv.FormatUint(uint64(seq), 10)] = true
	c.mu.Unlock()
	c.toEngine(r)
}

// plainSDP is the engine's side of a call's media: the browser's codec and telephone-event 101.
func plainSDP(ep *media.Endpoint, pt int) *sdp.Session {
	cd := sdp.Codec{PT: 0, Name: "PCMU", ClockRate: 8000}
	switch pt {
	case 8:
		cd = sdp.Codec{PT: 8, Name: "PCMA", ClockRate: 8000}
	case media.PTOpus:
		cd = sdp.Codec{PT: pt, Name: "opus", ClockRate: 48000, Channels: 2}
	}
	return sdp.New("127.0.0.1", ep.Port(), uint64(time.Now().UnixNano()/1000), []sdp.Codec{cd}, 101)
}

func withDirection(s *sdp.Session, dir string) *sdp.Session {
	if dir == "" || dir == "sendrecv" {
		return s
	}
	a := s.Audio()
	for i, l := range a.Lines {
		if l.Type == 'a' && (l.Value == "sendrecv" || l.Value == "sendonly" || l.Value == "recvonly" || l.Value == "inactive") {
			a.Lines[i].Value = dir
			return s
		}
	}
	a.Lines = append(a.Lines, sdp.Line{Type: 'a', Value: dir})
	return s
}

func inverse(dir string) string {
	switch dir {
	case "sendonly":
		return "recvonly"
	case "recvonly":
		return "sendonly"
	case "inactive":
		return "inactive"
	}
	return "sendrecv"
}

// bridge joins the browser's endpoint and the plain one, renumbering telephone-event to 101.
func bridge(browser, plain *media.Endpoint, browserEventPT int) {
	if browserEventPT >= 0 && browserEventPT != 101 {
		plain.MapPayloadType(browserEventPT, 101)
		browser.MapPayloadType(101, browserEventPT)
	}
	media.Connect(browser, plain)
}

// Run keeps nothing running itself; it exists so the gateway's lifetime reads like the others'.
func (g *Gateway) Run(ctx context.Context) { <-ctx.Done() }

func paramOf(p sip.Params, name string) string {
	v, _ := p.Get(name)
	return v
}
