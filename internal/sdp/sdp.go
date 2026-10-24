// Package sdp parses and writes Session Description Protocol bodies (RFC 4566) and does the rewriting
// BitVoice needs to anchor media: changing connection addresses and ports while keeping everything else.
package sdp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Session is a parsed SDP body. Lines are kept in order so that writing it back changes only what was
// changed deliberately.
type Session struct {
	// Lines before the first m= line, as type and value ("v", "0").
	Lines []Line
	Media []*Media
}

// Line is one type=value line.
type Line struct {
	Type  byte
	Value string
}

// Media is one m= section.
type Media struct {
	Type    string // audio, video, text, application, image
	Port    int
	NPorts  int // 0 when absent
	Proto   string
	Formats []string
	Lines   []Line // lines after the m= line
}

// Parse parses an SDP body.
func Parse(b []byte) (*Session, error) {
	s := &Session{}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	var cur *Media
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, "\r ")
		if raw == "" {
			continue
		}
		if len(raw) < 2 || raw[1] != '=' {
			return nil, fmt.Errorf("sdp: bad line %q", raw)
		}
		l := Line{Type: raw[0], Value: raw[2:]}
		if l.Type == 'm' {
			m, err := parseMediaLine(l.Value)
			if err != nil {
				return nil, err
			}
			s.Media = append(s.Media, m)
			cur = m
			continue
		}
		if cur != nil {
			cur.Lines = append(cur.Lines, l)
		} else {
			s.Lines = append(s.Lines, l)
		}
	}
	if len(s.Lines) == 0 || s.Lines[0].Type != 'v' {
		return nil, errors.New("sdp: missing v= line")
	}
	return s, nil
}

func parseMediaLine(v string) (*Media, error) {
	f := strings.Fields(v)
	if len(f) < 3 {
		return nil, fmt.Errorf("sdp: bad m= line %q", v)
	}
	m := &Media{Type: f[0], Proto: f[2], Formats: f[3:]}
	port := f[1]
	if slash := strings.IndexByte(port, '/'); slash >= 0 {
		n, err := strconv.Atoi(port[slash+1:])
		if err != nil {
			return nil, fmt.Errorf("sdp: bad port count in %q", v)
		}
		m.NPorts = n
		port = port[:slash]
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return nil, fmt.Errorf("sdp: bad port in %q", v)
	}
	m.Port = p
	return m, nil
}

// Bytes writes the session with CRLF line endings.
func (s *Session) Bytes() []byte {
	var b strings.Builder
	for _, l := range s.Lines {
		writeLine(&b, l)
	}
	for _, m := range s.Media {
		port := strconv.Itoa(m.Port)
		if m.NPorts > 0 {
			port += "/" + strconv.Itoa(m.NPorts)
		}
		fmt.Fprintf(&b, "m=%s %s %s", m.Type, port, m.Proto)
		for _, f := range m.Formats {
			b.WriteByte(' ')
			b.WriteString(f)
		}
		b.WriteString("\r\n")
		for _, l := range m.Lines {
			writeLine(&b, l)
		}
	}
	return []byte(b.String())
}

func writeLine(b *strings.Builder, l Line) {
	b.WriteByte(l.Type)
	b.WriteByte('=')
	b.WriteString(l.Value)
	b.WriteString("\r\n")
}

// Clone deep-copies a session.
func (s *Session) Clone() *Session {
	c := &Session{Lines: append([]Line(nil), s.Lines...)}
	for _, m := range s.Media {
		mc := *m
		mc.Formats = append([]string(nil), m.Formats...)
		mc.Lines = append([]Line(nil), m.Lines...)
		c.Media = append(c.Media, &mc)
	}
	return c
}

func get(lines []Line, t byte) (string, int) {
	for i, l := range lines {
		if l.Type == t {
			return l.Value, i
		}
	}
	return "", -1
}

// Connection returns the connection address that applies to a media section (its own c= or the session's).
func (s *Session) Connection(m *Media) string {
	if v, i := get(m.Lines, 'c'); i >= 0 {
		return connAddr(v)
	}
	v, _ := get(s.Lines, 'c')
	return connAddr(v)
}

func connAddr(v string) string {
	f := strings.Fields(v)
	if len(f) != 3 {
		return ""
	}
	addr := f[2]
	if slash := strings.IndexByte(addr, '/'); slash >= 0 {
		addr = addr[:slash] // multicast TTL
	}
	return addr
}

