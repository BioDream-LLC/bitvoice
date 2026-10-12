package p25

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"errors"
	"fmt"
)

// P25 voice encryption: the Block Encryption Protocol of TIA-102.AAAD. A key variable (DES, triple
// DES or AES-256) runs in output feedback mode from a 64-bit message indicator (MI); each voice
// superframe gets its own keystream, and the MI for the next superframe is the present one clocked
// 64 times through an LFSR. The block cipher is the Go standard library's; nothing here is a cipher.

// Algorithm IDs (TIA-102.BAAC).
const (
	AlgClear  = 0x80
	AlgDES    = 0x81 // DES-OFB, 56-bit key
	AlgTDES   = 0x83 // triple DES, three keys
	AlgAES256 = 0x84
)

// AlgName is the algorithm's usual name.
func AlgName(alg int) string {
	switch alg {
	case AlgClear:
		return "clear"
	case AlgDES:
		return "DES-OFB"
	case AlgTDES:
		return "triple DES"
	case AlgAES256:
		return "AES-256"
	}
	return fmt.Sprintf("algorithm %#02x", alg)
}

// Key is one traffic key: its algorithm, key ID and key variable.
type Key struct {
	Alg   int
	ID    int
	Bytes []byte
}

// NewBlock makes the block cipher for a key, checking its length.
func (k Key) block() (cipher.Block, error) {
	switch k.Alg {
	case AlgDES:
		if len(k.Bytes) != 8 {
			return nil, errors.New("a DES key is 8 bytes (16 hex digits)")
		}
		return des.NewCipher(k.Bytes)
	case AlgTDES:
		if len(k.Bytes) != 24 {
			return nil, errors.New("a triple DES key is 24 bytes (48 hex digits)")
		}
		return des.NewTripleDESCipher(k.Bytes)
	case AlgAES256:
		if len(k.Bytes) != 32 {
			return nil, errors.New("an AES-256 key is 32 bytes (64 hex digits)")
		}
		return aes.NewCipher(k.Bytes)
	}
	return nil, fmt.Errorf("unsupported encryption algorithm %#02x", k.Alg)
}

// Check says whether the key can be used.
func (k Key) Check() error {
	_, err := k.block()
	return err
}

// NextMI clocks the 64-bit MI LFSR (C(x) = 1 + x^15 + x^27 + x^38 + x^46 + x^62 + x^64) 64 times.
func NextMI(mi uint64) uint64 {
	for i := 0; i < 64; i++ {
		// The register shifts left; stage k is bit k-1 from the right, so stage 64 is the leftmost.
		// The feedback (stages 15, 27, 38, 46, 62 and 64) enters stage 1.
		mi = mi<<1 | lfsrFeedback(mi)
	}
	return mi
}

func lfsrFeedback(r uint64) uint64 {
	return (r>>14 ^ r>>26 ^ r>>37 ^ r>>45 ^ r>>61 ^ r>>63) & 1
}

// expand fills an n-bit input register from the MI: a register n bits long with the LFSR rule on
// its rightmost 64 bits, loaded with the MI and shifted n-64 times (AAAD 4.2).
func expand(mi uint64, n int) []byte {
	out := make([]byte, n/8)
	reg := mi
	bits := make([]byte, 0, n)
	for i := 63; i >= 0; i-- {
		bits = append(bits, byte(reg>>i&1))
	}
	for len(bits) < n {
		fb := lfsrFeedback(reg)
		reg = reg<<1 | fb
		bits = append(bits, byte(fb))
	}
	for i, b := range bits {
		out[i/8] |= b << (7 - i%8)
	}
	return out
}

// superframeOctets is how many octets of a voice superframe the keystream covers: 3 reserved, 8 of
// link control, 18 IMBE frames of 11 octets and 4 of low speed data.
const superframeOctets = 213

// Keystream is the 213 octets that encrypt one voice superframe sent with the given MI.
func (k Key) Keystream(mi uint64) ([]byte, error) {
	b, err := k.block()
	if err != nil {
		return nil, err
	}
	n := b.BlockSize()
	reg := expand(mi, 8*n)
	if n == 8 {
		reg = expand(mi, 64)
	}
	out := make([]byte, 0, superframeOctets+n)
	b.Encrypt(reg, reg) // the first iteration is not used (it encrypts the MI)
	for len(out) < superframeOctets {
		b.Encrypt(reg, reg)
		out = append(out, reg...)
	}
	return out[:superframeOctets], nil
}

// Where each IMBE frame of a superframe (1 to 18) and each pair of low speed data octets falls in
// the keystream (AAAD table 5-2): 3 reserved and 8 link control octets first, then frames 1-8, LSD,
// frames 9-17, LSD, frame 18.
func imbeOffset(frame int) int {
	off := 11 + 11*(frame-1)
	if frame >= 9 {
		off += 2
	}
	if frame >= 18 {
		off += 2
	}
	return off
}

func lsdOffset(second bool) int {
	if second {
		return imbeOffset(18) - 2
	}
	return imbeOffset(9) - 2
}

// GF(64) for the encryption sync's Reed-Solomon code (α^6 + α + 1).
var gfExp, gfLog [128]byte

