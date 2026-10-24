package ldap

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Conn is a connection to a directory server. One request at a time.
type Conn struct {
	mu      sync.Mutex
	conn    net.Conn
	r       *bufio.Reader
	id      int64
	timeout time.Duration
	tls     bool
}

// Options say how to reach the server.
type Options struct {
	// URL is ldap://host[:389] or ldaps://host[:636].
	URL string
	// StartTLS upgrades an ldap:// connection to TLS before anything else is sent.
	StartTLS bool
	// TLS checks the server's certificate (RootCAs for a company's own CA); its ServerName is set from the URL.
	TLS *tls.Config
	// Timeout bounds connecting and each request (default 10 s).
	Timeout time.Duration
}

// Result codes this package looks at (RFC 4511 appendix A).
const (
	Success                 = 0
	SizeLimitExceeded       = 4
	InvalidCredentials      = 49
	NoSuchObject            = 32
	InsufficientAccess      = 50
	ConfidentialityRequired = 13
)

// Error is a result the server answered other than success.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	name := map[int]string{1: "operations error", 2: "protocol error", 3: "time limit exceeded", 4: "size limit exceeded",
		7: "authentication method not supported", 8: "stronger authentication required", 10: "referral", 11: "administrative limit exceeded",
		13: "confidentiality required", 32: "no such object", 34: "invalid DN syntax", 48: "inappropriate authentication",
		49: "invalid credentials", 50: "insufficient access rights", 51: "busy", 52: "unavailable", 53: "unwilling to perform",
		80: "other"}[e.Code]
	if name == "" {
		name = fmt.Sprintf("result %d", e.Code)
	}
	if e.Message != "" {
		return "ldap: " + name + ": " + strings.TrimRight(e.Message, "\x00")
	}
	return "ldap: " + name
}

// IsCode says whether err is a server result of that code.
func IsCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Dial connects to a directory server.
func Dial(ctx context.Context, o Options) (*Conn, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" {
		return nil, errors.New("ldap: give the server as ldap://host or ldaps://host")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	host, port := u.Hostname(), u.Port()
	var useTLS bool
	switch strings.ToLower(u.Scheme) {
	case "ldap":
		if port == "" {
			port = "389"
		}
	case "ldaps":
		if port == "" {
			port = "636"
		}
		useTLS = true
	default:
		return nil, errors.New("ldap: the address starts ldap:// or ldaps://")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.TLS != nil {
		tc = o.TLS.Clone()
	}
	if tc.ServerName == "" {
		tc.ServerName = host
	}
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	c := &Conn{conn: raw, timeout: timeout}
	if useTLS {
		tconn := tls.Client(raw, tc)
		raw.SetDeadline(time.Now().Add(timeout))
		if err := tconn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("ldap: TLS with %s: %w", host, err)
		}
		c.conn, c.tls = tconn, true
	}
	c.r = bufio.NewReader(c.conn)
	if o.StartTLS && !useTLS {
		if err := c.startTLS(ctx, tc); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// Secure says whether the connection is encrypted.
func (c *Conn) Secure() bool { return c.tls }

// Close says goodbye (UnbindRequest) and closes the connection.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	c.id++
	msg := sequence(integer(c.id), prim(classApplication|2, nil))
	c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	c.conn.Write(msg.encode())
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Conn) send(op *element, controls ...*element) (int64, error) {
	if c.conn == nil {
		return 0, errors.New("ldap: the connection is closed")
	}
	c.id++
	msg := sequence(integer(c.id), op)
	if len(controls) > 0 {
		msg.children = append(msg.children, cons(classContext|0, controls...))
	}
	c.conn.SetDeadline(time.Now().Add(c.timeout))
	_, err := c.conn.Write(msg.encode())
	return c.id, err
}

// message is one answer: its operation and its controls.
type message struct {
	op       *element
	controls []*element
}

func (c *Conn) receive(id int64) (message, error) {
	for {
		c.conn.SetReadDeadline(time.Now().Add(c.timeout))
		e, err := readElement(c.r)
		if err != nil {
			return message{}, fmt.Errorf("ldap: reading the answer: %w", err)
		}
		if e.tag != tagSequence || len(e.children) < 2 {
			return message{}, errors.New("ldap: the server sent something that is not an LDAP message")
		}
		got, err := decodeInt(e.children[0].value)
		if err != nil {
			return message{}, err
		}
		m := message{op: e.children[1]}
		if len(e.children) > 2 && e.children[2].tag == classContext|constructed|0 {
			m.controls = e.children[2].children
		}
		if got == 0 && m.op.tag == classApplication|constructed|24 {
			// An unsolicited notice: the server is closing the connection.
			res, _ := parseResult(m.op)
			return message{}, res
		}
		if got != id {
			continue // an answer to something abandoned
		}
		return m, nil
	}
}

// parseResult reads an LDAPResult: code, matched DN, message (and nil error on success).
func parseResult(op *element) (*Error, error) {
	if len(op.children) < 3 {
		return nil, errors.New("ldap: a short result")
	}
	code, err := decodeInt(op.children[0].value)
	if err != nil {
		return nil, err
	}
	return &Error{Code: int(code), Message: string(op.children[2].value)}, nil
}

func resultErr(op *element) error {
	r, err := parseResult(op)
	if err != nil {
		return err
	}
	if r.Code == Success {
		return nil
	}
	return r
}

func (c *Conn) startTLS(ctx context.Context, tc *tls.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.send(cons(classApplication|23, prim(classContext|0, []byte("1.3.6.1.4.1.1466.20037"))))
	if err != nil {
		return err
	}
	m, err := c.receive(id)
	if err != nil {
		return err
	}
	if m.op.tag != classApplication|constructed|24 {
		return errors.New("ldap: StartTLS was not answered")
	}
	if err := resultErr(m.op); err != nil {
		return fmt.Errorf("ldap: the server refused StartTLS: %w", err)
	}
	if c.r.Buffered() > 0 {
		return errors.New("ldap: the server sent data before TLS began")
	}
	tconn := tls.Client(c.conn, tc)
	c.conn.SetDeadline(time.Now().Add(c.timeout))
	if err := tconn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("ldap: StartTLS with %s: %w", tc.ServerName, err)
	}
	c.conn, c.tls = tconn, true
	c.r = bufio.NewReader(tconn)
	return nil
}

