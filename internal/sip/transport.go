package sip

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Peer is where a message came from or is going: a transport and an address. For TCP it also names the
// connection, because a response must go back on the connection the request arrived on.
type Peer struct {
	Transport string // UDP, TCP or TLS
	Addr      netip.AddrPort
	// Host is the name to verify when BitVoice opens a TLS connection to the peer (a trunk's host name).
	Host string
	// Local is the address of this machine the peer reached (an extra address); requests and
	// responses to the peer leave from it. Zero for the main socket.
	Local netip.Addr
	// Pin, when set, is the SHA-256 of the certificate a TLS peer must present (another BitVoice
	// server): checked instead of a certificate authority.
	Pin  []byte
	conn *streamConn
}

func (p Peer) String() string { return strings.ToLower(p.Transport) + ":" + p.Addr.String() }

// Reliable reports whether the transport is a stream (TCP or TLS), which needs no retransmission.
func (p Peer) Reliable() bool { return p.Transport == "TCP" || p.Transport == "TLS" }

// IsZero reports whether the peer is unset.
func (p Peer) IsZero() bool { return !p.Addr.IsValid() }

// Incoming is a received message.
type Incoming struct {
	Msg   *Message
	From  Peer
	Local netip.AddrPort // the local address it arrived on
}

// Stack owns the transports and the transaction layer. Handlers receive requests that start new server
// transactions; responses are matched to client transactions here.
type Stack struct {
	// Host is the address advertised in Via and Contact. Empty means the local address of the listener.
	Host string
	// Log receives one line per dropped or malformed message. Defaults to slog.Default().
	Log *slog.Logger
	// Trace, when set, sees every message sent and received (for the SIP trace screen).
	Trace func(dir string, p Peer, m *Message)

	handler func(*ServerTx, *Incoming)

	udp     *net.UDPConn
	udpPort int
	tcp     net.Listener
	tcpPort int
	tls     net.Listener
	tlsPort int
	// TLSClient configures connections BitVoice opens over TLS (to trunks). Nil verifies against the
	// system roots with the peer's host name.
	TLSClient *tls.Config
	// SharePort lets an extra address (ListenExtra) use the same port as the main sockets.
	SharePort bool
	mu        sync.Mutex
	streams   map[netip.AddrPort]*streamConn
	extra     map[netip.Addr]*extraAddr
	clients   map[string]*ClientTx
	servers   map[string]*ServerTx
	invites   map[string]*ServerTx // by Call-ID and CSeq number, to stop 2xx retransmission on ACK
	closed    bool
	wg        sync.WaitGroup
	T1        time.Duration
	T2        time.Duration
	TxExpire  time.Duration // timer B/F: 64*T1
}

// NewStack returns a stack; call Listen and Serve.
func NewStack(handler func(*ServerTx, *Incoming)) *Stack {
	return &Stack{
		handler: handler,
		streams: map[netip.AddrPort]*streamConn{},
		clients: map[string]*ClientTx{},
		servers: map[string]*ServerTx{},
		invites: map[string]*ServerTx{},
		T1:      500 * time.Millisecond,
		T2:      4 * time.Second,
	}
}

func (s *Stack) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Stack) expire() time.Duration {
	if s.TxExpire > 0 {
		return s.TxExpire
	}
	return 64 * s.T1
}

// Listen opens UDP and TCP on addr (host:port). Port 0 picks a free port, the same for both where possible.
func (s *Stack) Listen(addr string) error {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	if ua.Port == 0 {
		// Any free port: UDP picks one, and TCP must get the same number, which something else may
		// hold. Try a few.
		for range 20 {
			if err = s.listen(addr, ua); err == nil || !addrInUse(err) {
				return err
			}
		}
		return err
	}
	return s.listen(addr, ua)
}

