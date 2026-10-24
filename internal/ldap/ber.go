// Package ldap is a small LDAPv3 client (RFC 4511) for reading a company directory: Active Directory,
// OpenLDAP and the rest. It binds with a password, searches with paged results (as Active Directory
// needs past 1,000 entries), and secures the connection with LDAPS or StartTLS through Go's crypto/tls.
// It writes nothing to the directory.
package ldap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// BER (X.690) as LDAP uses it: definite lengths, low tag numbers.

const (
	classUniversal   = 0x00
	classApplication = 0x40
	classContext     = 0x80
	constructed      = 0x20

	tagBoolean     = 0x01
	tagInteger     = 0x02
	tagOctetString = 0x04
	tagNull        = 0x05
	tagEnumerated  = 0x0a
	tagSequence    = 0x30 // universal 16, constructed
	tagSet         = 0x31 // universal 17, constructed
)

// element is one BER value: its whole tag byte, and its contents (or its children, when constructed).
type element struct {
	tag      byte
	value    []byte
	children []*element
}

func (e *element) constructed() bool { return e.tag&constructed != 0 }

func appendLength(b []byte, n int) []byte {
	switch {
	case n < 0x80:
		return append(b, byte(n))
	case n < 0x100:
		return append(b, 0x81, byte(n))
	case n < 0x10000:
		return append(b, 0x82, byte(n>>8), byte(n))
	case n < 0x1000000:
		return append(b, 0x83, byte(n>>16), byte(n>>8), byte(n))
	default:
		return append(b, 0x84, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}

// encode writes an element: a primitive's value, or its children one after another.
func (e *element) encode() []byte {
	body := e.value
	if e.constructed() {
		var buf []byte
		for _, c := range e.children {
			buf = append(buf, c.encode()...)
		}
		body = buf
	}
	out := appendLength([]byte{e.tag}, len(body))
	return append(out, body...)
}

func prim(tag byte, v []byte) *element { return &element{tag: tag, value: v} }
func cons(tag byte, kids ...*element) *element {
	return &element{tag: tag | constructed, children: kids}
}
func octets(s string) *element    { return prim(tagOctetString, []byte(s)) }
func integer(n int64) *element    { return prim(tagInteger, encodeInt(n)) }
func enumerated(n int64) *element { return prim(tagEnumerated, encodeInt(n)) }
func boolean(v bool) *element {
	return prim(tagBoolean, []byte{map[bool]byte{true: 0xff, false: 0}[v]})
}
func sequence(kids ...*element) *element { return &element{tag: tagSequence, children: kids} }

func encodeInt(n int64) []byte {
	var b []byte
	for {
		b = append([]byte{byte(n)}, b...)
		n >>= 8
		if (n == 0 && b[0]&0x80 == 0) || (n == -1 && b[0]&0x80 != 0) {
			return b
		}
	}
}

func decodeInt(b []byte) (int64, error) {
	if len(b) == 0 || len(b) > 8 {
		return 0, errors.New("ldap: a bad integer")
	}
	n := int64(int8(b[0]))
	for _, c := range b[1:] {
		n = n<<8 | int64(c)
	}
	return n, nil
}

// maxMessage bounds one message from the server (an entry with large attributes, say a photo).
const maxMessage = 16 << 20

// readElement reads one element from r.
func readElement(r io.Reader) (*element, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0]&0x1f == 0x1f {
		return nil, errors.New("ldap: high tag numbers are not used by LDAP")
	}
	n := int(hdr[1])
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 4 {
			return nil, errors.New("ldap: a bad length")
		}
		var lb [4]byte
		if _, err := io.ReadFull(r, lb[:k]); err != nil {
			return nil, err
		}
		n = 0
		for _, c := range lb[:k] {
			n = n<<8 | int(c)
		}
	}
	if n > maxMessage {
		return nil, fmt.Errorf("ldap: a message of %d bytes is too large", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return parseElement(hdr[0], body, 0)
}

// parseElement makes an element from its tag and contents, parsing children of constructed ones.
func parseElement(tag byte, body []byte, depth int) (*element, error) {
	e := &element{tag: tag, value: body}
	if tag&constructed == 0 {
		return e, nil
	}
	if depth > 30 {
		return nil, errors.New("ldap: values nested too deep")
	}
	r := bytes.NewReader(body)
	for r.Len() > 0 {
		var hdr [2]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, errors.New("ldap: a truncated value")
		}
		n := int(hdr[1])
		if n&0x80 != 0 {
			k := n & 0x7f
			if k == 0 || k > 4 {
				return nil, errors.New("ldap: a bad length")
			}
			var lb [4]byte
			if _, err := io.ReadFull(r, lb[:k]); err != nil {
				return nil, errors.New("ldap: a truncated length")
			}
			n = 0
			for _, c := range lb[:k] {
				n = n<<8 | int(c)
			}
		}
		if n > r.Len() {
			return nil, errors.New("ldap: a value runs past its container")
		}
		sub := make([]byte, n)
		_, _ = io.ReadFull(r, sub)
		c, err := parseElement(hdr[0], sub, depth+1)
		if err != nil {
			return nil, err
		}
		e.children = append(e.children, c)
	}
	return e, nil
}
