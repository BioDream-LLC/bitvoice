// Package sip implements the parts of SIP (RFC 3261) BitVoice needs: message parsing and writing, URIs and
// addresses, Via handling, digest authentication, transports and transactions.
//
// Parsing is liberal and writing is strict. Real phones and carriers send compact header names, odd
// capitalisation, folded lines and stray whitespace; all of that is accepted. What BitVoice sends is always
// canonical, with a Content-Length that matches the body.
package sip

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Methods BitVoice understands.
const (
	INVITE    = "INVITE"
	ACK       = "ACK"
	BYE       = "BYE"
	CANCEL    = "CANCEL"
	REGISTER  = "REGISTER"
	OPTIONS   = "OPTIONS"
	INFO      = "INFO"
	UPDATE    = "UPDATE"
	REFER     = "REFER"
	NOTIFY    = "NOTIFY"
	SUBSCRIBE = "SUBSCRIBE"
	MESSAGE   = "MESSAGE"
	PRACK     = "PRACK"
)

// MaxMessageSize bounds what the parser accepts. A UDP datagram cannot be larger, and a TCP peer sending more
// than this in one message is either broken or hostile.
const MaxMessageSize = 65535

// Header is one header field. Order is kept because Via and Record-Route order carries meaning.
type Header struct {
	Name  string
	Value string
}

// Message is a SIP request or response.
type Message struct {
	// Request line, set when IsRequest.
	Method     string
	RequestURI string

	// Status line, set when !IsRequest.
	StatusCode int
	Reason     string

	IsRequest bool
	Headers   []Header
	Body      []byte
}

// compact maps RFC 3261 section 7.3.3 compact forms (and RFC 3515 / 4028 ones) to full names.
var compact = map[string]string{
	"i": "Call-ID", "m": "Contact", "e": "Content-Encoding", "l": "Content-Length",
	"c": "Content-Type", "f": "From", "s": "Subject", "k": "Supported", "t": "To",
	"v": "Via", "r": "Refer-To", "b": "Referred-By", "x": "Session-Expires", "o": "Event",
	"u": "Allow-Events",
}

// canonical spellings for headers whose usual form is not simple title case.
var canonical = map[string]string{
	"call-id": "Call-ID", "cseq": "CSeq", "www-authenticate": "WWW-Authenticate",
	"mime-version": "MIME-Version", "rack": "RAck", "rseq": "RSeq", "sip-etag": "SIP-ETag",
	"sip-if-match": "SIP-If-Match", "p-asserted-identity": "P-Asserted-Identity",
	"p-preferred-identity": "P-Preferred-Identity",
}

// CanonicalName returns the full, canonically capitalised form of a header name.
func CanonicalName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) == 1 {
		if full, ok := compact[strings.ToLower(name)]; ok {
			return full
		}
	}
	lower := strings.ToLower(name)
	if c, ok := canonical[lower]; ok {
		return c
	}
	parts := strings.Split(lower, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}

// multiValue headers may carry several comma-separated values in one line; they are split on parse so
// callers see one value per element. Headers whose values can contain commas inside quotes or angle
// brackets are split with splitList, which respects both.
var multiValue = map[string]bool{
	"Via": true, "Contact": true, "Route": true, "Record-Route": true, "Allow": true,
	"Supported": true, "Require": true, "Proxy-Require": true, "Unsupported": true, "Allow-Events": true,
}

// ErrIncomplete means the buffer holds the start of a message but not all of it (stream transports).
var ErrIncomplete = errors.New("sip: incomplete message")

// Parse parses one complete SIP message. For stream transports use ParseStream, which reports how many bytes
// were consumed.
func Parse(data []byte) (*Message, error) {
	m, _, err := parse(data, false)
	return m, err
}

// ParseStream parses the first message in a stream buffer and returns how many bytes it used. It returns
// ErrIncomplete when more data is needed. Leading CRLF keep-alives (RFC 5626) are skipped.
func ParseStream(data []byte) (*Message, int, error) {
	return parse(data, true)
}