func (s *Stack) listen(addr string, ua *net.UDPAddr) error {
	var err error
	var u *net.UDPConn
	if s.SharePort {
		u, err = listenSharedUDP(ua.String())
	} else {
		u, err = net.ListenUDP("udp", ua)
	}
	if err != nil {
		return fmt.Errorf("sip: listen udp %s: %w", addr, err)
	}
	if err := u.SetReadBuffer(4 << 20); err != nil {
		s.log().Debug("sip: could not enlarge UDP buffer", "err", err)
	}
	s.udp = u
	s.udpPort = u.LocalAddr().(*net.UDPAddr).Port
	host, _, _ := net.SplitHostPort(addr)
	var t net.Listener
	if s.SharePort {
		t, err = listenSharedTCP(net.JoinHostPort(host, strconv.Itoa(s.udpPort)))
	} else {
		t, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(s.udpPort)))
	}
	if err != nil {
		u.Close()
		return fmt.Errorf("sip: listen tcp: %w", err)
	}
	s.tcp = t
	s.tcpPort = t.Addr().(*net.TCPAddr).Port
	return nil
}

// ListenTLS opens SIP over TLS (RFC 3261 26.2) on addr with a server certificate. Call before Serve.
func (s *Stack) ListenTLS(addr string, cfg *tls.Config) error {
	l, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("sip: listen tls %s: %w", addr, err)
	}
	s.tls = l
	s.tlsPort = l.Addr().(*net.TCPAddr).Port
	return nil
}

// TLSPort returns the TLS listening port, 0 when TLS is off.
func (s *Stack) TLSPort() int { return s.tlsPort }

// Port returns the UDP (and TCP) listening port.
func (s *Stack) Port() int { return s.udpPort }

// BindIP is the address the stack listens on, or nil when it listens on every address.
func (s *Stack) BindIP() net.IP {
	if s.udp == nil {
		return nil
	}
	ip := s.udp.LocalAddr().(*net.UDPAddr).IP
	if ip == nil || ip.IsUnspecified() {
		return nil
	}
	return ip
}

// Serve reads until ctx is done or Close is called.
func (s *Stack) Serve(ctx context.Context) {
	s.wg.Add(2)
	go s.readUDP()
	go s.accept(s.tcp, "TCP")
	if s.tls != nil {
		s.wg.Add(1)
		go s.accept(s.tls, "TLS")
	}
	s.serveExtra()
	go func() {
		<-ctx.Done()
		s.Close()
	}()
}

// Close stops the transports and waits for readers.
func (s *Stack) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	streams := make([]*streamConn, 0, len(s.streams))
	for _, c := range s.streams {
		streams = append(streams, c)
	}
	s.mu.Unlock()
	if s.udp != nil {
		s.udp.Close()
	}
	if s.tcp != nil {
		s.tcp.Close()
	}
	if s.tls != nil {
		s.tls.Close()
	}
	s.closeExtra()
	for _, c := range streams {
		c.conn.Close()
	}
	s.wg.Wait()
}

func (s *Stack) readUDP() { s.readUDPOn(s.udp) }

func (s *Stack) readUDPOn(conn *net.UDPConn) {
	defer s.wg.Done()
	buf := make([]byte, MaxMessageSize)
	local := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	var via netip.Addr
	if conn != s.udp {
		via = local.Addr().Unmap()
	}
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n <= 4 && strings.TrimSpace(string(buf[:n])) == "" {
			continue // CRLF keep-alive
		}
		msg, err := Parse(buf[:n])
		if err != nil {
			s.log().Debug("sip: dropped malformed datagram", "from", from, "err", err)
			continue
		}
		s.receive(&Incoming{Msg: msg, From: Peer{Transport: "UDP", Addr: unmap(from), Local: via}, Local: local})
	}
}

func unmap(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port())
}

type streamConn struct {
	via       netip.Addr // the extra address it arrived on, if any
	conn      net.Conn
	mu        sync.Mutex
	peer      netip.AddrPort
	transport string // TCP or TLS
}

func (c *streamConn) write(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(b)
	return err
}

func (s *Stack) accept(l net.Listener, transport string) {
	defer s.wg.Done()
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		s.addStream(c, transport)
	}
}

