package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// redirectPlain wraps the HTTPS listener: a browser that speaks plain HTTP to it (someone typed
// http://) is answered with a redirect to the same address over HTTPS and nothing else. No page,
// no API and no cookie is ever served without TLS.
type redirectPlain struct{ net.Listener }

func (l redirectPlain) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &sniffConn{Conn: c}, nil
}

// sniffConn looks at the first byte the client sends: a TLS handshake starts with 0x16; anything
// else is plain HTTP and gets the redirect.
type sniffConn struct {
	net.Conn
	once sync.Once
	r    *bufio.Reader
	err  error
}

func (c *sniffConn) Read(p []byte) (int, error) {
	c.once.Do(func() {
		c.r = bufio.NewReaderSize(c.Conn, 4096)
		_ = c.Conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		b, err := c.r.Peek(1)
		_ = c.Conn.SetReadDeadline(time.Time{})
		if err != nil {
			c.err = err
			return
		}
		if b[0] != 0x16 {
			c.redirect()
			c.err = io.EOF
		}
	})
	if c.err != nil {
		return 0, c.err
	}
	return c.r.Read(p)
}

// redirect answers one plain HTTP request with 308 to https:// and closes.
func (c *sniffConn) redirect() {
	defer c.Conn.Close()
	_ = c.Conn.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(c.r)
	if err != nil {
		return
	}
	host := req.Host
	if host == "" || strings.ContainsAny(host, " \r\n/") {
		host = c.Conn.LocalAddr().String()
	}
	path := req.URL.RequestURI()
	if !strings.HasPrefix(path, "/") {
		path = "/"
	}
	target := (&url.URL{Scheme: "https", Host: host}).String() + path
	body := "This server speaks HTTPS only: " + target + "\n"
	_, _ = io.WriteString(c.Conn, "HTTP/1.1 308 Permanent Redirect\r\nLocation: "+target+
		"\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\nContent-Length: "+
		strconv.Itoa(len(body))+"\r\n\r\n"+body)
}