func parse(data []byte, stream bool) (*Message, int, error) {
	skipped := 0
	for len(data) > 0 && (data[0] == '\r' || data[0] == '\n') {
		data = data[1:]
		skipped++
	}
	if len(data) == 0 {
		if stream {
			return nil, skipped, ErrIncomplete
		}
		return nil, 0, errors.New("sip: empty message")
	}
	end := bytes.Index(data, []byte("\r\n\r\n"))
	sepLen := 4
	if end < 0 {
		// Some broken senders use bare LF.
		if e := bytes.Index(data, []byte("\n\n")); e >= 0 {
			end, sepLen = e, 2
		}
	}
	if end < 0 {
		if stream && len(data) < MaxMessageSize {
			return nil, 0, ErrIncomplete
		}
		if !stream {
			// A datagram with headers but no blank line: accept it as headers only.
			end, sepLen = len(data), 0
		} else {
			return nil, 0, errors.New("sip: header section too large")
		}
	}
	head := string(data[:end])
	head = strings.ReplaceAll(head, "\r\n", "\n")
	lines := strings.Split(head, "\n")

	m := &Message{}
	if err := m.parseStartLine(lines[0]); err != nil {
		return nil, 0, err
	}

	// Unfold continuation lines (RFC 3261 7.3.1).
	var unfolded []string
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += " " + strings.TrimSpace(line)
			continue
		}
		unfolded = append(unfolded, line)
	}
	for _, line := range unfolded {
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, 0, fmt.Errorf("sip: malformed header line %q", line)
		}
		rawName := strings.TrimSpace(line[:colon])
		if !isToken(rawName) {
			return nil, 0, fmt.Errorf("sip: bad header name %q", rawName)
		}
		name := CanonicalName(rawName)
		value := strings.TrimSpace(line[colon+1:])
		if multiValue[name] {
			for _, v := range splitList(value) {
				m.Headers = append(m.Headers, Header{name, v})
			}
			continue
		}
		m.Headers = append(m.Headers, Header{name, value})
	}

	bodyStart := end + sepLen
	rest := data[bodyStart:]
	if cl := m.Get("Content-Length"); cl != "" {
		n, err := strconv.Atoi(strings.TrimSpace(cl))
		if err != nil || n < 0 {
			return nil, 0, fmt.Errorf("sip: bad Content-Length %q", cl)
		}
		if n > len(rest) {
			if stream {
				return nil, 0, ErrIncomplete
			}
			// A datagram that is shorter than it claims: RFC 3261 18.3 says discard it.
			return nil, 0, errors.New("sip: body shorter than Content-Length")
		}
		m.Body = append([]byte(nil), rest[:n]...)
		return m, skipped + bodyStart + n, nil
	}
	if stream {
		// Content-Length is mandatory on stream transports; without it the body is empty.
		return m, skipped + bodyStart, nil
	}
	if len(rest) > 0 {
		m.Body = append([]byte(nil), rest...)
	}
	return m, skipped + len(data), nil
}

func (m *Message) parseStartLine(line string) error {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "SIP/") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 || !strings.EqualFold(parts[0], "SIP/2.0") {
			return fmt.Errorf("sip: bad status line %q", line)
		}
		code, err := strconv.Atoi(parts[1])
		if err != nil || code < 100 || code > 699 {
			return fmt.Errorf("sip: bad status code in %q", line)
		}
		m.StatusCode = code
		if len(parts) == 3 {
			m.Reason = parts[2]
		}
		return nil
	}
	parts := strings.Fields(line)
	if len(parts) != 3 || !strings.EqualFold(parts[2], "SIP/2.0") {
		return fmt.Errorf("sip: bad request line %q", line)
	}
	for _, r := range parts[0] {
		if r < 'A' || r > 'Z' {
			if r != '-' && r != '_' {
				return fmt.Errorf("sip: bad method %q", parts[0])
			}
		}
	}
	m.IsRequest = true
	m.Method = parts[0]
	m.RequestURI = parts[1]
	return nil
}