func (s *Stack) addStream(c net.Conn, transport string) *streamConn {
	peer := unmap(c.RemoteAddr().(*net.TCPAddr).AddrPort())
	sc := &streamConn{conn: c, peer: peer, transport: transport}
	s.mu.Lock()
	if la, ok := c.LocalAddr().(*net.TCPAddr); ok && s.extra[la.AddrPort().Addr().Unmap()] != nil {
		sc.via = la.AddrPort().Addr().Unmap()
	}
	if s.closed {
		s.mu.Unlock()
		c.Close()
		return sc
	}
	s.streams[peer] = sc
	s.wg.Add(1)
	s.mu.Unlock()
	go s.readStream(sc)
	return sc
}

func (s *Stack) readStream(sc *streamConn) {
	defer s.wg.Done()
	defer func() {
		sc.conn.Close()
		s.mu.Lock()
		if s.streams[sc.peer] == sc {
			delete(s.streams, sc.peer)
		}
		s.mu.Unlock()
	}()
	local := unmap(sc.conn.LocalAddr().(*net.TCPAddr).AddrPort())
	r := bufio.NewReaderSize(sc.conn, MaxMessageSize)
	var buf []byte
	chunk := make([]byte, 8192)
	for {
		// An idle stream with nothing for ten minutes is closed; phones re-open as needed.
		_ = sc.conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				msg, used, perr := ParseStream(buf)
				if errors.Is(perr, ErrIncomplete) {
					buf = buf[used:]
					break
				}
				if perr != nil {
					s.log().Debug("sip: closing stream after malformed message", "from", sc.peer, "err", perr)
					return
				}
				buf = buf[used:]
				s.receive(&Incoming{Msg: msg, From: Peer{Transport: sc.transport, Addr: sc.peer, Local: sc.via, conn: sc}, Local: local})
			}
			if len(buf) > MaxMessageSize {
				return
			}
		}
		if err != nil {
			if err != io.EOF && !errors.Is(err, net.ErrClosed) {
				s.log().Debug("sip: stream closed", "from", sc.peer, "err", err)
			}
			return
		}
	}
}

// Send writes a message to a peer without any transaction handling.
func (s *Stack) Send(m *Message, to Peer) error {
	if s.Trace != nil {
		s.Trace("out", to, m)
	}
	b := m.Bytes()
	switch to.Transport {
	case "TCP", "TLS":
		c := to.conn
		if c == nil {
			s.mu.Lock()
			c = s.streams[to.Addr]
			s.mu.Unlock()
		}
		if c == nil {
			d := &net.Dialer{Timeout: 3 * time.Second}
			var conn net.Conn
			var err error
			if to.Transport == "TLS" {
				cfg := &tls.Config{MinVersion: tls.VersionTLS12}
				if s.TLSClient != nil {
					cfg = s.TLSClient.Clone()
				}
				if cfg.ServerName == "" {
					cfg.ServerName = to.Host
				}
				if len(to.Pin) > 0 {
					pin := to.Pin
					cfg.InsecureSkipVerify = true // the pin below is the check
					cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
						if len(raw) == 0 {
							return errors.New("sip: the server presented no certificate")
						}
						sum := sha256.Sum256(raw[0])
						if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
							return errors.New("sip: the server's certificate is not the one it published")
						}
						return nil
					}
				}
				conn, err = tls.DialWithDialer(d, "tcp", to.Addr.String(), cfg)
			} else {
				conn, err = d.Dial("tcp", to.Addr.String())
			}
			if err != nil {
				return fmt.Errorf("sip: connect %s: %w", to.Addr, err)
			}
			c = s.addStream(conn, to.Transport)
		}
		return c.write(b)
	default:
		if s.udp == nil {
			return errors.New("sip: no UDP transport")
		}
		_, err := s.udpFor(to).WriteToUDPAddrPort(b, to.Addr)
		return err
	}
}

// SendRaw writes raw bytes to a UDP peer (CRLF keep-alives that hold NAT bindings open).
func (s *Stack) SendRaw(b []byte, to Peer) error {
	if to.Transport != "UDP" {
		if to.conn != nil {
			return to.conn.write(b)
		}
		return nil
	}
	if s.udp == nil {
		return errors.New("sip: no UDP transport")
	}
	_, err := s.udpFor(to).WriteToUDPAddrPort(b, to.Addr)
	return err
}

