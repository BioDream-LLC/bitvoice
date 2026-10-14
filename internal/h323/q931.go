package h323

import (
	"encoding/binary"
	"errors"
	"io"
)

// Q.931 messages, as H.225.0 carries them in TPKT frames (RFC 1006) over TCP.

const (
	q931Alerting        = 0x01
	q931CallProceeding  = 0x02
	q931Setup           = 0x05
	q931Connect         = 0x07
	q931ReleaseComplete = 0x5a
	q931Facility        = 0x62

	ieBearer     = 0x04
	ieCause      = 0x08
	ieDisplay    = 0x28
	ieCalling    = 0x6c
	ieCalled     = 0x70
	ieUserUser   = 0x7e
	maxFrame     = 16384
	userUserH225 = 0x05 // the user-user protocol discriminator for H.225.0
	q931Protocol = 0x08
)

var errQ931 = errors.New("h323: bad Q.931 message")

type q931 struct {
	callRef  uint16
	fromDest bool // the flag of the call reference: sent by the side that did not start the call
	msgType  byte
	ies      map[byte][]byte
}

func readTPKT(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	if h[0] != 3 {
		return nil, errQ931
	}
	n := int(binary.BigEndian.Uint16(h[2:]))
	if n < 4 || n > maxFrame {
		return nil, errQ931
	}
	b := make([]byte, n-4)
	_, err := io.ReadFull(r, b)
	return b, err
}

func tpkt(b []byte) []byte {
	out := make([]byte, 4, 4+len(b))
	out[0] = 3
	binary.BigEndian.PutUint16(out[2:], uint16(4+len(b)))
	return append(out, b...)
}

func parseQ931(b []byte) (*q931, error) {
	if len(b) < 5 || b[0] != q931Protocol || b[1] != 2 {
		return nil, errQ931
	}
	m := &q931{callRef: binary.BigEndian.Uint16(b[2:]) & 0x7fff, fromDest: b[2]&0x80 != 0, msgType: b[4], ies: map[byte][]byte{}}
	for i := 5; i < len(b); {
		id := b[i]
		i++
		if id&0x80 != 0 {
			continue // a single-octet element
		}
		var n int
		if id == ieUserUser {
			if i+2 > len(b) {
				return nil, errQ931
			}
			n = int(binary.BigEndian.Uint16(b[i:]))
			i += 2
		} else {
			if i >= len(b) {
				return nil, errQ931
			}
			n = int(b[i])
			i++
		}
		if i+n > len(b) {
			return nil, errQ931
		}
		m.ies[id] = b[i : i+n]
		i += n
	}
	return m, nil
}

func (m *q931) bytes() []byte {
	ref := m.callRef & 0x7fff
	if m.fromDest {
		ref |= 0x8000
	}
	b := []byte{q931Protocol, 2, byte(ref >> 8), byte(ref), m.msgType}
	for _, id := range []byte{ieBearer, ieCause, ieDisplay, ieCalling, ieCalled, ieUserUser} {
		v, ok := m.ies[id]
		if !ok {
			continue
		}
		if id == ieUserUser {
			b = append(b, id, byte(len(v)>>8), byte(len(v)))
		} else {
			if len(v) > 255 {
				v = v[:255]
			}
			b = append(b, id, byte(len(v)))
		}
		b = append(b, v...)
	}
	return b
}

// number reads a party number element: type and plan, maybe presentation, then the digits.
func number(v []byte) string {
	if len(v) == 0 {
		return ""
	}
	i := 1
	if v[0]&0x80 == 0 && len(v) > 1 {
		i = 2 // octet 3a follows
	}
	out := make([]byte, 0, len(v))
	for _, c := range v[i:] {
		if c >= '0' && c <= '9' || c == '*' || c == '#' || c == '+' {
			out = append(out, c)
		}
	}
	return string(out)
}

func numberIE(digits string) []byte {
	return append([]byte{0x81}, digits...) // unknown type, ISDN/telephony plan
}

// uu is the H.225.0 part of a message.
func (m *q931) uu() (*uuie, error) {
	v := m.ies[ieUserUser]
	if len(v) < 2 || v[0] != userUserH225 {
		return nil, errQ931
	}
	return decodeUU(v[1:])
}

func newQ931(msgType byte, callRef uint16, fromDest bool, u *uuie) *q931 {
	m := &q931{callRef: callRef, fromDest: fromDest, msgType: msgType, ies: map[byte][]byte{}}
	if u != nil {
		m.ies[ieUserUser] = append([]byte{userUserH225}, encodeUU(u)...)
	}
	return m
}

// RawSetup frames a Setup with the given H.225.0 user information (for tests).
func RawSetup(callRef uint16, uu []byte) []byte {
	m := &q931{callRef: callRef, msgType: q931Setup, ies: map[byte][]byte{ieBearer: {0x88, 0x90, 0xa5}, ieUserUser: append([]byte{userUserH225}, uu...)}}
	return tpkt(m.bytes())
}

// ReadMessage reads one framed Q.931 message and says its type and whether it carries Fast Connect
// channels (for tests).
func ReadMessage(r io.Reader) (msgType byte, fast int, err error) {
	b, err := readTPKT(r)
	if err != nil {
		return 0, 0, err
	}
	m, err := parseQ931(b)
	if err != nil {
		return 0, 0, err
	}
	if u, err := m.uu(); err == nil {
		fast = len(u.fastStart)
	}
	return m.msgType, fast, nil
}

// Q.931 message types, for tests.
const (
	MsgAlerting        = q931Alerting
	MsgCallProceeding  = q931CallProceeding
	MsgConnect         = q931Connect
	MsgReleaseComplete = q931ReleaseComplete
)
