package pbx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/msrp"
	"github.com/biodream-llc/bitvoice/internal/rtt"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/srtp"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/tty"
)

// A 911 call can carry text as well as voice: a TTY on the voice line (deaf and hard-of-hearing
// callers' text telephones), real-time text beside the voice (RFC 4103), or a text-to-911 conversation
// from a text control centre (MSRP or SIP MESSAGE). All of them are one conversation on the call, shown
// to the call-taker beside the location and answered from the same box.

// textQuiet is how long a pause ends the caller's line: TTY and real-time text arrive a character at a
// time with no end-of-message mark (a TTY user types GA, "go ahead", when done).
const textQuiet = 4 * time.Second

// startText listens for a TTY on a 911 call's voice, from the moment it arrives. Called once the
// caller's media has started.
func (c *Call) startText() {
	c.mu.Lock()
	emergency := c.i3 != nil || c.esinet
	c.mu.Unlock()
	if !emergency || c.e.Config().System.PSAP.TTYOff || c.caller == nil {
		return
	}
	d := tty.NewDecoder()
	var dec media.Decoders
	stop := c.caller.ep.Listen(func(pkt []byte) {
		if len(pkt) < 12 || pkt[0]&0x30 != 0 || pkt[0]&0x0f != 0 {
			return
		}
		pt := int(pkt[1] & 0x7f)
		if !media.Transcodable(pt) {
			return
		}
		pcm := dec.Narrowband(pt, pkt[12:])
		if s := d.Feed(pcm); s != "" {
			c.textHeard("tty", s)
		}
	})
	go func() {
		<-c.cancelled
		stop()
	}()
}

// textHeard adds characters from the caller to the conversation: to the line being typed, or a new one
// after a pause or a new line. A backspace takes the last character back.
func (c *Call) textHeard(via, s string) {
	now := time.Now()
	c.mu.Lock()
	first := !c.textOn[via]
	if c.textOn == nil {
		c.textOn = map[string]bool{}
	}
	c.textOn[via] = true
	if n := len(c.text); n > 0 && c.text[n-1].Open && now.Sub(c.textAt) > textQuiet {
		c.text[n-1].Open = false // a pause ended the caller's line
	}
	for _, r := range s {
		n := len(c.text)
		open := n > 0 && c.text[n-1].Open && c.text[n-1].From == "caller" && c.text[n-1].Via == via
		switch r {
		case '\n', '\r':
			if open {
				c.text[n-1].Open = false
			}
			continue
		case '\b':
			if open {
				t := c.text[n-1].Text
				_, size := utf8.DecodeLastRuneInString(t)
				c.text[n-1].Text = t[:len(t)-size]
			}
			continue
		}
		if !open {
			if r == ' ' {
				continue
			}
			c.text = append(c.text, TextLine{At: now, From: "caller", Via: via, Open: true})
			n++
		}
		if len(c.text[n-1].Text) < maxTextLine {
			c.text[n-1].Text += string(r)
		}
	}
	c.textAt = now
	c.mu.Unlock()
	if first {
		c.e.Log.Info("911 call carries text", "call", c.ID, "via", via)
	}
	c.e.publish("call.updated", c.info())
}

// maxTextLine bounds one line of a conversation.
const maxTextLine = 4000

// TextLine is one turn of a 911 call's text conversation.
type TextLine = store.TextLine

// SendCallText sends the call-taker's text to a 911 caller, the way the caller's text came (or by TTY when
// via is "tty"), and adds it to the conversation.
func (e *Engine) SendCallText(id, via, text, ext, name string) (TextLine, error) {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return TextLine{}, errors.New("no such call")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return TextLine{}, errors.New("there is no text to send")
	}
	if len(text) > maxTextLine {
		return TextLine{}, errors.New("the text is too long")
	}
	c.mu.Lock()
	if via == "" {
		via = c.textVia()
	}
	ended, taken := c.ended, c.takenBy != ""
	c.mu.Unlock()
	if ended {
		return TextLine{}, errors.New("the call has ended")
	}
	if !taken {
		return TextLine{}, errors.New("take the call before sending text")
	}
	var err error
	switch via {
	case "tty":
		text = tty.Sendable(text)
		if strings.TrimSpace(text) == "" {
			return TextLine{}, errors.New("a TTY has capital letters, digits and some punctuation only: nothing here can be sent")
		}
		err = c.sendTTY(text)
	default:
		if f := c.textSender(via); f != nil {
			err = f(text)
		} else {
			err = errors.New("this call has no text connection of that kind")
		}
	}
	if err != nil {
		return TextLine{}, err
	}
	l := TextLine{At: time.Now(), From: "taker", Name: firstNonEmptyStr(name, ext), Via: via, Text: text}
	c.mu.Lock()
	if n := len(c.text); n > 0 && c.text[n-1].Open {
		c.text[n-1].Open = false // the caller's line ends when the call-taker answers it
	}
	c.text = append(c.text, l)
	c.mu.Unlock()
	e.publish("call.updated", c.info())
	return l, nil
}

// textVia is how text reaches this caller: the session's own text channel when there is one, else TTY.
// Called with c.mu held.
func (c *Call) textVia() string {
	if c.caller != nil && c.caller.msrpOn.Load() {
		return "msrp"
	}
	if c.textOn["sip"] {
		return "sip"
	}
	if c.caller != nil && c.caller.rttOn.Load() {
		return "rtt"
	}
	return "tty"
}

// textSender returns how to send text by via on this call, nil when the call has no such channel.
func (c *Call) textSender(via string) func(string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if via == "msrp" && c.caller != nil {
		l := c.caller
		l.mu.Lock()
		ss := l.msrp
		l.mu.Unlock()
		if ss == nil {
			return nil
		}
		return ss.Send
	}
	if via == "rtt" && c.caller != nil {
		l := c.caller
		l.mu.Lock()
		out := l.textOut
		l.mu.Unlock()
		if out == nil {
			return nil
		}
		return func(text string) error {
			out.Send(strings.ReplaceAll(text, "\n", string(rtt.LineSep)) + string(rtt.LineSep))
			return nil
		}
	}
	return c.senders[via]
}

// sendTTY plays text as TTY tones to the caller. The call-taker's side is muted meanwhile (TTYs are
// half duplex), and the call-taker hears the tones go.
func (c *Call) sendTTY(text string) error {
	c.mu.Lock()
	a, l := c.caller, c.callee
	c.mu.Unlock()
	if a == nil || l == nil {
		return errors.New("answer the call before sending text")
	}
	c.ttySend.Lock() // one message at a time
	defer c.ttySend.Unlock()
	pcm := tty.Encode(text)
	pt := 0
	if strings.EqualFold(a.codecName(), "PCMA") {
		pt = 8
	}
	connect(a, nil)
	connect(l, nil)
	p := media.NewPlayer(a.ep, pt)
	p.PlayPCM(pcm)
	select {
	case <-p.Done():
	case <-c.cancelled:
	}
	p.Stop()
	c.mu.Lock()
	ok := !c.ended && c.callee == l
	c.mu.Unlock()
	if ok {
		connect(a, l)
	}
	return nil
}