// Stateless sends a request outside any transaction (the ACK for a 2xx), adding a fresh top Via.
func (s *Stack) Stateless(req *Message, to Peer) error {
	via := &Via{Transport: to.Transport, Host: s.LocalHost(to), Port: s.LocalPort(to.Transport),
		Params: Params{{Name: "branch", Value: NewBranch()}, {Name: "rport", Flag: true}}}
	req.Prepend("Via", via.String())
	if req.Get("Max-Forwards") == "" {
		req.Set("Max-Forwards", "70")
	}
	return s.Send(req, to)
}

// LocalHost returns the host to advertise for messages to a peer.
func (s *Stack) LocalHost(to Peer) string {
	if to.Addr.Addr().IsLoopback() {
		// A peer on this machine (the browser phone gateway) is reached on loopback, whatever the
		// public address is.
		if to.Addr.Addr().Is6() && !to.Addr.Addr().Is4In6() {
			return "::1"
		}
		return "127.0.0.1"
	}
	if to.Local.IsValid() {
		return to.Local.String() // the peer reached the shared address: it is the one to give
	}
	if s.Host != "" {
		return s.Host
	}
	// Ask the kernel which local address routes to the peer. No packet is sent.
	if c, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(to.Addr)); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return "127.0.0.1"
}

// LocalPort returns the advertised port for a transport.
func (s *Stack) LocalPort(transport string) int {
	switch transport {
	case "TCP":
		return s.tcpPort
	case "TLS":
		return s.tlsPort
	}
	return s.udpPort
}

// ResponseTarget works out where to send a response from the top Via (RFC 3261 18.2.2, RFC 3581).
func ResponseTarget(req *Message, from Peer) Peer {
	if from.Transport != "UDP" {
		return from
	}
	// Symmetric response routing: answer the address the request came from. This is what makes phones behind
	// NAT work without configuration, and RFC 3581 asks for it when rport is present. Answering the source even
	// without rport is what every deployed proxy does in practice.
	return from
}

func (s *Stack) receive(in *Incoming) {
	if s.Trace != nil {
		s.Trace("in", in.From, in.Msg)
	}
	m := in.Msg
	if err := m.Validate(); err != nil {
		s.log().Debug("sip: dropped invalid message", "from", in.From, "err", err)
		if m.IsRequest && m.Method != ACK && m.Get("Via") != "" {
			_ = s.Send(NewResponse(m, 400, err.Error()), ResponseTarget(m, in.From))
		}
		return
	}
	if !m.IsRequest {
		s.receiveResponse(in)
		return
	}
	// Record where it came from in the top Via (RFC 3261 18.2.1, RFC 3581).
	if v, err := ParseVia(m.Get("Via")); err == nil {
		if v.Host != in.From.Addr.Addr().String() {
			v.Params.Set("received", in.From.Addr.Addr().String())
		}
		if _, ok := v.Params.Get("rport"); ok {
			v.Params.Set("rport", strconv.Itoa(int(in.From.Addr.Port())))
		}
		m.Headers[indexOf(m, "Via")].Value = v.String()
	}
	s.receiveRequest(in)
}

func indexOf(m *Message, name string) int {
	for i, h := range m.Headers {
		if h.Name == name {
			return i
		}
	}
	return -1
}

// NewResponse builds a response to a request, copying the headers RFC 3261 8.2.6 requires.
func NewResponse(req *Message, code int, reason string) *Message {
	r := &Message{StatusCode: code, Reason: reason}
	for _, h := range req.Headers {
		switch h.Name {
		case "Via", "From", "To", "Call-ID", "CSeq", "Record-Route":
			r.Headers = append(r.Headers, h)
		}
	}
	// Record-Route is echoed only in responses that can create a dialog (RFC 3261 12.1.1).
	if code <= 100 || code >= 300 {
		r.Del("Record-Route")
	}
	r.Set("Server", "BitVoice")
	return r
}

// ParseInt is a small helper for numeric header values.
func ParseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
