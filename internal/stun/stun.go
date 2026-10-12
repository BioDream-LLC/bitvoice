// Package stun implements the part of STUN (RFC 8489) an ICE-lite endpoint needs (RFC 8445 section 2.5):
// reading Binding requests, checking MESSAGE-INTEGRITY with the local ICE password, and answering with
// XOR-MAPPED-ADDRESS, MESSAGE-INTEGRITY and FINGERPRINT. HMAC-SHA1 and CRC-32 come from the standard library.
package stun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net/netip"
)

const (
	magicCookie = 0x2112A442
	headerLen   = 20

	BindingRequest  = 0x0001
	BindingSuccess  = 0x0101
	BindingError    = 0x0111
	attrUsername    = 0x0006
	attrIntegrity   = 0x0008
	attrErrorCode   = 0x0009
	attrXORMapped   = 0x0020
	attrPriority    = 0x0024
	attrUseCand     = 0x0025
	attrFingerprint = 0x8028
	attrControlling = 0x802A
	attrControlled  = 0x8029

	fingerprintXOR = 0x5354554e
)

// Errors from Parse and Check.
var (
	ErrNotSTUN   = errors.New("stun: not a STUN message")
	ErrIntegrity = errors.New("stun: message integrity check failed")
)

// IsSTUN reports whether a datagram looks like STUN (RFC 7983 demultiplexing: first byte 0-3, magic cookie).
func IsSTUN(b []byte) bool {
	return len(b) >= headerLen && b[0] < 4 && binary.BigEndian.Uint32(b[4:]) == magicCookie
}

// Message is a parsed STUN message.
type Message struct {
	Type          uint16
	TxID          [12]byte
	Attrs         []Attr
	raw           []byte
	intAt         int // offset of MESSAGE-INTEGRITY, -1 when absent
	fingerprintOK bool
}

// Attr is one attribute.
type Attr struct {
	Type  uint16
	Value []byte
}

// Parse reads a STUN message.
func Parse(b []byte) (*Message, error) {
	if !IsSTUN(b) {
		return nil, ErrNotSTUN
	}
	n := int(binary.BigEndian.Uint16(b[2:]))
	if n%4 != 0 || headerLen+n > len(b) {
		return nil, ErrNotSTUN
	}
	b = b[:headerLen+n]
	m := &Message{Type: binary.BigEndian.Uint16(b), raw: b, intAt: -1}
	copy(m.TxID[:], b[8:20])
	for p := headerLen; p+4 <= len(b); {
		t := binary.BigEndian.Uint16(b[p:])
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		if p+4+l > len(b) {
			return nil, ErrNotSTUN
		}
		if t == attrIntegrity && m.intAt < 0 {
			m.intAt = p
		}
		if t == attrFingerprint {
			want := binary.BigEndian.Uint32(b[p+4:])
			m.fingerprintOK = l == 4 && crc32.ChecksumIEEE(b[:p])^fingerprintXOR == want
		}
		m.Attrs = append(m.Attrs, Attr{Type: t, Value: b[p+4 : p+4+l]})
		p += 4 + (l+3)&^3
	}
	return m, nil
}

// Get returns an attribute's value.
func (m *Message) Get(t uint16) ([]byte, bool) {
	for _, a := range m.Attrs {
		if a.Type == t {
			return a.Value, true
		}
	}
	return nil, false
}

// Username returns the USERNAME attribute.
func (m *Message) Username() string {
	v, _ := m.Get(attrUsername)
	return string(v)
}

// UseCandidate reports whether the controlling agent nominated this pair.
func (m *Message) UseCandidate() bool {
	_, ok := m.Get(attrUseCand)
	return ok
}