// describeText answers a 911 caller's real-time text stream (an m=text line with T.140, RFC 4103):
// BitVoice itself is the far end, and the text goes to the call-taker's screen, since desk phones
// carry no text. Any other text stream, or one on a call that is not a 911 call, is refused.
func (l *Leg) describeText(s, offer *sdp.Session) {
	for i, m := range s.Media {
		if i >= len(offer.Media) {
			continue
		}
		o := offer.Media[i]
		if o.Type == "message" && strings.Contains(strings.ToUpper(o.Proto), "MSRP") {
			if o.Port == 0 || !l.textOK || !l.msrpFor(m, o) {
				m.Port = 0
			}
			continue
		}
		if o.Type != "text" {
			continue
		}
		t140, red := 0, 0
		for _, k := range o.Codecs() {
			switch strings.ToLower(k.Name) {
			case "t140":
				if t140 == 0 {
					t140 = k.PT
				}
			case "red":
				if red == 0 {
					red = k.PT
				}
			}
		}
		ep := (*media.Endpoint)(nil)
		if o.Port != 0 && t140 != 0 && l.textOK { // c.mu may be held here: only the leg's own state is read
			ep = l.textEndpoint(t140, red, s.Connection(m), o)
		}
		if ep == nil {
			m.Port = 0
			continue
		}
		m.Type, m.Port, m.Proto = "text", ep.Port(), o.Proto
		m.Formats = []string{strconv.Itoa(t140)}
		m.Lines = []sdp.Line{{Type: 'a', Value: fmt.Sprintf("rtpmap:%d t140/1000", t140)}}
		if red != 0 {
			m.Formats = []string{strconv.Itoa(red), strconv.Itoa(t140)}
			m.Lines = append([]sdp.Line{{Type: 'a', Value: fmt.Sprintf("rtpmap:%d red/1000", red)}, {Type: 'a', Value: fmt.Sprintf("fmtp:%d %d/%d/%d", red, t140, t140, t140)}}, m.Lines...)
		}
		m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "sendrecv"})
		if o.Secure() {
			l.mu.Lock()
			key := l.textKey
			l.mu.Unlock()
			if key == nil {
				m.Port = 0
				continue
			}
			m.SetCrypto([]sdp.Crypto{{Tag: l.textTag, Suite: l.textSuite, Key: key}})
		}
	}
}

// textEndpoint opens (once) the leg's text stream, sending to the address the caller offered.
func (l *Leg) textEndpoint(t140, red int, _ string, o *sdp.Media) *media.Endpoint {
	l.mu.Lock()
	if l.text != nil || l.ended {
		ep := l.text
		l.mu.Unlock()
		return ep
	}
	remote := l.remoteSDP
	l.mu.Unlock()
	host := ""
	if l.peer.Local.IsValid() {
		host = l.peer.Local.String()
	}
	ep, err := l.e.pool.AllocateOn(host)
	if err != nil {
		l.e.Log.Warn("no port for real-time text", "err", err)
		return nil
	}
	var key []byte
	var tag int
	var suite string
	if o.Secure() {
		cr, ok := supportedCrypto(o)
		if !ok {
			ep.Close()
			return nil
		}
		key, tag, suite = newMasterKey(), cr.Tag, cr.Suite
		in, err1 := srtp.New(suite, cr.Key[:16], cr.Key[16:])
		out, err2 := srtp.New(suite, key[:16], key[16:])
		if err1 != nil || err2 != nil {
			ep.Close()
			return nil
		}
		ep.SetSRTP(in, out)
	}
	if remote != nil {
		ep.SetRemote(remote.Connection(o), o.Port)
	}
	recv := &rtt.Receiver{T140: t140, Red: red}
	c := l.call
	ep.Listen(func(pkt []byte) {
		if s := recv.Packet(pkt); s != "" {
			s = strings.NewReplacer(string(rtt.LineSep), "\n", "\r\n", "\n").Replace(s)
			c.textHeard("rtt", s)
		}
	})
	out := &rtt.Sender{T140: t140, Red: red, SSRC: uint32(time.Now().UnixNano()), Write: ep.SendRTP}
	l.mu.Lock()
	if l.text != nil || l.ended {
		l.mu.Unlock()
		ep.Close()
		return l.text
	}
	l.text, l.textOut, l.textKey, l.textTag, l.textSuite = ep, out, key, tag, suite
	l.mu.Unlock()
	ep.Start()
	l.rttOn.Store(true) // offered: the call-taker may answer by text before any arrives
	return ep
}

func (l *Leg) closeText() {
	l.mu.Lock()
	ep, out := l.text, l.textOut
	l.text, l.textOut = nil, nil
	ss := l.msrp
	l.mu.Unlock()
	if ss != nil {
		ss.Close()
	}
	if out != nil {
		out.Close()
	}
	if ep != nil {
		ep.Close()
	}
}

// leavesByTrunk reports whether a plan sends the call on to another network.
func (p plan) leavesByTrunk() bool {
	for _, s := range p.stages {
		for _, t := range s.targets {
			if t.viaTrunk != "" {
				return true
			}
		}
	}
	return false
}

