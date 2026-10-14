// Package h323 joins H.323 trunks (H.225.0 call signalling over TCP, Fast Connect) to the engine.
// To the engine each H.323 trunk is an ordinary SIP trunk at a loopback port of the gateway's own,
// like an IAX2 trunk; the gateway turns each call into an H.323 call and back. Audio does not pass
// through the gateway: Fast Connect only exchanges RTP addresses, so the far end and the engine send
// RTP to each other directly. H.245 is not spoken: a peer that refuses Fast Connect gets no call.
package h323

import (
	"bytes"
	"errors"
	"fmt"
)

// This file is ALIGNED PER (ITU-T X.691), as H.225.0 and H.245 use it, for the types they need.

var errPER = errors.New("h323: bad PER encoding")

// perError carries a decoding failure out of the nested decoders.
type perError struct{ err error }

// catch turns a decoding failure into an error.
func catch(err *error) {
	if r := recover(); r != nil {
		if pe, ok := r.(perError); ok {
			*err = pe.err
			return
		}
		panic(r)
	}
}

func fail(format string, args ...any) {
	panic(perError{fmt.Errorf("h323: "+format, args...)})
}

type reader struct {
	b   []byte
	pos int // in bits
}

func (r *reader) bit() bool {
	if r.pos >= len(r.b)*8 {
		fail("message ends early")
	}
	v := r.b[r.pos/8]>>(7-uint(r.pos%8))&1 == 1
	r.pos++
	return v
}

func (r *reader) bits(n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		v <<= 1
		if r.bit() {
			v |= 1
		}
	}
	return v
}

func (r *reader) align() { r.pos = (r.pos + 7) &^ 7 }

func (r *reader) octets(n int) []byte {
	r.align()
	end := r.pos/8 + n
	if n < 0 || end > len(r.b) {
		fail("message ends early")
	}
	out := r.b[r.pos/8 : end]
	r.pos = end * 8
	return out
}

// constrained reads a whole number in lb..ub.
func (r *reader) constrained(lb, ub int64) int64 {
	rng := ub - lb + 1
	switch {
	case rng == 1:
		return lb
	case rng <= 255:
		return lb + int64(r.bits(bitsFor(rng)))
	case rng == 256:
		r.align()
		return lb + int64(r.bits(8))
	case rng <= 65536:
		r.align()
		return lb + int64(r.bits(16))
	}
	// Larger ranges: a length in octets (1..), then the value.
	n := int(r.constrained(1, int64(bytesFor(uint64(rng-1)))))
	r.align()
	return lb + int64(r.bits(8*n))
}

// length reads an unconstrained length determinant.
func (r *reader) length() int {
	r.align()
	if !r.bit() {
		return int(r.bits(7))
	}
	if !r.bit() {
		return int(r.bits(14))
	}
	fail("fragmented length")
	return 0
}

func (r *reader) smallNumber() int {
	if !r.bit() {
		return int(r.bits(6))
	}
	n := r.length()
	if n > 2 {
		fail("number too large")
	}
	return int(r.bits(8 * n))
}

// count reads the number of items of a SEQUENCE OF, each taking at least a bit.
func (r *reader) count() int {
	n := r.length()
	if n > len(r.b)*8-r.pos {
		fail("message ends early")
	}
	return n
}

// openType returns the octets of an open type (an extension, or an extension choice).
func (r *reader) openType() []byte { return r.octets(r.length()) }

// extensions reads, after a sequence's root, which additions are present, and each one's octets.
func (r *reader) extensions() map[int][]byte {
	n := r.smallNumber() + 1
	if n > len(r.b)*8-r.pos {
		fail("message ends early")
	}
	present := make([]bool, n)
	for i := range present {
		present[i] = r.bit()
	}
	out := map[int][]byte{}
	for i, p := range present {
		if p {
			out[i] = r.openType()
		}
	}
	return out
}

// choice reads a choice index: root alternatives 0..n-1, or an extension (ext, index).
func (r *reader) choice(n int, extensible bool) (int, bool) {
	if extensible && r.bit() {
		return r.smallNumber(), true
	}
	return int(r.constrained(0, int64(n-1))), false
}

func (r *reader) octetString(lb, ub int) []byte {
	if lb == ub {
		if lb <= 2 {
			return bitsToBytes(r, lb)
		}
		return r.octets(lb)
	}
	var n int
	if ub < 0 {
		n = r.length()
	} else {
		n = int(r.constrained(int64(lb), int64(ub)))
	}
	return r.octets(n)
}

func bitsToBytes(r *reader, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.bits(8))
	}
	return out
}

func (r *reader) oid() []byte { return r.octets(r.length()) }

func (r *reader) bmpString(lb, ub int) string {
	n := int(r.constrained(int64(lb), int64(ub)))
	b := r.octets(2 * n)
	rs := make([]rune, n)
	for i := range rs {
		rs[i] = rune(b[2*i])<<8 | rune(b[2*i+1])
	}
	return string(rs)
}