// SetConnection sets the session-level connection address and removes media-level ones, so every media
// section uses addr.
func (s *Session) SetConnection(addr string) {
	family := "IP4"
	if strings.Contains(addr, ":") {
		family = "IP6"
	}
	value := "IN " + family + " " + addr
	if _, i := get(s.Lines, 'c'); i >= 0 {
		s.Lines[i].Value = value
	} else {
		// c= goes after s=, i=, u=, e=, p= and before t= (RFC 4566 order).
		at := len(s.Lines)
		for i, l := range s.Lines {
			if l.Type == 't' {
				at = i
				break
			}
		}
		s.Lines = append(s.Lines[:at], append([]Line{{Type: 'c', Value: value}}, s.Lines[at:]...)...)
	}
	for _, m := range s.Media {
		out := m.Lines[:0]
		for _, l := range m.Lines {
			if l.Type != 'c' {
				out = append(out, l)
			}
		}
		m.Lines = out
	}
	// Keep the origin's address consistent; some phones check it.
	if v, i := get(s.Lines, 'o'); i >= 0 {
		f := strings.Fields(v)
		if len(f) == 6 {
			f[4], f[5] = family, addr
			s.Lines[i].Value = strings.Join(f, " ")
		}
	}
}

// BumpVersion increments the origin's session version, needed whenever an offer changes in a re-INVITE.
func (s *Session) BumpVersion() {
	if v, i := get(s.Lines, 'o'); i >= 0 {
		f := strings.Fields(v)
		if len(f) == 6 {
			if n, err := strconv.ParseUint(f[2], 10, 64); err == nil {
				f[2] = strconv.FormatUint(n+1, 10)
				s.Lines[i].Value = strings.Join(f, " ")
			}
		}
	}
}

// Attr returns the value of the first a=name or a=name:value attribute in a media section, and whether it
// exists.
func (m *Media) Attr(name string) (string, bool) {
	for _, l := range m.Lines {
		if l.Type != 'a' {
			continue
		}
		if l.Value == name {
			return "", true
		}
		if strings.HasPrefix(l.Value, name+":") {
			return l.Value[len(name)+1:], true
		}
	}
	return "", false
}

// Attrs returns the values of every a=name:value attribute of a media section, in order.
func (m *Media) Attrs(name string) []string {
	var out []string
	for _, l := range m.Lines {
		if l.Type == 'a' && strings.HasPrefix(l.Value, name+":") {
			out = append(out, l.Value[len(name)+1:])
		}
	}
	return out
}

// SessionAttr returns a session-level a=name or a=name:value attribute, and whether it exists.
func (s *Session) SessionAttr(name string) (string, bool) {
	for _, l := range s.Lines {
		if l.Type != 'a' {
			continue
		}
		if l.Value == name {
			return "", true
		}
		if strings.HasPrefix(l.Value, name+":") {
			return l.Value[len(name)+1:], true
		}
	}
	return "", false
}

// Direction returns sendrecv, sendonly, recvonly or inactive for a media section, falling back to the session.
func (s *Session) Direction(m *Media) string {
	for _, d := range []string{"sendonly", "recvonly", "inactive", "sendrecv"} {
		if _, ok := m.Attr(d); ok {
			return d
		}
	}
	for _, l := range s.Lines {
		if l.Type == 'a' {
			switch l.Value {
			case "sendonly", "recvonly", "inactive", "sendrecv":
				return l.Value
			}
		}
	}
	return "sendrecv"
}

// Codec is one rtpmap entry.
type Codec struct {
	PT        int
	Name      string
	ClockRate int
	Channels  int
	// Fmtp is the codec's format parameters (octet-align=1;mode-set=...), from its a=fmtp line.
	Fmtp string
}

// Attributes are the rtpmap line for a codec and the fmtp line some need: G.729 without Annex B
// (BitVoice does not decode its silence frames), Opus as RFC 7587 describes it (48000/2 always,
// in-band FEC on).
func (c Codec) Attributes() []Line {
	name := c.Name
	rate := c.ClockRate
	if rate == 0 {
		rate = 8000
	}
	switch strings.ToUpper(name) {
	case "OPUS":
		return []Line{{'a', fmt.Sprintf("rtpmap:%d opus/48000/2", c.PT)}, {'a', fmt.Sprintf("fmtp:%d minptime=10;useinbandfec=1", c.PT)}}
	case "G729":
		return []Line{{'a', fmt.Sprintf("rtpmap:%d G729/8000", c.PT)}, {'a', fmt.Sprintf("fmtp:%d annexb=no", c.PT)}}
	case "G723":
		return []Line{{'a', fmt.Sprintf("rtpmap:%d G723/8000", c.PT)}}
	case "ILBC":
		// RFC 3952: BitVoice offers 20 ms frames; an answer keeps the mode offered.
		f := c.Fmtp
		if f == "" {
			f = "mode=20"
		}
		return []Line{{'a', fmt.Sprintf("rtpmap:%d iLBC/8000", c.PT)}, {'a', fmt.Sprintf("fmtp:%d %s", c.PT, f)}}
	case "SPEEX":
		// RFC 5574: narrowband only; an answer keeps the other side's fmtp (its mode=).
		l := []Line{{'a', fmt.Sprintf("rtpmap:%d speex/8000", c.PT)}}
		if c.Fmtp != "" {
			l = append(l, Line{'a', fmt.Sprintf("fmtp:%d %s", c.PT, c.Fmtp)})
		}
		return l
	case "AMR", "AMR-WB":
		// RFC 4867: octet-aligned unless the other side asked for bandwidth-efficient framing.
		f := c.Fmtp
		if f == "" {
			f = "octet-align=1"
		}
		r := 8000
		if strings.EqualFold(name, "AMR-WB") {
			r = 16000
		}
		return []Line{{'a', fmt.Sprintf("rtpmap:%d %s/%d", c.PT, strings.ToUpper(name), r)}, {'a', fmt.Sprintf("fmtp:%d %s", c.PT, f)}}
	}
	var l []Line
	if c.Channels > 1 {
		l = []Line{{'a', fmt.Sprintf("rtpmap:%d %s/%d/%d", c.PT, name, rate, c.Channels)}}
	} else {
		l = []Line{{'a', fmt.Sprintf("rtpmap:%d %s/%d", c.PT, name, rate)}}
	}
	if c.Fmtp != "" {
		// Codecs carried untouched keep their parameters: G.722.1's bitrate (RFC 5577), G.719's
		// (RFC 5404), Codec2's mode.
		l = append(l, Line{'a', fmt.Sprintf("fmtp:%d %s", c.PT, c.Fmtp)})
	}
	return l
}