// hasMSRP reports whether a description offers an MSRP text session (text to 911).
func hasMSRP(s *sdp.Session) bool {
	for _, m := range s.Media {
		if m.Type == "message" && m.Port != 0 && strings.Contains(strings.ToUpper(m.Proto), "MSRP") {
			return true
		}
	}
	return false
}

// answerText answers a text-to-911 session itself: the conversation waits on the 911 screen until a
// call-taker takes it.
func (c *Call) answerText() {
	c.mu.Lock()
	a := c.caller
	offer := c.offer
	if c.ended || a == nil || offer == nil {
		c.mu.Unlock()
		return
	}
	ans := offer.Clone()
	stripICE(ans)
	ans.SetConnection(c.e.mediaHost(a.peer))
	for _, m := range ans.Media {
		m.Port = 0
	}
	ans.Lines = replaceOrigin(ans.Lines, nil)
	c.mu.Unlock()
	a.describeText(ans, offer)
	if !a.msrpOn.Load() {
		c.reject(488, "No Text Session")
		return
	}
	c.mu.Lock()
	c.localAnswer = true
	c.answer = time.Now()
	c.answeredBy = "Text to 911"
	a.mu.Lock()
	a.localSDP = ans
	a.answered = true
	a.mu.Unlock()
	ok := c.response(200, ans)
	c.mu.Unlock()
	c.e.mu.Lock()
	delete(c.e.pending, a.invite.Branch())
	c.e.mu.Unlock()
	a.invite.Respond(ok)
	c.ansOnce.Do(func() { close(c.answered) })
	c.e.publish("call.updated", c.info())
}

// TakeTextCall makes a call-taker the one handling a text-to-911 conversation: the answer that counts
// for answer times, and the moment CAD is told.
func (e *Engine) TakeTextCall(id, ext, name string) error {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	c.mu.Lock()
	text, taken, ended := c.textOnly, firstNonEmptyStr(c.takenName, c.takenBy), c.ended
	c.mu.Unlock()
	switch {
	case ended:
		return errors.New("the conversation has ended")
	case !text:
		return errors.New("this is a voice call: answer it on a phone")
	case taken != "":
		return fmt.Errorf("already taken by %s", taken)
	}
	if ext == "" {
		ext = name
	}
	c.emergencyAnswered(&Leg{Ext: ext, Name: name})
	e.publish("call.updated", c.info())
	return nil
}

// EndTextCall ends a text-to-911 conversation from the PSAP's side.
func (e *Engine) EndTextCall(id string) error {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	c.hangup("ended by the call-taker", "callee")
	return nil
}

