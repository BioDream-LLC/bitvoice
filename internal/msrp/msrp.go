// Package msrp speaks the Message Session Relay Protocol (RFC 4975) for one session: the text
// messages a text control centre (TCC) sends to a PSAP for text to 911, and the replies. It takes
// SEND requests (in one chunk or several), answers each, and sends text. Relays (RFC 4976) are not
// used: a TCC connects to the PSAP directly.
package msrp

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxMessage bounds one message, all chunks together.
const MaxMessage = 64 << 10

// NewID makes a session or transaction ID.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Message is one complete message received.
type Message struct {
	ID          string
	ContentType string
	Body        []byte
}

// Text returns a message's text: text/plain as it is, the text/plain inside message/cpim (RFC 3862),
// and nothing for anything else (an is-composing notice, a picture).
func (m Message) Text() (string, bool) {
	ct, _, err := mime.ParseMediaType(m.ContentType)
	if err != nil {
		return "", false
	}
	switch ct {
	case "text/plain":
		return string(m.Body), true
	case "message/cpim":
		// CPIM: headers, a blank line, then the MIME headers of the content, a blank line, the content.
		parts := bytes.SplitN(m.Body, []byte("\r\n\r\n"), 3)
		if len(parts) < 3 {
			parts = bytes.SplitN(m.Body, []byte("\n\n"), 3)
		}
		if len(parts) == 3 {
			inner := Message{Body: parts[2]}
			for _, h := range strings.Split(string(parts[1]), "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(h), ":"); ok && strings.EqualFold(k, "Content-Type") {
					inner.ContentType = strings.TrimSpace(v)
				}
			}
			if inner.ContentType != "" {
				return inner.Text()
			}
		}
	}
	return "", false
}

// Session is one MSRP session over one connection.
type Session struct {
	Local  string // this end's path (msrp://host:port/id;tcp)
	Remote string // the far end's path
	// OnMessage is called with each complete message, from the connection's reader.
	OnMessage func(Message)

	mu     sync.Mutex
	conn   net.Conn
	w      *bufio.Writer
	closed bool
	ready  chan struct{}
	once   sync.Once
	pend   map[string]*partial // by Message-ID
}

type partial struct {
	ct   string
	buf  []byte
	size int
}

// Path makes an MSRP path for a host and port, with a new session ID.
func Path(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "msrp://" + host + ":" + strconv.Itoa(port) + "/" + NewID() + ";tcp"
}

// sessionID is the session part of a path.
func sessionID(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	id, _, _ := strings.Cut(path[i+1:], ";")
	return id
}

// HostPort is where a path says to connect.
func HostPort(path string) (string, error) {
	if len(path) < 7 || !strings.EqualFold(path[:7], "msrp://") {
		return "", errors.New("not an msrp:// path")
	}
	hp, _, _ := strings.Cut(path[7:], "/")
	if _, _, err := net.SplitHostPort(hp); err != nil {
		return "", err
	}
	return hp, nil
}

// New starts a session between two paths.
func New(local, remote string) *Session {
	return &Session{Local: local, Remote: remote, ready: make(chan struct{}), pend: map[string]*partial{}}
}

// Serve runs the session on a connection, until it closes. A connection whose first request is not
// for this session is refused.
func (s *Session) Serve(c net.Conn) error {
	s.mu.Lock()
	if s.closed || s.conn != nil {
		s.mu.Unlock()
		c.Close()
		return errors.New("the session already has a connection")
	}
	s.conn, s.w = c, bufio.NewWriter(c)
	s.mu.Unlock()
	s.once.Do(func() { close(s.ready) })
	defer s.Close()
	r := bufio.NewReaderSize(c, 8<<10)
	for {
		req, err := read(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := s.handle(req); err != nil {
			return err
		}
	}
}

// Wait waits for the session's connection, up to d.
func (s *Session) Wait(d time.Duration) bool {
	select {
	case <-s.ready:
		return true
	case <-time.After(d):
		return false
	}
}

// Close ends the session.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// frame is one request or response.
type frame struct {
	tid     string
	method  string // SEND, REPORT, or "" for a response
	status  int
	headers map[string]string
	body    []byte
	flag    byte // $ complete, + more chunks, # aborted
}

func (f *frame) get(k string) string { return f.headers[strings.ToLower(k)] }

// read reads one frame (RFC 4975 7.1: start line, headers, an optional body, the end line).
func read(r *bufio.Reader) (*frame, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 || parts[0] != "MSRP" || parts[1] == "" {
		return nil, errors.New("msrp: bad start line")
	}
	f := &frame{tid: parts[1], headers: map[string]string{}}
	if n, err := strconv.Atoi(parts[2]); err == nil && len(parts[2]) == 3 {
		f.status = n
	} else {
		f.method = parts[2]
	}
	end := "-------" + f.tid
	for {
		h, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(h, end) && len(h) == len(end)+1 { // no body
			f.flag = h[len(end)]
			return f, nil
		}
		if h == "" {
			break
		}
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, errors.New("msrp: bad header")
		}
		if len(f.headers) > 32 {
			return nil, errors.New("msrp: too many headers")
		}
		f.headers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	// The body runs to CRLF and the end line; it may hold anything else.
	marker := []byte("\r\n" + end)
	var body []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		body = append(body, b)
		if len(body) > MaxMessage+len(marker)+2 {
			return nil, errors.New("msrp: chunk too large")
		}
		if len(body) >= len(marker)+1 && bytes.Equal(body[len(body)-len(marker)-1:len(body)-1], marker) {
			f.flag = body[len(body)-1]
			f.body = body[:len(body)-len(marker)-1]
			if _, err := readLine(r); err != nil { // the rest of the end line
				return nil, err
			}
			return f, nil
		}
	}
}