// static payload types that need no rtpmap (RFC 3551).
var static = map[int]Codec{
	0: {PT: 0, Name: "PCMU", ClockRate: 8000, Channels: 1}, 3: {PT: 3, Name: "GSM", ClockRate: 8000, Channels: 1}, 8: {PT: 8, Name: "PCMA", ClockRate: 8000, Channels: 1}, 9: {PT: 9, Name: "G722", ClockRate: 8000, Channels: 1},
	18: {PT: 18, Name: "G729", ClockRate: 8000, Channels: 1}, 13: {PT: 13, Name: "CN", ClockRate: 8000, Channels: 1},
}

// Codecs returns the codecs a media section offers, in preference order.
func (m *Media) Codecs() []Codec {
	maps := map[int]Codec{}
	for _, l := range m.Lines {
		if l.Type != 'a' || !strings.HasPrefix(l.Value, "rtpmap:") {
			continue
		}
		f := strings.Fields(l.Value[len("rtpmap:"):])
		if len(f) != 2 {
			continue
		}
		pt, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		parts := strings.Split(f[1], "/")
		c := Codec{PT: pt, Name: parts[0], Channels: 1}
		if len(parts) > 1 {
			c.ClockRate, _ = strconv.Atoi(parts[1])
		}
		if len(parts) > 2 {
			c.Channels, _ = strconv.Atoi(parts[2])
		}
		if strings.EqualFold(c.Name, "speex") && c.ClockRate != 0 && c.ClockRate != 8000 {
			// Wideband Speex is another codec (Asterisk's speex16 and speex32); BitVoice has only narrowband.
			c.Name = "speex" + strconv.Itoa(c.ClockRate/1000)
		}
		maps[pt] = c
	}
	for _, l := range m.Lines {
		v, ok := strings.CutPrefix(l.Value, "fmtp:")
		if l.Type != 'a' || !ok {
			continue
		}
		ptS, params, _ := strings.Cut(v, " ")
		if pt, err := strconv.Atoi(ptS); err == nil {
			if c, ok := maps[pt]; ok {
				c.Fmtp = strings.TrimSpace(params)
				maps[pt] = c
			}
		}
	}
	var out []Codec
	for _, f := range m.Formats {
		pt, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		if c, ok := maps[pt]; ok {
			out = append(out, c)
		} else if c, ok := static[pt]; ok {
			out = append(out, c)
		}
	}
	return out
}

// Audio returns the first audio section, or nil.
func (s *Session) Audio() *Media {
	for _, m := range s.Media {
		if m.Type == "audio" {
			return m
		}
	}
	return nil
}

// New builds a minimal audio offer or answer.
func New(addr string, port int, sessionID uint64, codecs []Codec, telephoneEventPT int) *Session {
	family := "IP4"
	if strings.Contains(addr, ":") {
		family = "IP6"
	}
	id := strconv.FormatUint(sessionID, 10)
	s := &Session{Lines: []Line{
		{'v', "0"}, {'o', "BitVoice " + id + " " + id + " IN " + family + " " + addr}, {'s', "BitVoice"},
		{'c', "IN " + family + " " + addr}, {'t', "0 0"},
	}}
	m := &Media{Type: "audio", Port: port, Proto: "RTP/AVP"}
	for _, c := range codecs {
		m.Formats = append(m.Formats, strconv.Itoa(c.PT))
		m.Lines = append(m.Lines, c.Attributes()...)
	}
	if telephoneEventPT > 0 {
		m.Formats = append(m.Formats, strconv.Itoa(telephoneEventPT))
		m.Lines = append(m.Lines, Line{'a', fmt.Sprintf("rtpmap:%d telephone-event/8000", telephoneEventPT)},
			Line{'a', fmt.Sprintf("fmtp:%d 0-16", telephoneEventPT)})
	}
	m.Lines = append(m.Lines, Line{'a', "ptime:20"}, Line{'a', "sendrecv"})
	s.Media = []*Media{m}
	return s
}