func init() {
	x := 1
	for i := 0; i < 63; i++ {
		gfExp[i], gfExp[i+63] = byte(x), byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x40 != 0 {
			x ^= 0x43
		}
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// gES is the (24,16,9) code's generator, lowest power first (BAAA 5.9, octal there).
var gES = [9]byte{0o26, 0o06, 0o24, 0o57, 0o60, 0o45, 0o75, 0o67, 1}

// esEncode makes the 24 hex bits (6-bit symbols) of an encryption sync word: MI (72 bits), ALGID
// and KID as 16 symbols, then 8 parity symbols.
func esEncode(mi uint64, alg, kid int) [24]byte {
	var info [12]byte // MI(72) as 9 bytes: the 64-bit LFSR value then 8 zeros; ALGID; KID
	for i := 0; i < 8; i++ {
		info[i] = byte(mi >> (56 - 8*i))
	}
	info[9], info[10], info[11] = byte(alg), byte(kid>>8), byte(kid)
	var cw [24]byte
	for i := 0; i < 16; i++ {
		bit := 6 * i
		v := 0
		for j := 0; j < 6; j++ {
			v = v<<1 | int(info[(bit+j)/8]>>(7-(bit+j)%8)&1)
		}
		cw[i] = byte(v)
	}
	// Systematic: parity is the remainder of m(x)·x^8 by g(x); the first symbol is the highest power.
	var rem [8]byte
	for i := 0; i < 16; i++ {
		f := cw[i] ^ rem[7]
		for j := 7; j > 0; j-- {
			rem[j] = rem[j-1] ^ gfMul(f, gES[j])
		}
		rem[0] = gfMul(f, gES[0])
	}
	for j := 0; j < 8; j++ {
		cw[16+j] = rem[7-j]
	}
	return cw
}

// esDecode reads MI, ALGID and KID from a received encryption sync word, correcting up to four
// symbol errors; ok is false when it cannot.
func esDecode(cw [24]byte) (mi uint64, alg, kid int, ok bool) {
	if !rsCorrect(cw[:], 8) {
		return 0, 0, 0, false
	}
	var info [12]byte
	for i := 0; i < 16; i++ {
		for j := 0; j < 6; j++ {
			bit := 6*i + j
			if cw[i]>>(5-j)&1 != 0 {
				info[bit/8] |= 0x80 >> (bit % 8)
			}
		}
	}
	for i := 0; i < 8; i++ {
		mi = mi<<8 | uint64(info[i])
	}
	return mi, int(info[9]), int(info[10])<<8 | int(info[11]), true
}

// rsCorrect corrects a shortened Reed-Solomon code word over GF(64) in place (first symbol the
// highest power, roots α^1..α^nroots) with Berlekamp-Massey, Chien search and Forney.
func rsCorrect(cw []byte, nroots int) bool {
	n := len(cw)
	syn := make([]byte, nroots)
	bad := false
	for i := 0; i < nroots; i++ {
		var s byte
		for _, c := range cw {
			s = gfMul(s, gfExp[i+1]) ^ c
		}
		syn[i] = s
		bad = bad || s != 0
	}
	if !bad {
		return true
	}
	// Berlekamp-Massey: the error locator λ.
	lam := make([]byte, nroots+1)
	b := make([]byte, nroots+1)
	lam[0], b[0] = 1, 1
	l := 0
	for r := 0; r < nroots; r++ {
		var d byte
		for i := 0; i <= l; i++ {
			d ^= gfMul(lam[i], syn[r-i])
		}
		copy(b[1:], b[:nroots]) // b(x)·x
		b[0] = 0
		if d != 0 {
			t := make([]byte, nroots+1)
			for i := range t {
				t[i] = lam[i] ^ gfMul(d, b[i])
			}
			if 2*l <= r {
				l = r + 1 - l
				inv := gfExp[63-int(gfLog[d])]
				for i := range b {
					b[i] = gfMul(lam[i], inv)
				}
			}
			lam = t
		}
	}
	// Chien search over the positions of the shortened code: position p (0 = first symbol) is the
	// coefficient of x^(n-1-p).
	var pos []int
	for p := 0; p < n; p++ {
		e := n - 1 - p
		xinv := gfExp[(63-e%63)%63]
		var v byte
		for i := l; i >= 0; i-- {
			v = gfMul(v, xinv) ^ lam[i]
		}
		if v == 0 {
			pos = append(pos, p)
		}
	}
	if len(pos) != l {
		return false
	}
	// Forney: Ω = S·λ mod x^nroots; error value = Ω(X⁻¹) / λ'(X⁻¹) (roots start at α^1, so no X factor).
	omega := make([]byte, nroots)
	for i := 0; i < nroots; i++ {
		for j := 0; j <= i && j <= l; j++ {
			omega[i] ^= gfMul(syn[i-j], lam[j])
		}
	}
	for _, p := range pos {
		e := n - 1 - p
		xinv := gfExp[(63-e%63)%63]
		var num, den byte
		for i := nroots - 1; i >= 0; i-- {
			num = gfMul(num, xinv) ^ omega[i]
		}
		for i := 1; i <= l; i += 2 { // the formal derivative keeps the odd terms
			t := lam[i]
			for k := 0; k < i-1; k++ {
				t = gfMul(t, xinv)
			}
			den ^= t
		}
		if den == 0 {
			return false
		}
		cw[p] ^= gfMul(num, gfExp[63-int(gfLog[den])])
	}
	return true
}

// esSymbols packs and unpacks the four encryption sync symbols an IMBE frame 12-17 carries in its
// frame-specific octets (TIA-102.BAHA figure 33): ES symbols of 6 bits, then 1 reserved bit and a
// 7-bit status.
func putES(extra []byte, s [4]byte) {
	v := uint32(s[0])<<26 | uint32(s[1])<<20 | uint32(s[2])<<14 | uint32(s[3])<<8
	extra[0], extra[1], extra[2], extra[3] = byte(v>>24), byte(v>>16), byte(v>>8), 0
}

func getES(extra []byte) [4]byte {
	v := uint32(extra[0])<<24 | uint32(extra[1])<<16 | uint32(extra[2])<<8 | uint32(extra[3])
	return [4]byte{byte(v >> 26 & 63), byte(v >> 20 & 63), byte(v >> 14 & 63), byte(v >> 8 & 63)}
}
