// Package srtp implements Secure RTP (RFC 3711) with the AES_CM_128_HMAC_SHA1_80 and _32 suites, the
// ones every SIP phone offers. The ciphers and MAC are Go's standard library (crypto/aes, crypto/hmac,
// crypto/sha1); this package only arranges them as RFC 3711 says: key derivation, the counter-mode IV,
// the rollover counter, authentication tags and the replay window.
package srtp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"hash"
	"sync"
)

// Suite names as SDP writes them (RFC 4568).
const (
	AES128SHA80 = "AES_CM_128_HMAC_SHA1_80"
	AES128SHA32 = "AES_CM_128_HMAC_SHA1_32"
)

// Errors from Unprotect.
var (
	ErrShort  = errors.New("srtp: packet too short")
	ErrAuth   = errors.New("srtp: authentication failed")
	ErrReplay = errors.New("srtp: replayed packet")
)

const (
	keyLen  = 16
	saltLen = 14
	authLen = 20
)

// TagLen returns the RTP authentication tag length of a suite, or 0 for an unknown suite.
func TagLen(suite string) int {
	switch suite {
	case AES128SHA80:
		return 10
	case AES128SHA32:
		return 4
	}
	return 0
}

// deriveKey implements the RFC 3711 4.3.1 key derivation with a key derivation rate of 0.
func deriveKey(block cipher.Block, salt []byte, label byte, n int) []byte {
	iv := make([]byte, 16)
	copy(iv, salt)
	iv[7] ^= label
	out := make([]byte, n)
	cipher.NewCTR(block, iv).XORKeyStream(out, out)
	return out
}

type keys struct {
	block cipher.Block
	salt  []byte
	mac   hash.Hash
}

func newKeys(master, masterSalt []byte, labels [3]byte) (keys, error) {
	mb, err := aes.NewCipher(master)
	if err != nil {
		return keys{}, err
	}
	ek := deriveKey(mb, masterSalt, labels[0], keyLen)
	ak := deriveKey(mb, masterSalt, labels[1], authLen)
	sk := deriveKey(mb, masterSalt, labels[2], saltLen)
	b, err := aes.NewCipher(ek)
	if err != nil {
		return keys{}, err
	}
	return keys{block: b, salt: sk, mac: hmac.New(sha1.New, ak)}, nil
}

// Context protects or unprotects one direction of a media session; each SSRC is tracked separately. It
// is safe for concurrent use.
type Context struct {
	mu        sync.Mutex
	rtp, rtcp keys
	tagLen    int
	streams   map[uint32]*stream
	rtcpIndex uint32
	rtcpWin   replay
}

type stream struct {
	roc     uint32
	highSeq uint16
	started bool
	win     replay
}

// replay is a 64-packet sliding window (RFC 3711 3.3.2).
type replay struct {
	top    uint64
	bits   uint64
	inited bool
}

func (r *replay) check(i uint64) bool {
	if !r.inited || i > r.top {
		return true
	}
	d := r.top - i
	return d < 64 && r.bits&(1<<d) == 0
}

func (r *replay) accept(i uint64) {
	if !r.inited {
		r.inited, r.top, r.bits = true, i, 1
		return
	}
	if i > r.top {
		shift := i - r.top
		if shift >= 64 {
			r.bits = 0
		} else {
			r.bits <<= shift
		}
		r.bits |= 1
		r.top = i
		return
	}
	r.bits |= 1 << (r.top - i)
}

// New makes a context from a 16-byte master key and 14-byte master salt.
func New(suite string, masterKey, masterSalt []byte) (*Context, error) {
	tl := TagLen(suite)
	if tl == 0 {
		return nil, errors.New("srtp: unsupported suite " + suite)
	}
	if len(masterKey) != keyLen || len(masterSalt) != saltLen {
		return nil, errors.New("srtp: the master key is 16 bytes and the salt 14")
	}
	rk, err := newKeys(masterKey, masterSalt, [3]byte{0, 1, 2})
	if err != nil {
		return nil, err
	}
	ck, err := newKeys(masterKey, masterSalt, [3]byte{3, 4, 5})
	if err != nil {
		return nil, err
	}
	return &Context{rtp: rk, rtcp: ck, tagLen: tl, streams: map[uint32]*stream{}}, nil
}

func (k *keys) iv(ssrc uint32, index uint64) []byte {
	iv := make([]byte, 16)
	copy(iv, k.salt)
	var s [4]byte
	binary.BigEndian.PutUint32(s[:], ssrc)
	for i := range 4 {
		iv[4+i] ^= s[i]
	}
	for i := range 6 {
		iv[8+i] ^= byte(index >> (8 * (5 - i)))
	}
	return iv
}

func (k *keys) tag(data []byte, roc *uint32, n int) []byte {
	k.mac.Reset()
	k.mac.Write(data)
	if roc != nil {
		var r [4]byte
		binary.BigEndian.PutUint32(r[:], *roc)
		k.mac.Write(r[:])
	}
	return k.mac.Sum(nil)[:n]
}

