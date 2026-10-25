// Package mgcp is a call agent for MGCP gateways (RFC 3435): analogue telephone adapters and line
// cards whose ports BitVoice controls one by one. Each port is registered to the engine as one more
// device of its extension, through the same loopback bridge as browser phones, so it rings, is
// rung and transfers like any phone. The agent drives the line package (RFC 3660): dial tone, digit
// collection by digit map, ringing with caller ID, ringback, busy, and the connection commands that
// carry the audio (CRCX, MDCX, DLCX).
package mgcp

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Message is an MGCP command or response.
type Message struct {
	Verb     string // command: CRCX, MDCX, DLCX, RQNT, NTFY, AUEP, AUCX, RSIP, EPCF; "" for a response
	Code     int    // response
	TxID     string
	Endpoint string // command: local@domain
	Comment  string // response text
	Params   [][2]string
	SDP      []byte
}

// ErrMessage is something that is not an MGCP message.
var ErrMessage = errors.New("mgcp: not an MGCP message")

// Parse reads one datagram, which may hold several piggybacked messages separated by a line holding
// a single dot.
func Parse(b []byte) ([]*Message, error) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	var out []*Message
	for _, part := range bytes.Split(b, []byte("\n.\n")) {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		m, err := parseOne(part)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, ErrMessage
	}
	return out, nil
}

func parseOne(b []byte) (*Message, error) {
	head, body, _ := bytes.Cut(b, []byte("\n\n"))
	lines := strings.Split(string(head), "\n")
	f := strings.Fields(lines[0])
	if len(f) < 2 {
		return nil, ErrMessage
	}
	m := &Message{}
	if code, err := strconv.Atoi(f[0]); err == nil {
		if code < 0 || code > 999 {
			return nil, ErrMessage
		}
		m.Code, m.TxID = code, f[1]
		if len(f) > 2 {
			m.Comment = strings.Join(f[2:], " ")
		}
	} else {
		if len(f) < 4 || !strings.EqualFold(f[3], "MGCP") {
			return nil, ErrMessage
		}
		m.Verb, m.TxID, m.Endpoint = strings.ToUpper(f[0]), f[1], f[2]
	}
	if _, err := strconv.ParseUint(m.TxID, 10, 32); err != nil {
		return nil, ErrMessage
	}
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		m.Params = append(m.Params, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	if len(bytes.TrimSpace(body)) > 0 {
		m.SDP = append([]byte(nil), body...)
	}
	return m, nil
}

// Get is a parameter's value ("" when absent); names are case-insensitive.
func (m *Message) Get(name string) string {
	for _, p := range m.Params {
		if strings.EqualFold(p[0], name) {
			return p[1]
		}
	}
	return ""
}

// Has reports whether a parameter is present, even with an empty value (ResponseAck "K:" is).
func (m *Message) Has(name string) bool {
	for _, p := range m.Params {
		if strings.EqualFold(p[0], name) {
			return true
		}
	}
	return false
}

// Set adds or replaces a parameter.
func (m *Message) Set(name, value string) {
	for i, p := range m.Params {
		if strings.EqualFold(p[0], name) {
			m.Params[i][1] = value
			return
		}
	}
	m.Params = append(m.Params, [2]string{name, value})
}

// Bytes is the message on the wire.
func (m *Message) Bytes() []byte {
	var b bytes.Buffer
	if m.Verb != "" {
		fmt.Fprintf(&b, "%s %s %s MGCP 1.0\r\n", m.Verb, m.TxID, m.Endpoint)
	} else {
		c := m.Comment
		if c == "" {
			c = reason(m.Code)
		}
		fmt.Fprintf(&b, "%03d %s %s\r\n", m.Code, m.TxID, c)
	}
	for _, p := range m.Params {
		fmt.Fprintf(&b, "%s: %s\r\n", p[0], p[1])
	}
	if len(m.SDP) > 0 {
		b.WriteString("\r\n")
		b.Write(m.SDP)
	}
	return b.Bytes()
}

func reason(code int) string {
	switch {
	case code == 100:
		return "Pending"
	case code == 200:
		return "OK"
	case code == 250:
		return "Connection deleted"
	case code >= 500:
		return "Error"
	case code >= 400:
		return "Transient error"
	}
	return "OK"
}

// Events splits an ObservedEvents value into event names without their package ("L/hd" and "hd"
// are both "hd"; "D/1" is "1"), keeping parameters ("oc(...)") out.
func Events(v string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i <= len(v); i++ {
		if i < len(v) {
			switch v[i] {
			case '(':
				depth++
				continue
			case ')':
				depth--
				continue
			case ',':
				if depth > 0 {
					continue
				}
			default:
				continue
			}
		}
		e := strings.TrimSpace(v[start:i])
		start = i + 1
		if p := strings.IndexByte(e, '('); p >= 0 {
			e = e[:p]
		}
		if p := strings.LastIndexByte(e, '/'); p >= 0 {
			e = e[p+1:]
		}
		if p := strings.IndexByte(e, '@'); p >= 0 {
			e = e[:p]
		}
		if e != "" {
			out = append(out, strings.ToLower(e))
		}
	}
	return out
}
