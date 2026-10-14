package main

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// splitPort sorts the connections of the web port by their first byte: a TLS handshake (0x16) goes to
// the HTTPS server, anything else is plain HTTP. Plain HTTP gets only what plainHandler allows (desk
// phones' settings files) and a redirect to HTTPS for everything else.
type splitPort struct {
	ln         net.Listener
	tls, plain *chanListener
	closeOnce  sync.Once
}

func newSplitPort(ln net.Listener) *splitPort {
	p := &splitPort{ln: ln, tls: newChanListener(ln.Addr()), plain: newChanListener(ln.Addr())}
	go p.run()
	return p
}

func (p *splitPort) run() {
	defer p.Close()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		go p.sort(c)
	}
}

// sort reads the first byte off the accept loop, so a client that sends nothing holds up no one.
func (p *splitPort) sort(c net.Conn) {
	br := bufio.NewReaderSize(c, 4096)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return
	}
	pc := &peekedConn{Conn: c, r: br}
	if b[0] == 0x16 {
		p.tls.put(pc)
	} else {
		p.plain.put(pc)
	}
}

func (p *splitPort) Close() error {
	var err error
	p.closeOnce.Do(func() {
		err = p.ln.Close()
		p.tls.Close()
		p.plain.Close()
	})
	return err
}

// plainHandler serves the desk phones' settings files over plain HTTP when phones may use it, and
// sends every other plain request on to HTTPS. Nothing else is ever served without TLS.
func plainHandler(next http.Handler, phonesPlain func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/provision/") && r.Method == http.MethodGet && phonesPlain() {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if host == "" || strings.ContainsAny(host, " \r\n/\\@") {
			host = r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()
		}
		target := (&url.URL{Scheme: "https", Host: host}).String() + r.URL.RequestURI()
		body := "This server speaks HTTPS only: " + target + "\n"
		w.Header().Set("Location", target)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusPermanentRedirect)
		_, _ = w.Write([]byte(body))
	})
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// chanListener is a net.Listener fed by splitPort.
type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(a net.Addr) *chanListener {
	return &chanListener{addr: a, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) put(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		_ = c.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }
