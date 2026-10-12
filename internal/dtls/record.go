// Package dtls implements the server side of DTLS 1.2 (RFC 6347) that WebRTC needs for DTLS-SRTP
// (RFC 5764): one cipher suite, TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, X25519 or P-256 key exchange,
// a client certificate checked against the SDP fingerprint, the extended master secret (RFC 7627), and
// export of the SRTP keys. Every primitive is Go's standard library; this package is the protocol around
// them.
package dtls

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Content types and handshake message types.
const (
	ctChangeCipherSpec = 20
	ctAlert            = 21
	ctHandshake        = 22
	ctApplicationData  = 23

	hsClientHello        = 1
	hsServerHello        = 2
	hsCertificate        = 11
	hsServerKeyExchange  = 12
	hsCertificateRequest = 13
	hsServerHelloDone    = 14
	hsCertificateVerify  = 15
	hsClientKeyExchange  = 16
	hsFinished           = 20

	suiteECDHE_ECDSA_AES128_GCM_SHA256 = 0xC02B

	versionDTLS12 = 0xFEFD
	versionDTLS10 = 0xFEFF

	recordHeader    = 13
	handshakeHeader = 12
)

var errShort = errors.New("dtls: short message")

// prf is the TLS 1.2 PRF with SHA-256 (RFC 5246 section 5).
func prf(secret []byte, label string, seed []byte, n int) []byte {
	ls := append([]byte(label), seed...)
	out := make([]byte, 0, n+sha256.Size)
	mac := hmac.New(sha256.New, secret)
	mac.Write(ls)
	a := mac.Sum(nil)
	for len(out) < n {
		mac.Reset()
		mac.Write(a)
		mac.Write(ls)
		out = append(out, mac.Sum(nil)...)
		mac.Reset()
		mac.Write(a)
		a = mac.Sum(nil)
	}
	return out[:n]
}

// record is one DTLS record.
type record struct {
	typ   byte
	epoch uint16
	seq   uint64 // 48 bits
	body  []byte
}

// parseRecords splits a datagram into records.
func parseRecords(b []byte) ([]record, error) {
	var out []record
	for len(b) > 0 {
		if len(b) < recordHeader {
			return out, errShort
		}
		n := int(binary.BigEndian.Uint16(b[11:]))
		if len(b) < recordHeader+n {
			return out, errShort
		}
		r := record{typ: b[0], epoch: binary.BigEndian.Uint16(b[3:]), body: b[recordHeader : recordHeader+n]}
		r.seq = uint64(binary.BigEndian.Uint16(b[5:]))<<32 | uint64(binary.BigEndian.Uint32(b[7:]))
		out = append(out, r)
		b = b[recordHeader+n:]
	}
	return out, nil
}

func writeRecordHeader(b []byte, typ byte, epoch uint16, seq uint64, n int) []byte {
	b = append(b, typ)
	b = binary.BigEndian.AppendUint16(b, versionDTLS12)
	b = binary.BigEndian.AppendUint16(b, epoch)
	b = binary.BigEndian.AppendUint16(b, uint16(seq>>32))
	b = binary.BigEndian.AppendUint32(b, uint32(seq))
	return binary.BigEndian.AppendUint16(b, uint16(n))
}

// gcmState protects records of one direction once ChangeCipherSpec has been sent or received.
type gcmState struct {
	aead cipher.AEAD
	salt []byte // the 4-byte implicit nonce
}

func newGCM(key, salt []byte) (*gcmState, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &gcmState{aead: a, salt: salt}, nil
}

func additional(typ byte, epoch uint16, seq uint64, n int) []byte {
	ad := make([]byte, 0, 13)
	ad = binary.BigEndian.AppendUint16(ad, epoch)
	ad = binary.BigEndian.AppendUint16(ad, uint16(seq>>32))
	ad = binary.BigEndian.AppendUint32(ad, uint32(seq))
	ad = append(ad, typ)
	ad = binary.BigEndian.AppendUint16(ad, versionDTLS12)
	return binary.BigEndian.AppendUint16(ad, uint16(n))
}

// seal returns a whole protected record. The explicit nonce is the epoch and sequence number, which never
// repeat under one key.
func (g *gcmState) seal(typ byte, epoch uint16, seq uint64, plain []byte) []byte {
	explicit := make([]byte, 8)
	binary.BigEndian.PutUint16(explicit, epoch)
	binary.BigEndian.PutUint16(explicit[2:], uint16(seq>>32))
	binary.BigEndian.PutUint32(explicit[4:], uint32(seq))
	nonce := append(append([]byte(nil), g.salt...), explicit...)
	body := g.aead.Seal(append([]byte(nil), explicit...), nonce, plain, additional(typ, epoch, seq, len(plain)))
	out := writeRecordHeader(make([]byte, 0, recordHeader+len(body)), typ, epoch, seq, len(body))
	return append(out, body...)
}

func (g *gcmState) open(r record) ([]byte, error) {
	if len(r.body) < 8+g.aead.Overhead() {
		return nil, errShort
	}
	nonce := append(append([]byte(nil), g.salt...), r.body[:8]...)
	n := len(r.body) - 8 - g.aead.Overhead()
	return g.aead.Open(nil, nonce, r.body[8:], additional(r.typ, r.epoch, r.seq, n))
}

// handshakeMsg is one handshake message, reassembled.
type handshakeMsg struct {
	typ  byte
	seq  uint16
	body []byte
}

// marshal writes the message unfragmented, which is also the form the transcript hash uses.
func (m handshakeMsg) marshal() []byte {
	b := make([]byte, 0, handshakeHeader+len(m.body))
	b = append(b, m.typ)
	b = append(b, byte(len(m.body)>>16), byte(len(m.body)>>8), byte(len(m.body)))
	b = binary.BigEndian.AppendUint16(b, m.seq)
	b = append(b, 0, 0, 0)
	b = append(b, byte(len(m.body)>>16), byte(len(m.body)>>8), byte(len(m.body)))
	return append(b, m.body...)
}

// reassembler collects fragments of handshake messages.
type reassembler struct {
	parts map[uint16]*partial
}

type partial struct {
	typ  byte
	body []byte
	have []bool
	got  int
}

func u24(b []byte) int { return int(b[0])<<16 | int(b[1])<<8 | int(b[2]) }

// add takes one handshake record's fragments and returns the messages it completed.
func (r *reassembler) add(b []byte) ([]handshakeMsg, error) {
	if r.parts == nil {
		r.parts = map[uint16]*partial{}
	}
	var done []handshakeMsg
	for len(b) > 0 {
		if len(b) < handshakeHeader {
			return done, errShort
		}
		typ, total := b[0], u24(b[1:])
		seq := binary.BigEndian.Uint16(b[4:])
		off, flen := u24(b[6:]), u24(b[9:])
		if total > 1<<16 || off+flen > total || len(b) < handshakeHeader+flen {
			return done, errShort
		}
		frag := b[handshakeHeader : handshakeHeader+flen]
		b = b[handshakeHeader+flen:]
		p := r.parts[seq]
		if p == nil {
			p = &partial{typ: typ, body: make([]byte, total), have: make([]bool, total)}
			r.parts[seq] = p
		}
		if len(p.body) != total || p.typ != typ {
			return done, errors.New("dtls: inconsistent fragments")
		}
		for i := range flen {
			if !p.have[off+i] {
				p.have[off+i] = true
				p.body[off+i] = frag[i]
				p.got++
			}
		}
		if p.got == total {
			done = append(done, handshakeMsg{typ: typ, seq: seq, body: p.body})
			delete(r.parts, seq)
		}
	}
	return done, nil
}