func readLine(r *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, more, err := r.ReadLine()
		if err != nil {
			return "", err
		}
		b = append(b, chunk...)
		if len(b) > 4096 {
			return "", errors.New("msrp: line too long")
		}
		if !more {
			return string(b), nil
		}
	}
}

func (s *Session) handle(f *frame) error {
	if f.method == "" { // a response to what we sent
		return nil
	}
	if sessionID(firstPath(f.get("To-Path"))) != sessionID(s.Local) {
		s.respond(f, 481, "Session Does Not Exist")
		return errors.New("msrp: a request for another session")
	}
	switch f.method {
	case "SEND":
	case "REPORT":
		return nil // reports get no response
	default:
		s.respond(f, 501, "Not Implemented")
		return nil
	}
	if f.get("Failure-Report") != "no" {
		s.respond(f, 200, "OK")
	}
	id := f.get("Message-ID")
	if id == "" || len(f.body) == 0 && f.flag == '$' && s.pend[id] == nil {
		return nil // an empty SEND opens the connection
	}
	p := s.pend[id]
	if p == nil {
		if len(s.pend) > 16 {
			return errors.New("msrp: too many messages in progress")
		}
		p = &partial{ct: f.get("Content-Type"), size: -1}
		s.pend[id] = p
	}
	start, _, total := byteRange(f.get("Byte-Range"))
	if total > MaxMessage {
		delete(s.pend, id)
		return nil
	}
	if total >= 0 {
		p.size = total
	}
	if start > 0 && start-1 == len(p.buf) { // chunks in order, as one connection carries them
		p.buf = append(p.buf, f.body...)
	} else if start-1 < len(p.buf) && start > 0 {
		p.buf = append(p.buf[:start-1], f.body...) // a resent chunk
	}
	if len(p.buf) > MaxMessage {
		delete(s.pend, id)
		return nil
	}
	switch f.flag {
	case '#':
		delete(s.pend, id)
	case '$':
		delete(s.pend, id)
		if s.OnMessage != nil {
			s.OnMessage(Message{ID: id, ContentType: p.ct, Body: p.buf})
		}
	}
	return nil
}

func firstPath(p string) string {
	f := strings.Fields(p)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1] // with no relays the path is one URI; with some, ours is the last
}

// byteRange parses "start-end/total"; end and total are -1 for "*".
func byteRange(v string) (start, end, total int) {
	start, end, total = 1, -1, -1
	r, t, ok := strings.Cut(v, "/")
	if !ok {
		return
	}
	a, b, _ := strings.Cut(r, "-")
	if n, err := strconv.Atoi(a); err == nil && n > 0 {
		start = n
	}
	if n, err := strconv.Atoi(b); err == nil {
		end = n
	}
	if n, err := strconv.Atoi(t); err == nil {
		total = n
	}
	return
}

func (s *Session) respond(f *frame, code int, phrase string) {
	var b strings.Builder
	fmt.Fprintf(&b, "MSRP %s %03d %s\r\n", f.tid, code, phrase)
	fmt.Fprintf(&b, "To-Path: %s\r\n", firstPath(f.get("From-Path")))
	fmt.Fprintf(&b, "From-Path: %s\r\n", s.Local)
	fmt.Fprintf(&b, "-------%s$\r\n", f.tid)
	s.write(b.String())
}

func (s *Session) write(b string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil || s.closed {
		return errors.New("msrp: the session is not connected")
	}
	if _, err := s.w.WriteString(b); err != nil {
		return err
	}
	return s.w.Flush()
}

// Send sends one text message (text/plain, UTF-8) in one chunk.
func (s *Session) Send(text string) error {
	if len(text) > MaxMessage {
		return errors.New("msrp: message too long")
	}
	tid := NewID()
	var b strings.Builder
	fmt.Fprintf(&b, "MSRP %s SEND\r\n", tid)
	fmt.Fprintf(&b, "To-Path: %s\r\n", s.Remote)
	fmt.Fprintf(&b, "From-Path: %s\r\n", s.Local)
	fmt.Fprintf(&b, "Message-ID: %s\r\n", NewID())
	fmt.Fprintf(&b, "Byte-Range: 1-%d/%d\r\n", len(text), len(text))
	b.WriteString("Content-Type: text/plain;charset=UTF-8\r\n\r\n")
	b.WriteString(text)
	fmt.Fprintf(&b, "\r\n-------%s$\r\n", tid)
	return s.write(b.String())
}

// Bind sends the empty SEND that opens a connection this end made (RFC 4975 5.4).
func (s *Session) Bind() error {
	tid := NewID()
	return s.write(fmt.Sprintf("MSRP %s SEND\r\nTo-Path: %s\r\nFrom-Path: %s\r\nMessage-ID: %s\r\nByte-Range: 1-0/0\r\n-------%s$\r\n",
		tid, s.Remote, s.Local, NewID(), tid))
}
