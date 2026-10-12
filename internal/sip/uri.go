package sip

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// URI is a sip: or sips: URI (RFC 3261 19.1). Telephone URIs (tel:) are kept with Scheme "tel" and the number
// in User.
type URI struct {
	Scheme   string // sip, sips or tel
	User     string
	Password string
	Host     string
	Port     int // 0 when absent
	Params   Params
	Headers  string // raw ?headers part, kept as-is
}

// Params is an ordered list of ;name=value parameters. A parameter without a value has Value "" and Flag true.
type Params []Param

// Param is one URI or header parameter.
type Param struct {
	Name  string
	Value string
	Flag  bool
}

// Get returns a parameter's value and whether it is present. Names compare case-insensitively.
func (p Params) Get(name string) (string, bool) {
	for _, x := range p {
		if strings.EqualFold(x.Name, name) {
			return x.Value, true
		}
	}
	return "", false
}

// Set adds or replaces a parameter.
func (p *Params) Set(name, value string) {
	for i, x := range *p {
		if strings.EqualFold(x.Name, name) {
			(*p)[i] = Param{Name: x.Name, Value: value, Flag: value == ""}
			return
		}
	}
	*p = append(*p, Param{Name: name, Value: value, Flag: value == ""})
}

// Del removes a parameter.
func (p *Params) Del(name string) {
	out := (*p)[:0]
	for _, x := range *p {
		if !strings.EqualFold(x.Name, name) {
			out = append(out, x)
		}
	}
	*p = out
}

func (p Params) String() string {
	var b strings.Builder
	for _, x := range p {
		b.WriteByte(';')
		b.WriteString(x.Name)
		if !x.Flag || x.Value != "" {
			b.WriteByte('=')
			b.WriteString(x.Value)
		}
	}
	return b.String()
}

func parseParams(s string) Params {
	var out Params
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			out = append(out, Param{Name: strings.TrimSpace(part[:eq]), Value: strings.TrimSpace(part[eq+1:])})
		} else {
			out = append(out, Param{Name: part, Flag: true})
		}
	}
	return out
}

// ParseURI parses a SIP, SIPS or tel URI, or a URN (the To of an emergency call).
func ParseURI(s string) (*URI, error) {
	s = strings.TrimSpace(s)
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return nil, fmt.Errorf("sip: URI %q has no scheme", s)
	}
	u := &URI{Scheme: strings.ToLower(s[:colon])}
	rest := s[colon+1:]
	if u.Scheme == "urn" {
		u.User = rest // a service URN (RFC 5031), such as urn:service:sos: kept as written
		return u, nil
	}
	if u.Scheme == "tel" {
		if semi := strings.IndexByte(rest, ';'); semi >= 0 {
			u.Params = parseParams(rest[semi+1:])
			rest = rest[:semi]
		}
		u.User = rest
		return u, nil
	}
	if u.Scheme != "sip" && u.Scheme != "sips" {
		return nil, fmt.Errorf("sip: unsupported URI scheme %q", u.Scheme)
	}
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		u.Headers = rest[q+1:]
		rest = rest[:q]
	}
	// The user part may legally contain ';' (user parameters), so the host is found after the last '@'.
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		userinfo := rest[:at]
		rest = rest[at+1:]
		if c := strings.IndexByte(userinfo, ':'); c >= 0 {
			u.User, u.Password = userinfo[:c], userinfo[c+1:]
		} else {
			u.User = userinfo
		}
	}
	hostport := rest
	if semi := strings.IndexByte(rest, ';'); semi >= 0 {
		hostport = rest[:semi]
		u.Params = parseParams(rest[semi+1:])
	}
	host, port, err := splitHostPort(hostport)
	if err != nil {
		return nil, fmt.Errorf("sip: URI %q: %w", s, err)
	}
	u.Host, u.Port = host, port
	return u, nil
}

// splitHostPort splits host[:port], allowing bracketed IPv6 and no port.
func splitHostPort(s string) (string, int, error) {
	if s == "" {
		return "", 0, fmt.Errorf("empty host")
	}
	if s[0] == '[' {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", 0, fmt.Errorf("unterminated IPv6 address")
		}
		host := s[1:end]
		if end+1 < len(s) {
			if s[end+1] != ':' {
				return "", 0, fmt.Errorf("bad host %q", s)
			}
			p, err := strconv.Atoi(s[end+2:])
			if err != nil || p <= 0 || p > 65535 {
				return "", 0, fmt.Errorf("bad port in %q", s)
			}
			return host, p, nil
		}
		return host, 0, nil
	}
	if strings.Count(s, ":") > 1 {
		// Bare IPv6 without brackets: accept as host with no port.
		return s, 0, nil
	}
	if c := strings.IndexByte(s, ':'); c >= 0 {
		p, err := strconv.Atoi(s[c+1:])
		if err != nil || p <= 0 || p > 65535 {
			return "", 0, fmt.Errorf("bad port in %q", s)
		}
		return s[:c], p, nil
	}
	return s, 0, nil
}

// HostPort renders host[:port] with IPv6 brackets.
func HostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