// ErrEmptyPassword is a bind asked for with no password: LDAP would take it as an anonymous bind and
// report success, so it is refused before it is sent.
var ErrEmptyPassword = errors.New("ldap: a password is required")

// Bind signs in as dn with a password (a simple bind).
func (c *Conn) Bind(dn, password string) error {
	if password == "" {
		return ErrEmptyPassword
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.send(cons(classApplication|0, integer(3), octets(dn), prim(classContext|0, []byte(password))))
	if err != nil {
		return err
	}
	m, err := c.receive(id)
	if err != nil {
		return err
	}
	if m.op.tag != classApplication|constructed|1 {
		return errors.New("ldap: the bind was not answered")
	}
	return resultErr(m.op)
}

// Scope is how deep a search goes.
type Scope int

const (
	ScopeBase Scope = iota
	ScopeOne
	ScopeSubtree
)

// Entry is one entry found: its DN and attributes (names as the server wrote them; Get ignores case).
type Entry struct {
	DN    string
	Attrs map[string][]string
}

// Get returns an attribute's first value ("" when it has none); names compare without case.
func (e Entry) Get(name string) string {
	if v := e.All(name); len(v) > 0 {
		return v[0]
	}
	return ""
}

// All returns an attribute's values.
func (e Entry) All(name string) []string {
	for k, v := range e.Attrs {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

// Search finds entries under base matching filter, fetching attrs ("" for all), pageSize at a time
// (Active Directory answers at most 1,000 entries to a search without paging). limit stops after that
// many entries (0: no limit).
func (c *Conn) Search(base string, scope Scope, filter string, attrs []string, pageSize, limit int) ([]Entry, error) {
	f, err := compileFilter(filter)
	if err != nil {
		return nil, err
	}
	if pageSize <= 0 {
		pageSize = 500
	}
	var want []*element
	for _, a := range attrs {
		if a = strings.TrimSpace(a); a != "" {
			want = append(want, octets(a))
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Entry
	var cookie []byte
	paged := true
	for {
		req := cons(classApplication|3, octets(base), enumerated(int64(scope)), enumerated(0), integer(0), integer(0), boolean(false), f,
			sequence(want...))
		var controls []*element
		if paged {
			v := sequence(integer(int64(pageSize)), prim(tagOctetString, cookie)).encode()
			controls = append(controls, sequence(octets("1.2.840.113556.1.4.319"), boolean(false), prim(tagOctetString, v)))
		}
		id, err := c.send(req, controls...)
		if err != nil {
			return out, err
		}
		cookie = nil
	read:
		for {
			m, err := c.receive(id)
			if err != nil {
				return out, err
			}
			switch m.op.tag {
			case classApplication | constructed | 4:
				e, err := parseEntry(m.op)
				if err != nil {
					return out, err
				}
				out = append(out, e) // past the limit, the page is still read to its end: the connection stays usable
			case classApplication | constructed | 19: // a referral to another server: not followed
			case classApplication | constructed | 5:
				if err := resultErr(m.op); err != nil {
					if IsCode(err, SizeLimitExceeded) && len(out) > 0 {
						return out, nil
					}
					return out, err
				}
				for _, ctl := range m.controls {
					if len(ctl.children) >= 1 && string(ctl.children[0].value) == "1.2.840.113556.1.4.319" {
						val := ctl.children[len(ctl.children)-1]
						if inner, err := parseElement(tagSequence, innerBody(val.value), 0); err == nil && len(inner.children) == 2 {
							cookie = inner.children[1].value
						}
					}
				}
				break read
			default:
				return out, errors.New("ldap: an unexpected answer to a search")
			}
		}
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if len(cookie) == 0 {
			return out, nil
		}
	}
}

// innerBody strips a value's own tag and length (the control's value is itself a BER SEQUENCE).
func innerBody(b []byte) []byte {
	if len(b) < 2 {
		return nil
	}
	n := int(b[1])
	i := 2
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 4 || len(b) < 2+k {
			return nil
		}
		n = 0
		for _, c := range b[2 : 2+k] {
			n = n<<8 | int(c)
		}
		i = 2 + k
	}
	if i+n > len(b) {
		return nil
	}
	return b[i : i+n]
}

func parseEntry(op *element) (Entry, error) {
	if len(op.children) < 2 {
		return Entry{}, errors.New("ldap: a short entry")
	}
	e := Entry{DN: string(op.children[0].value), Attrs: map[string][]string{}}
	for _, a := range op.children[1].children {
		if len(a.children) < 2 {
			continue
		}
		name := string(a.children[0].value)
		for _, v := range a.children[1].children {
			e.Attrs[name] = append(e.Attrs[name], string(v.value))
		}
	}
	return e, nil
}