// Check verifies MESSAGE-INTEGRITY (short-term credentials: the key is the password) and, if present,
// FINGERPRINT.
func (m *Message) Check(password string) error {
	if m.intAt < 0 {
		return ErrIntegrity
	}
	// The length field covers the message up to and including MESSAGE-INTEGRITY (RFC 8489 14.5).
	head := append([]byte(nil), m.raw[:m.intAt]...)
	binary.BigEndian.PutUint16(head[2:], uint16(m.intAt-headerLen+24))
	mac := hmac.New(sha1.New, []byte(password))
	mac.Write(head)
	if !hmac.Equal(mac.Sum(nil), m.raw[m.intAt+4:m.intAt+24]) {
		return ErrIntegrity
	}
	if _, has := m.Get(attrFingerprint); has && !m.fingerprintOK {
		return ErrIntegrity
	}
	return nil
}

// builder writes a message.
type builder struct{ b []byte }

func newBuilder(typ uint16, tx [12]byte) *builder {
	b := make([]byte, headerLen, 128)
	binary.BigEndian.PutUint16(b, typ)
	binary.BigEndian.PutUint32(b[4:], magicCookie)
	copy(b[8:], tx[:])
	return &builder{b: b}
}

func (w *builder) add(t uint16, v []byte) {
	w.b = binary.BigEndian.AppendUint16(w.b, t)
	w.b = binary.BigEndian.AppendUint16(w.b, uint16(len(v)))
	w.b = append(w.b, v...)
	for len(w.b)%4 != 0 {
		w.b = append(w.b, 0)
	}
	binary.BigEndian.PutUint16(w.b[2:], uint16(len(w.b)-headerLen))
}

func (w *builder) integrity(password string) {
	binary.BigEndian.PutUint16(w.b[2:], uint16(len(w.b)-headerLen+24))
	mac := hmac.New(sha1.New, []byte(password))
	mac.Write(w.b)
	w.add(attrIntegrity, mac.Sum(nil))
}

func (w *builder) fingerprint() {
	binary.BigEndian.PutUint16(w.b[2:], uint16(len(w.b)-headerLen+8))
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], crc32.ChecksumIEEE(w.b)^fingerprintXOR)
	w.add(attrFingerprint, v[:])
}

func xorAddress(addr netip.AddrPort, tx [12]byte) []byte {
	ip := addr.Addr().Unmap()
	v := []byte{0, 1}
	if ip.Is6() {
		v[1] = 2
	}
	v = binary.BigEndian.AppendUint16(v, addr.Port()^uint16(magicCookie>>16))
	key := make([]byte, 16)
	binary.BigEndian.PutUint32(key, magicCookie)
	copy(key[4:], tx[:])
	raw := ip.AsSlice()
	for i := range raw {
		raw[i] ^= key[i]
	}
	return append(v, raw...)
}

// BindingResponse answers a Binding request from addr, signed with the local ICE password.
func BindingResponse(req *Message, addr netip.AddrPort, password string) []byte {
	w := newBuilder(BindingSuccess, req.TxID)
	w.add(attrXORMapped, xorAddress(addr, req.TxID))
	w.integrity(password)
	w.fingerprint()
	return w.b
}

// BindingRequestFor builds a Binding request (for tests and for consent checks).
func BindingRequestFor(tx [12]byte, username, password string, priority uint32, useCandidate bool) []byte {
	w := newBuilder(BindingRequest, tx)
	w.add(attrUsername, []byte(username))
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], priority)
	w.add(attrPriority, p[:])
	w.add(attrControlling, make([]byte, 8))
	if useCandidate {
		w.add(attrUseCand, nil)
	}
	w.integrity(password)
	w.fingerprint()
	return w.b
}

// MappedAddress reads XOR-MAPPED-ADDRESS from a response.
func (m *Message) MappedAddress() (netip.AddrPort, bool) {
	v, ok := m.Get(attrXORMapped)
	if !ok || len(v) < 2 || !(v[1] == 1 && len(v) == 8 || v[1] == 2 && len(v) == 20) {
		return netip.AddrPort{}, false
	}
	port := binary.BigEndian.Uint16(v[2:]) ^ uint16(magicCookie>>16)
	key := make([]byte, 16)
	binary.BigEndian.PutUint32(key, magicCookie)
	copy(key[4:], m.TxID[:])
	raw := append([]byte(nil), v[4:]...)
	for i := range raw {
		raw[i] ^= key[i]
	}
	ip, ok := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(ip, port), ok
}