func (u *URI) String() string {
	if u == nil {
		return ""
	}
	if u.Scheme == "tel" {
		return "tel:" + u.User + u.Params.String()
	}
	if u.Scheme == "urn" {
		return "urn:" + u.User
	}
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteByte(':')
	if u.User != "" {
		b.WriteString(u.User)
		if u.Password != "" {
			b.WriteByte(':')
			b.WriteString(u.Password)
		}
		b.WriteByte('@')
	}
	b.WriteString(HostPort(u.Host, u.Port))
	b.WriteString(u.Params.String())
	if u.Headers != "" {
		b.WriteByte('?')
		b.WriteString(u.Headers)
	}
	return b.String()
}

// Clone copies a URI.
func (u *URI) Clone() *URI {
	c := *u
	c.Params = append(Params(nil), u.Params...)
	return &c
}

// Transport returns the transport parameter in lower case, defaulting to udp (tls for sips).
func (u *URI) Transport() string {
	if t, ok := u.Params.Get("transport"); ok && t != "" {
		return strings.ToLower(t)
	}
	if u.Scheme == "sips" {
		return "tls"
	}
	return "udp"
}

// Address is a name-addr or addr-spec as used in From, To, Contact and similar headers.
type Address struct {
	Display string
	URI     *URI
	Params  Params // header parameters such as tag and expires
	Star    bool   // Contact: *
}

// ParseAddress parses `"Display" <sip:uri>;params`, `<sip:uri>;params` or `sip:uri;params`.
func ParseAddress(s string) (*Address, error) {
	s = strings.TrimSpace(s)
	if s == "*" {
		return &Address{Star: true}, nil
	}
	a := &Address{}
	var uriPart, rest string
	if lt := indexUnquoted(s, '<'); lt >= 0 {
		gt := strings.IndexByte(s[lt:], '>')
		if gt < 0 {
			return nil, fmt.Errorf("sip: unterminated < in %q", s)
		}
		a.Display = unquote(strings.TrimSpace(s[:lt]))
		uriPart = s[lt+1 : lt+gt]
		rest = s[lt+gt+1:]
	} else {
		// addr-spec: header parameters start at the first ';' (URI parameters cannot appear without <>).
		if semi := strings.IndexByte(s, ';'); semi >= 0 {
			uriPart, rest = s[:semi], s[semi:]
		} else {
			uriPart = s
		}
	}
	u, err := ParseURI(uriPart)
	if err != nil {
		return nil, err
	}
	a.URI = u
	a.Params = parseParams(strings.TrimPrefix(strings.TrimSpace(rest), ";"))
	return a, nil
}

func indexUnquoted(s string, c byte) int {
	quoted := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && quoted:
			i++
		case s[i] == '"':
			quoted = !quoted
		case s[i] == c && !quoted:
			return i
		}
	}
	return -1
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

func (a *Address) String() string {
	if a.Star {
		return "*"
	}
	var b strings.Builder
	if a.Display != "" {
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(a.Display, `\`, `\\`), `"`, `\"`))
		b.WriteString(`" `)
	}
	b.WriteByte('<')
	b.WriteString(a.URI.String())
	b.WriteByte('>')
	b.WriteString(a.Params.String())
	return b.String()
}

// Tag returns the tag parameter.
func (a *Address) Tag() string {
	v, _ := a.Params.Get("tag")
	return v
}

// Clone copies an address.
func (a *Address) Clone() *Address {
	c := *a
	if a.URI != nil {
		c.URI = a.URI.Clone()
	}
	c.Params = append(Params(nil), a.Params...)
	return &c
}

// Via is one Via header value (RFC 3261 20.42).
type Via struct {
	Transport string // UDP, TCP, TLS, WS, WSS
	Host      string
	Port      int
	Params    Params
}

// ParseVia parses one Via value.
func ParseVia(s string) (*Via, error) {
	s = strings.TrimSpace(s)
	sp := strings.IndexAny(s, " \t")
	if sp < 0 {
		return nil, fmt.Errorf("sip: bad Via %q", s)
	}
	proto := strings.Split(s[:sp], "/")
	if len(proto) != 3 || !strings.EqualFold(proto[0], "SIP") {
		return nil, fmt.Errorf("sip: bad Via protocol %q", s[:sp])
	}
	v := &Via{Transport: strings.ToUpper(proto[2])}
	rest := strings.TrimSpace(s[sp:])
	hostport := rest
	if semi := strings.IndexByte(rest, ';'); semi >= 0 {
		hostport = rest[:semi]
		v.Params = parseParams(rest[semi+1:])
	}
	host, port, err := splitHostPort(strings.TrimSpace(hostport))
	if err != nil {
		return nil, fmt.Errorf("sip: Via %q: %w", s, err)
	}
	v.Host, v.Port = host, port
	return v, nil
}

func (v *Via) String() string {
	return "SIP/2.0/" + v.Transport + " " + HostPort(v.Host, v.Port) + v.Params.String()
}

// Branch returns the branch parameter.
func (v *Via) Branch() string {
	b, _ := v.Params.Get("branch")
	return b
}

// MagicCookie starts every RFC 3261 branch.
const MagicCookie = "z9hG4bK"

// NewBranch returns a fresh RFC 3261 branch.
func NewBranch() string { return MagicCookie + randomHex(10) }

// NewTag returns a fresh From/To tag.
func NewTag() string { return randomHex(8) }

// NewCallID returns a fresh Call-ID.
func NewCallID() string { return randomHex(16) }

// randRead is the random source, a variable so tests can make it fail.
var randRead = rand.Read

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		panic("sip: system random source failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// IsIP reports whether host is a literal IP address.
func IsIP(host string) bool { return net.ParseIP(host) != nil }
