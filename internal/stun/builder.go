package stun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"net/netip"
)

// Builder writes any STUN message, for TURN (RFC 8656).
type Builder struct{ w *builder }

// NewBuilder starts a message of a type with a transaction ID.
func NewBuilder(typ uint16, tx [12]byte) *Builder { return &Builder{newBuilder(typ, tx)} }

// Add appends an attribute.
func (b *Builder) Add(t uint16, v []byte) { b.w.add(t, v) }

// AddXORAddress appends an XOR-encoded address attribute (XOR-MAPPED, -RELAYED or -PEER-ADDRESS).
func (b *Builder) AddXORAddress(t uint16, addr netip.AddrPort) {
	var tx [12]byte
	copy(tx[:], b.w.b[8:20])
	b.w.add(t, xorAddress(addr, tx))
}

// Integrity appends MESSAGE-INTEGRITY with a long-term key (RFC 8489 9.2.2).
func (b *Builder) Integrity(key []byte) {
	w := b.w
	binary.BigEndian.PutUint16(w.b[2:], uint16(len(w.b)-headerLen+24))
	mac := hmac.New(sha1.New, key)
	mac.Write(w.b)
	w.add(attrIntegrity, mac.Sum(nil))
}

// Fingerprint appends FINGERPRINT; it must come last.
func (b *Builder) Fingerprint() { b.w.fingerprint() }

// Bytes is the message.
func (b *Builder) Bytes() []byte { return b.w.b }

// CheckKey verifies MESSAGE-INTEGRITY with a long-term key, and FINGERPRINT if present.
func (m *Message) CheckKey(key []byte) error {
	if m.intAt < 0 {
		return ErrIntegrity
	}
	head := append([]byte(nil), m.raw[:m.intAt]...)
	binary.BigEndian.PutUint16(head[2:], uint16(m.intAt-headerLen+24))
	mac := hmac.New(sha1.New, key)
	mac.Write(head)
	if !hmac.Equal(mac.Sum(nil), m.raw[m.intAt+4:m.intAt+24]) {
		return ErrIntegrity
	}
	if _, has := m.Get(attrFingerprint); has && !m.fingerprintOK {
		return ErrIntegrity
	}
	return nil
}

// HasIntegrity says whether the message carries MESSAGE-INTEGRITY.
func (m *Message) HasIntegrity() bool { return m.intAt >= 0 }

// XORAddress reads an XOR-encoded address attribute.
func (m *Message) XORAddress(t uint16) (netip.AddrPort, bool) {
	v, ok := m.Get(t)
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

// Length is the size of the message at the start of b, header included, or 0 when b does not
// start with a whole STUN header (for streams).
func Length(b []byte) int {
	if len(b) < headerLen {
		return 0
	}
	return headerLen + int(binary.BigEndian.Uint16(b[2:]))
}
