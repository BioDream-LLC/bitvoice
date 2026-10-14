// Package speex is narrowband Speex (8 kHz, 20 ms frames, RFC 5574), a Go translation of the
// floating-point build of the Xiph.Org reference library, libspeex 1.2.1 (BSD licence, see
// LICENSE-SPEEX). It is bit-exact with that build: the same packets from the encoder and the same
// samples from the decoder, packet loss included (TestBitExactWithLibspeex).
//
// Every product goes through mul (or mul64 where the reference computes in double), which rounds
// it, so that Go never fuses a multiply and an add into one instruction and changes the rounding.
package speex

import "math"

func mul(a, b float32) float32   { return float32(a * b) }
func mul64(a, b float64) float64 { return float64(a * b) }

// bitWriter packs fields most significant bit first, as speex_bits_pack.
type bitWriter struct {
	buf   []byte
	nbits int
}

func (w *bitWriter) pack(data uint32, n int) {
	for n > 0 {
		bit := (data >> uint(n-1)) & 1
		if w.nbits%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		w.buf[w.nbits/8] |= byte(bit) << uint(7-w.nbits%8)
		w.nbits++
		n--
	}
}

// bytes is the frame as speex_bits_write gives it: the last byte filled with a 0 then 1s.
func (w *bitWriter) bytes() []byte {
	out := append([]byte(nil), w.buf...)
	if r := w.nbits % 8; r != 0 {
		out[len(out)-1] |= byte(0xff>>uint(r+1)) & 0xff
	}
	return out
}

// bitReader reads fields as speex_bits_unpack_unsigned; reading past the end sets overflow and
// gives zeros.
type bitReader struct {
	buf      []byte
	pos      int
	overflow bool
}

func (r *bitReader) remaining() int {
	if r.overflow {
		return -1
	}
	return len(r.buf)*8 - r.pos
}

func (r *bitReader) unpack(n int) uint32 {
	if r.pos+n > len(r.buf)*8 {
		r.overflow = true
	}
	if r.overflow {
		return 0
	}
	var d uint32
	for ; n > 0; n-- {
		d = d<<1 | uint32(r.buf[r.pos/8]>>uint(7-r.pos%8))&1
		r.pos++
	}
	return d
}

func (r *bitReader) advance(n int) {
	if r.pos+n > len(r.buf)*8 || r.overflow {
		r.overflow = true
		return
	}
	r.pos += n
}

// speexRand is the reference's uniform noise generator.
func speexRand(std float32, seed *uint32) float32 {
	*seed = 1664525**seed + 1013904223
	f := math.Float32frombits(0x3f800000 | (0x007fffff & *seed))
	f = float32(float64(f) - 1.5)
	return float32(mul64(mul64(3.4642, float64(std)), float64(f)))
}

// spxCos is the reference's cosine approximation.
func spxCos(x float32) float32 {
	if float64(x) < 1.5707963268 {
		x = mul(x, x)
		return 0.9999932946 + mul(x, -0.4999124376+mul(x, 0.0414877472+mul(-0.0012712095, x)))
	}
	x = float32(math.Pi - float64(x))
	x = mul(x, x)
	return -(0.9999932946 + mul(x, -0.4999124376+mul(x, 0.0414877472+mul(-0.0012712095, x))))
}