// msrpFor opens (once) the leg's text-to-911 session for an offered m=message line, and fills in the
// answer's line. It listens for the TCC's connection, or connects when the TCC asks to be passive.
func (l *Leg) msrpFor(m, o *sdp.Media) bool {
	l.mu.Lock()
	ss := l.msrp
	ended := l.ended
	l.mu.Unlock()
	path, _ := o.Attr("path")
	setup, _ := o.Attr("setup")
	if ended || path == "" {
		return false
	}
	secure := strings.EqualFold(o.Proto, "TCP/TLS/MSRP") || msrp.Secure(strings.Fields(path)[0])
	if secure && l.e.TLS == nil {
		l.e.Log.Warn("text to 911 over TLS refused: this server has no certificate (Settings › Certificates)", "call", l.call.ID)
		return false
	}
	if ss == nil {
		host := l.e.mediaHost(l.peer)
		active := setup == "passive"
		var ln net.Listener
		port := 9 // RFC 4975 8.1: an active end that will not be connected to says port 9
		if !active {
			var err error
			if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
				l.e.Log.Warn("no port for text to 911", "err", err)
				return false
			}
			port = ln.Addr().(*net.TCPAddr).Port
		}
		local := msrp.Path(host, port)
		if secure {
			local = msrp.SecurePath(host, port)
			fp, _ := o.Attr("fingerprint")
			l.mu.Lock()
			l.msrpTLS, l.msrpPeerFP = true, fp
			l.mu.Unlock()
			if ln != nil {
				ln = &tlsListener{Listener: tls.NewListener(ln, l.msrpServerTLS(fp)), tcp: ln.(*net.TCPListener)}
			}
		}
		ss = msrp.New(local, strings.Fields(path)[0])
		c := l.call
		ss.OnMessage = func(msg msrp.Message) {
			if s, ok := msg.Text(); ok && strings.TrimSpace(s) != "" {
				c.textHeard("msrp", strings.TrimRight(s, "\r\n")+"\n")
			}
		}
		l.mu.Lock()
		l.msrp, l.msrpActive = ss, active
		l.mu.Unlock()
		go l.serveMSRP(ss, ln)
	}
	m.Type, m.Port, m.Proto = "message", portOf(ss.Local), o.Proto
	m.Formats = []string{"*"}
	m.Lines = []sdp.Line{{Type: 'a', Value: "accept-types:text/plain message/cpim"}, {Type: 'a', Value: "path:" + ss.Local}}
	if l.msrpTLS {
		if der := l.e.ownCertificate(l.e.mediaHost(l.peer)); der != nil {
			m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "fingerprint:" + msrp.Fingerprint(der)})
		}
	}
	if l.msrpActive {
		m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "setup:active"})
	} else if setup != "" {
		m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "setup:passive"})
	}
	m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "sendrecv"})
	l.msrpOn.Store(true)
	return true
}

func portOf(path string) int {
	hp, err := msrp.HostPort(path)
	if err != nil {
		return 0
	}
	_, p, _ := net.SplitHostPort(hp)
	n, _ := strconv.Atoi(p)
	return n
}

// serveMSRP runs a text-to-911 session: it takes the TCC's connection (one, within 30 seconds) or
// makes it, then reads until either side ends. The call ends when the session does.
func (l *Leg) serveMSRP(ss *msrp.Session, ln net.Listener) {
	var conn net.Conn
	var err error
	if ln != nil {
		deadline := time.Now().Add(30 * time.Second)
		if tl, ok := ln.(*tlsListener); ok {
			_ = tl.tcp.SetDeadline(deadline)
		} else {
			_ = ln.(*net.TCPListener).SetDeadline(deadline)
		}
		conn, err = ln.Accept()
		ln.Close()
		if tc, ok := conn.(*tls.Conn); ok && err == nil {
			// the handshake (and the TCC's certificate check) before any MSRP is read
			_ = tc.SetDeadline(deadline)
			if err = tc.Handshake(); err != nil {
				tc.Close()
			} else {
				_ = tc.SetDeadline(time.Time{})
			}
		}
	} else {
		hp, herr := msrp.HostPort(ss.Remote)
		if herr != nil {
			err = herr
		} else if conn, err = l.dialMSRP(ss.Remote, hp); err == nil {
			go func() {
				if ss.Wait(5 * time.Second) {
					_ = ss.Bind()
				}
			}()
		}
	}
	if err != nil {
		l.e.Log.Warn("text to 911: no connection from the text control centre", "call", l.call.ID, "err", err)
		l.call.hangup("the text session never connected", "system")
		return
	}
	if err := ss.Serve(conn); err != nil {
		l.e.Log.Warn("text to 911 session failed", "call", l.call.ID, "err", err)
	}
	l.mu.Lock()
	ended := l.ended
	l.mu.Unlock()
	if !ended && l.call.textOnly {
		l.call.hangup("the text session closed", "caller")
	}
}