const digitAlphabet = "#*,0123456789" // dialedDigits' permitted alphabet, in canonical order

func (r *reader) digits(lb, ub int) string {
	n := int(r.constrained(int64(lb), int64(ub)))
	if ub*4 > 16 {
		r.align()
	}
	out := make([]byte, n)
	for i := range out {
		v := int(r.bits(4))
		if v >= len(digitAlphabet) {
			fail("bad digit")
		}
		out[i] = digitAlphabet[v]
	}
	return string(out)
}

func bitsFor(rng int64) int {
	n := 0
	for (int64(1) << n) < rng {
		n++
	}
	return n
}

func bytesFor(v uint64) int {
	n := 1
	for v >>= 8; v > 0; v >>= 8 {
		n++
	}
	return n
}

type writer struct {
	b    []byte
	nbit int
}

func (w *writer) bit(v bool) {
	if w.nbit%8 == 0 {
		w.b = append(w.b, 0)
	}
	if v {
		w.b[len(w.b)-1] |= 1 << (7 - uint(w.nbit%8))
	}
	w.nbit++
}

func (w *writer) bits(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bit(v>>uint(i)&1 == 1)
	}
}

func (w *writer) align() { w.nbit = (w.nbit + 7) &^ 7 }

func (w *writer) octets(b []byte) {
	w.align()
	w.b = append(w.b[:w.nbit/8], b...)
	w.nbit += 8 * len(b)
}

func (w *writer) constrained(v, lb, ub int64) {
	rng := ub - lb + 1
	v -= lb
	switch {
	case rng == 1:
	case rng <= 255:
		w.bits(uint64(v), bitsFor(rng))
	case rng == 256:
		w.align()
		w.bits(uint64(v), 8)
	case rng <= 65536:
		w.align()
		w.bits(uint64(v), 16)
	default:
		n := bytesFor(uint64(v))
		w.constrained(int64(n), 1, int64(bytesFor(uint64(rng-1))))
		w.align()
		w.bits(uint64(v), 8*n)
	}
}

func (w *writer) length(n int) {
	w.align()
	if n < 128 {
		w.bits(uint64(n), 8)
		return
	}
	w.bits(uint64(0x8000|n), 16)
}

func (w *writer) smallNumber(n int) {
	if n < 64 {
		w.bit(false)
		w.bits(uint64(n), 6)
		return
	}
	w.bit(true)
	w.length(bytesFor(uint64(n)))
	w.bits(uint64(n), 8*bytesFor(uint64(n)))
}

func (w *writer) openType(enc []byte) {
	if len(enc) == 0 {
		enc = []byte{0}
	}
	w.length(len(enc))
	w.octets(enc)
}

// sub encodes a nested type on its own, for an open type.
func sub(f func(*writer)) []byte {
	w := &writer{}
	f(w)
	return w.b
}

// extensions writes the addition bitmap of a sequence (count additions known) and each present one.
func (w *writer) extensions(count int, present map[int][]byte) {
	w.smallNumber(count - 1)
	for i := 0; i < count; i++ {
		_, ok := present[i]
		w.bit(ok)
	}
	for i := 0; i < count; i++ {
		if b, ok := present[i]; ok {
			w.openType(b)
		}
	}
}

func (w *writer) octetString(b []byte, lb, ub int) {
	if lb == ub {
		if lb <= 2 {
			for _, c := range b {
				w.bits(uint64(c), 8)
			}
			return
		}
		w.octets(b)
		return
	}
	if ub < 0 {
		w.length(len(b))
	} else {
		w.constrained(int64(len(b)), int64(lb), int64(ub))
	}
	w.octets(b)
}

func (w *writer) oid(content []byte) {
	w.length(len(content))
	w.octets(content)
}

func (w *writer) bmpString(s string, lb, ub int) {
	rs := []rune(s)
	if len(rs) > ub {
		rs = rs[:ub]
	}
	w.constrained(int64(len(rs)), int64(lb), int64(ub))
	var b []byte
	for _, c := range rs {
		if c > 0xffff {
			c = '?'
		}
		b = append(b, byte(c>>8), byte(c))
	}
	w.octets(b)
}

func (w *writer) digits(s string, lb, ub int) {
	var ok []byte
	for i := 0; i < len(s) && len(ok) < ub; i++ {
		if bytes.IndexByte([]byte(digitAlphabet), s[i]) >= 0 {
			ok = append(ok, s[i])
		}
	}
	w.constrained(int64(len(ok)), int64(lb), int64(ub))
	if ub*4 > 16 {
		w.align()
	}
	for _, c := range ok {
		w.bits(uint64(bytes.IndexByte([]byte(digitAlphabet), c)), 4)
	}
}