// isToken reports whether s is a non-empty RFC 3261 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("-.!%*_+`'~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// splitList splits a comma-separated header value, ignoring commas inside quotes and angle brackets.
func splitList(v string) []string {
	var out []string
	depth, quoted, start := 0, false, 0
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c == '\\' && quoted:
			i++
		case c == '"':
			quoted = !quoted
		case c == '<' && !quoted:
			depth++
		case c == '>' && !quoted && depth > 0:
			depth--
		case c == ',' && !quoted && depth == 0:
			if s := strings.TrimSpace(v[start:i]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(v[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// Get returns the first value of a header, or "".
func (m *Message) Get(name string) string {
	name = CanonicalName(name)
	for _, h := range m.Headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

// GetAll returns every value of a header in order.
func (m *Message) GetAll(name string) []string {
	name = CanonicalName(name)
	var out []string
	for _, h := range m.Headers {
		if h.Name == name {
			out = append(out, h.Value)
		}
	}
	return out
}

// Set replaces every value of a header with one value, keeping the position of the first.
func (m *Message) Set(name, value string) {
	name = CanonicalName(name)
	for i, h := range m.Headers {
		if h.Name == name {
			m.Headers[i].Value = value
			m.removeFrom(name, i+1)
			return
		}
	}
	m.Headers = append(m.Headers, Header{name, value})
}

// Add appends a header value.
func (m *Message) Add(name, value string) {
	m.Headers = append(m.Headers, Header{CanonicalName(name), value})
}

// Prepend inserts a header value before any existing value of the same header (used for Via and Record-Route).
func (m *Message) Prepend(name, value string) {
	name = CanonicalName(name)
	for i, h := range m.Headers {
		if h.Name == name {
			m.Headers = append(m.Headers[:i], append([]Header{{name, value}}, m.Headers[i:]...)...)
			return
		}
	}
	m.Headers = append([]Header{{name, value}}, m.Headers...)
}

// Del removes every value of a header.
func (m *Message) Del(name string) {
	m.removeFrom(CanonicalName(name), 0)
}

// RemoveFirst removes the first value of a header (used to pop the top Via).
func (m *Message) RemoveFirst(name string) {
	name = CanonicalName(name)
	for i, h := range m.Headers {
		if h.Name == name {
			m.Headers = append(m.Headers[:i], m.Headers[i+1:]...)
			return
		}
	}
}

func (m *Message) removeFrom(name string, from int) {
	out := m.Headers[:from]
	for _, h := range m.Headers[from:] {
		if h.Name != name {
			out = append(out, h)
		}
	}
	m.Headers = out
}

// Bytes writes the message in canonical form with a correct Content-Length.
func (m *Message) Bytes() []byte {
	var b bytes.Buffer
	if m.IsRequest {
		fmt.Fprintf(&b, "%s %s SIP/2.0\r\n", m.Method, m.RequestURI)
	} else {
		reason := m.Reason
		if reason == "" {
			reason = ReasonPhrase(m.StatusCode)
		}
		fmt.Fprintf(&b, "SIP/2.0 %d %s\r\n", m.StatusCode, reason)
	}
	for _, h := range m.Headers {
		if h.Name == "Content-Length" {
			continue
		}
		b.WriteString(h.Name)
		b.WriteString(": ")
		b.WriteString(h.Value)
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(m.Body))
	b.Write(m.Body)
	return b.Bytes()
}

func (m *Message) String() string { return string(m.Bytes()) }

// Clone returns a deep copy.
func (m *Message) Clone() *Message {
	c := *m
	c.Headers = append([]Header(nil), m.Headers...)
	c.Body = append([]byte(nil), m.Body...)
	return &c
}

// CSeq returns the sequence number and method of the CSeq header.
func (m *Message) CSeq() (uint32, string) {
	f := strings.Fields(m.Get("CSeq"))
	if len(f) != 2 {
		return 0, ""
	}
	n, err := strconv.ParseUint(f[0], 10, 32)
	if err != nil {
		return 0, ""
	}
	return uint32(n), strings.ToUpper(f[1])
}

// CallID returns the Call-ID.
func (m *Message) CallID() string { return m.Get("Call-ID") }

// Validate checks the headers every request and response must carry (RFC 3261 8.1.1).
func (m *Message) Validate() error {
	for _, h := range []string{"Via", "From", "To", "Call-ID", "CSeq"} {
		if m.Get(h) == "" {
			return fmt.Errorf("sip: missing %s header", h)
		}
	}
	_, method := m.CSeq()
	if method == "" {
		return fmt.Errorf("sip: bad CSeq %q", m.Get("CSeq"))
	}
	if m.IsRequest && method != m.Method && !(m.Method == ACK && method == INVITE) {
		return fmt.Errorf("sip: CSeq method %s does not match %s", method, m.Method)
	}
	return nil
}

// ReasonPhrase returns the standard reason phrase for a status code.
func ReasonPhrase(code int) string {
	if r, ok := reasons[code]; ok {
		return r
	}
	switch {
	case code < 200:
		return "Trying"
	case code < 300:
		return "OK"
	case code < 400:
		return "Redirection"
	case code < 500:
		return "Client Error"
	case code < 600:
		return "Server Error"
	}
	return "Global Failure"
}

var reasons = map[int]string{
	100: "Trying", 180: "Ringing", 181: "Call Is Being Forwarded", 182: "Queued", 183: "Session Progress",
	200: "OK", 202: "Accepted", 301: "Moved Permanently", 302: "Moved Temporarily",
	400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed",
	407: "Proxy Authentication Required", 408: "Request Timeout", 410: "Gone", 413: "Request Entity Too Large",
	415: "Unsupported Media Type", 416: "Unsupported URI Scheme", 420: "Bad Extension", 423: "Interval Too Brief",
	480: "Temporarily Unavailable", 481: "Call/Transaction Does Not Exist", 482: "Loop Detected",
	483: "Too Many Hops", 484: "Address Incomplete", 486: "Busy Here", 487: "Request Terminated",
	488: "Not Acceptable Here", 491: "Request Pending", 500: "Server Internal Error", 501: "Not Implemented",
	502: "Bad Gateway", 503: "Service Unavailable", 504: "Server Time-out", 505: "Version Not Supported",
	600: "Busy Everywhere", 603: "Decline", 604: "Does Not Exist Anywhere", 606: "Not Acceptable", 607: "Unwanted", 608: "Rejected",
}