// headerLen is the RTP header length including CSRCs and an extension.
func headerLen(p []byte) (int, error) {
	if len(p) < 12 {
		return 0, ErrShort
	}
	n := 12 + 4*int(p[0]&0x0f)
	if p[0]&0x10 != 0 {
		if len(p) < n+4 {
			return 0, ErrShort
		}
		n += 4 + 4*int(binary.BigEndian.Uint16(p[n+2:]))
	}
	if n > len(p) {
		return 0, ErrShort
	}
	return n, nil
}

// estimate guesses the packet's rollover counter (RFC 3711 3.3.1).
func (s *stream) estimate(seq uint16) uint32 {
	if !s.started {
		return s.roc
	}
	v := s.roc
	if s.highSeq < 32768 {
		if int(seq)-int(s.highSeq) > 32768 && v > 0 {
			v--
		}
	} else if int(s.highSeq)-32768 > int(seq) {
		v++
	}
	return v
}

func (s *stream) update(seq uint16, roc uint32) {
	switch {
	case !s.started:
		s.started, s.highSeq, s.roc = true, seq, roc
	case roc > s.roc || roc == s.roc && seq > s.highSeq:
		s.highSeq, s.roc = seq, roc
	}
}

func (c *Context) stream(ssrc uint32) *stream {
	s := c.streams[ssrc]
	if s == nil {
		s = &stream{}
		c.streams[ssrc] = s
	}
	return s
}

// Protect encrypts and authenticates an RTP packet, returning a new slice.
func (c *Context) Protect(pkt []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := headerLen(pkt)
	if err != nil {
		return nil, err
	}
	seq := binary.BigEndian.Uint16(pkt[2:])
	ssrc := binary.BigEndian.Uint32(pkt[8:])
	s := c.stream(ssrc)
	roc := s.estimate(seq)
	s.update(seq, roc)
	out := make([]byte, len(pkt), len(pkt)+c.tagLen)
	copy(out, pkt)
	idx := uint64(roc)<<16 | uint64(seq)
	cipher.NewCTR(c.rtp.block, c.rtp.iv(ssrc, idx)).XORKeyStream(out[h:], out[h:])
	return append(out, c.rtp.tag(out, &roc, c.tagLen)...), nil
}

// Unprotect checks and decrypts an SRTP packet, returning the RTP packet.
func (c *Context) Unprotect(pkt []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(pkt) < 12+c.tagLen {
		return nil, ErrShort
	}
	body, tag := pkt[:len(pkt)-c.tagLen], pkt[len(pkt)-c.tagLen:]
	h, err := headerLen(body)
	if err != nil {
		return nil, err
	}
	seq := binary.BigEndian.Uint16(body[2:])
	ssrc := binary.BigEndian.Uint32(body[8:])
	s := c.stream(ssrc)
	roc := s.estimate(seq)
	idx := uint64(roc)<<16 | uint64(seq)
	if !s.win.check(idx) {
		return nil, ErrReplay
	}
	if subtle.ConstantTimeCompare(c.rtp.tag(body, &roc, c.tagLen), tag) != 1 {
		return nil, ErrAuth
	}
	out := append([]byte(nil), body...)
	cipher.NewCTR(c.rtp.block, c.rtp.iv(ssrc, idx)).XORKeyStream(out[h:], out[h:])
	s.win.accept(idx)
	s.update(seq, roc)
	return out, nil
}

// SRTCP always carries the 80-bit tag (RFC 3711 3.4) whatever the RTP suite.
const rtcpTag = 10

// ProtectRTCP encrypts and authenticates an RTCP compound packet.
func (c *Context) ProtectRTCP(pkt []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(pkt) < 8 {
		return nil, ErrShort
	}
	c.rtcpIndex = (c.rtcpIndex + 1) & 0x7fffffff
	ssrc := binary.BigEndian.Uint32(pkt[4:])
	out := make([]byte, len(pkt), len(pkt)+4+rtcpTag)
	copy(out, pkt)
	cipher.NewCTR(c.rtcp.block, c.rtcp.iv(ssrc, uint64(c.rtcpIndex))).XORKeyStream(out[8:], out[8:])
	out = binary.BigEndian.AppendUint32(out, 0x80000000|c.rtcpIndex)
	return append(out, c.rtcp.tag(out, nil, rtcpTag)...), nil
}

// UnprotectRTCP checks and decrypts an SRTCP packet.
func (c *Context) UnprotectRTCP(pkt []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(pkt) < 8+4+rtcpTag {
		return nil, ErrShort
	}
	body, tag := pkt[:len(pkt)-rtcpTag], pkt[len(pkt)-rtcpTag:]
	if subtle.ConstantTimeCompare(c.rtcp.tag(body, nil, rtcpTag), tag) != 1 {
		return nil, ErrAuth
	}
	e := binary.BigEndian.Uint32(body[len(body)-4:])
	idx := uint64(e & 0x7fffffff)
	if !c.rtcpWin.check(idx) {
		return nil, ErrReplay
	}
	c.rtcpWin.accept(idx)
	out := append([]byte(nil), body[:len(body)-4]...)
	if e&0x80000000 != 0 {
		ssrc := binary.BigEndian.Uint32(out[4:])
		cipher.NewCTR(c.rtcp.block, c.rtcp.iv(ssrc, idx)).XORKeyStream(out[8:], out[8:])
	}
	return out, nil
}