// textMessage takes a SIP MESSAGE (RFC 3428) in a 911 caller's dialog as text from the caller, and
// opens the way back: replies go the same way. It reports false for a message with no text in it.
func (l *Leg) textMessage(m *sip.Message) bool {
	msg := msrp.Message{ContentType: m.Get("Content-Type"), Body: m.Body}
	s, ok := msg.Text()
	if !ok || strings.TrimSpace(s) == "" || len(s) > maxTextLine {
		return false
	}
	c := l.call
	c.mu.Lock()
	if c.senders == nil {
		c.senders = map[string]func(string) error{}
	}
	c.senders["sip"] = func(text string) error {
		r := l.newRequest(sip.MESSAGE)
		r.Set("Content-Type", "text/plain;charset=UTF-8")
		r.Body = []byte(text)
		resp := l.request(r)
		if resp == nil || resp.StatusCode >= 300 {
			return errors.New("the caller's side did not take the message")
		}
		return nil
	}
	c.mu.Unlock()
	c.textHeard("sip", strings.TrimRight(s, "\r\n")+"\n")
	return true
}

// msrpServerTLS is the TLS a TCC connects to for msrps: this server's certificate, and the TCC's
// own certificate asked for: it must be the one its SDP named (a=fingerprint), or, when it named
// none, one traced to the PSAP Credentialing Agency (Core services) when those roots are set.
func (l *Leg) msrpServerTLS(fp string) *tls.Config {
	cfg := l.e.TLS.Clone()
	cfg.MinVersion = tls.VersionTLS12
	cfg.ClientAuth = tls.RequestClientCert
	roots := l.e.pcaRoots()
	cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
		return checkTCCCert(raw, fp, roots, "")
	}
	return cfg
}

// dialMSRP connects to a passive TCC: over TLS for an msrps path, checking its certificate the same
// way (and its name, when it is checked against roots).
func (l *Leg) dialMSRP(path, hp string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !msrp.Secure(path) {
		return d.Dial("tcp", hp)
	}
	l.mu.Lock()
	fp := l.msrpPeerFP
	l.mu.Unlock()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if l.e.TLSClient != nil {
		cfg = l.e.TLSClient.Clone()
	}
	host, _, _ := net.SplitHostPort(hp)
	roots := l.e.pcaRoots()
	cfg.InsecureSkipVerify = true // checked below: by fingerprint, or against the roots by name
	cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
		return checkTCCCert(raw, fp, roots, host)
	}
	return tls.DialWithDialer(d, "tcp", hp, cfg)
}

// checkTCCCert: by its SDP fingerprint when there is one; else chained to the PCA roots (or, for a
// TCC this server connects to, the system's roots) and, when this server connected, for its name.
// A TCC that connects with no certificate and no fingerprint is taken only when no roots are set:
// the session is then encrypted, its far end known only from SIP.
func checkTCCCert(raw [][]byte, fp string, roots *x509.CertPool, host string) error {
	if fp != "" {
		if len(raw) == 0 || !msrp.FingerprintMatches(fp, raw[0]) {
			return errors.New("msrps: the text control centre's certificate is not the one its SDP named")
		}
		return nil
	}
	if len(raw) == 0 {
		if roots != nil || host != "" {
			return errors.New("msrps: the text control centre presented no certificate")
		}
		return nil
	}
	certs := make([]*x509.Certificate, 0, len(raw))
	for _, b := range raw {
		c, err := x509.ParseCertificate(b)
		if err != nil {
			return err
		}
		certs = append(certs, c)
	}
	opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool(), DNSName: host,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(opts)
	return err
}

// pcaRoots are the PSAP Credentialing Agency roots from Core services, or nil.
func (e *Engine) pcaRoots() *x509.CertPool {
	r := strings.TrimSpace(e.Config().System.Core.PCARoots)
	if r == "" {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(r)) {
		return nil
	}
	return pool
}

// ownCertificate is this server's certificate for a name (DER), or nil.
func (e *Engine) ownCertificate(host string) []byte {
	if e.TLS == nil {
		return nil
	}
	var c *tls.Certificate
	if e.TLS.GetCertificate != nil {
		c, _ = e.TLS.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
	} else if len(e.TLS.Certificates) > 0 {
		c = &e.TLS.Certificates[0]
	}
	if c == nil || len(c.Certificate) == 0 {
		return nil
	}
	return c.Certificate[0]
}

// tlsListener is a TLS listener that keeps its TCP listener, for the accept deadline.
type tlsListener struct {
	net.Listener
	tcp *net.TCPListener
}
