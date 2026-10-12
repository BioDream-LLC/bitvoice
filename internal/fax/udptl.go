// Package fax receives and sends fax over T.38 (ITU-T T.38, T.30, T.4) with nothing but Go's
// standard library: it is the fax machine at BitVoice's end of a call. Fax arrives as T.30
// signalling and T.4 page data inside T.38 IFP packets, carried by UDPTL; pages come out as a PDF
// whose images keep the fax's own compression (PDF's CCITTFaxDecode), so no picture is redrawn.
package fax

import (
	"errors"
)

// IFP indicators (T.38 Type-of-msg t30-indicator).
const (
	IndNoSignal = iota
	IndCNG
	IndCED
	IndV21Preamble
	IndV27_2400
	IndV27_4800
	IndV29_7200
	IndV29_9600
	IndV17_7200Short
	IndV17_7200Long
	IndV17_9600Short
	IndV17_9600Long
	IndV17_12000Short
	IndV17_12000Long
	IndV17_14400Short
	IndV17_14400Long
)

// IFP data types (t30-data).
const (
	DataV21 = iota
	DataV27_2400
	DataV27_4800
	DataV29_7200
	DataV29_9600
	DataV17_7200
	DataV17_9600
	DataV17_12000
	DataV17_14400
)

// Field types (Data-Field field-type).
const (
	FieldHDLCData = iota
	FieldHDLCSigEnd
	FieldHDLCFCSOK
	FieldHDLCFCSBad
	FieldHDLCFCSOKSigEnd
	FieldHDLCFCSBadSigEnd
	FieldT4Data
	FieldT4SigEnd
)

// Field is one data field of an IFP packet.
type Field struct {
	Type int
	Data []byte
}

// IFP is one T.38 Internet Facsimile Protocol packet: an indicator, or data of a modulation with
// its fields.
type IFP struct {
	Indicator bool // true: Kind is a t30-indicator; false: Kind is a t30-data type
	Kind      int
	Fields    []Field
}

var errShort = errors.New("fax: packet too short")

// The IFP encoding is ASN.1 aligned PER (X.691) of T.38's IFPPacket:
//   1 bit data-field present, 1 bit CHOICE (0 indicator, 1 data), 1 bit extension (0), 4 bits value;
//   then, octet aligned, the field count; each field is 1 bit field-data present and 3 bits field
//   type (T.38 version 0, the 1998 syntax, has no extension bit there, and is what BitVoice
//   negotiates), and when present, octet aligned, the data length less one in two octets and the
//   data. A field without data takes only those 4 bits: the next one follows in the same octet.

type bitWriter struct {
	b []byte
	n int // bits used in the last octet (0: aligned)
}

func (w *bitWriter) bits(v uint, count int) {
	for i := count - 1; i >= 0; i-- {
		if w.n == 0 {
			w.b = append(w.b, 0)
		}
		if v>>uint(i)&1 != 0 {
			w.b[len(w.b)-1] |= 0x80 >> uint(w.n)
		}
		w.n = (w.n + 1) % 8
	}
}

func (w *bitWriter) align()           { w.n = 0 }
func (w *bitWriter) octets(b ...byte) { w.align(); w.b = append(w.b, b...) }

type bitReader struct {
	b   []byte
	pos int // in bits
}

func (r *bitReader) bits(count int) (uint, error) {
	var v uint
	for i := 0; i < count; i++ {
		if r.pos/8 >= len(r.b) {
			return 0, errShort
		}
		v = v<<1 | uint(r.b[r.pos/8]>>(7-uint(r.pos%8))&1)
		r.pos++
	}
	return v, nil
}

func (r *bitReader) align() { r.pos = (r.pos + 7) / 8 * 8 }

func (r *bitReader) octets(n int) ([]byte, error) {
	r.align()
	at := r.pos / 8
	if at+n > len(r.b) {
		return nil, errShort
	}
	r.pos += 8 * n
	return r.b[at : at+n], nil
}

// Marshal encodes an IFP packet. Only T.38 version 0 values are produced.
func (p IFP) Marshal() []byte {
	w := &bitWriter{}
	present, choice := uint(0), uint(1)
	if !p.Indicator && len(p.Fields) > 0 {
		present = 1
	}
	if p.Indicator {
		choice = 0
	}
	w.bits(present, 1)
	w.bits(choice, 1)
	w.bits(0, 1) // not an extension value
	w.bits(uint(p.Kind&0x0f), 4)
	if present == 0 {
		return w.b
	}
	w.octets(appendLength(nil, len(p.Fields))...)
	for _, f := range p.Fields {
		has := uint(0)
		if len(f.Data) > 0 {
			has = 1
		}
		w.bits(has, 1)
		w.bits(uint(f.Type&0x07), 3) // version 0 (1998) syntax: no extension bit
		if has == 1 {
			n := len(f.Data) - 1
			w.octets(byte(n>>8), byte(n))
			w.octets(f.Data...)
		}
	}
	return w.b
}

// appendLength writes an aligned PER length determinant (X.691 10.9).
func appendLength(b []byte, n int) []byte {
	if n < 128 {
		return append(b, byte(n))
	}
	return append(b, 0x80|byte(n>>8), byte(n))
}

func readLength(b []byte) (n, used int, err error) {
	if len(b) < 1 {
		return 0, 0, errShort
	}
	if b[0]&0x80 == 0 {
		return int(b[0]), 1, nil
	}
	if b[0]&0xc0 == 0x80 && len(b) >= 2 {
		return int(b[0]&0x3f)<<8 | int(b[1]), 2, nil
	}
	return 0, 0, errors.New("fax: length too large")
}

// ParseIFP decodes an IFP packet. Extension values (T.38 version 1 and later: V.8, V.34, V.33) are
// reported with Kind -1 (or a field Type -1), so a version 0 receiver can ignore them.
func ParseIFP(b []byte) (IFP, error) {
	r := &bitReader{b: b}
	var p IFP
	present, err := r.bits(1)
	if err != nil {
		return p, err
	}
	choice, _ := r.bits(1)
	ext, _ := r.bits(1)
	p.Indicator = choice == 0
	if ext == 1 {
		// An extension value: a normally small non-negative number (6 bits after a 0 bit).
		if _, err := r.bits(7); err != nil {
			return p, err
		}
		p.Kind = -1
	} else {
		k, err := r.bits(4)
		if err != nil {
			return p, err
		}
		p.Kind = int(k)
	}
	if present == 0 {
		return p, nil
	}
	r.align()
	count, used, err := readLength(b[r.pos/8:])
	if err != nil {
		return p, err
	}
	r.pos += 8 * used
	for i := 0; i < count; i++ {
		has, err := r.bits(1)
		if err != nil {
			return p, err
		}
		ft, err := r.bits(3)
		if err != nil {
			return p, err
		}
		f := Field{Type: int(ft)}
		if has == 1 {
			ln, err := r.octets(2)
			if err != nil {
				return p, err
			}
			d, err := r.octets(int(ln[0])<<8 | int(ln[1]) + 1)
			if err != nil {
				return p, err
			}
			f.Data = append([]byte(nil), d...)
		}
		p.Fields = append(p.Fields, f)
	}
	return p, nil
}

// UDPTL carries IFP packets over UDP (T.38 Annex... 9.1): each datagram holds a sequence number,
// the IFP packet, and copies of the packets before it (redundancy), so a lost datagram's packet
// arrives in the next one.
//
//   seq-number (2 octets), primary-ifp-packet (length, octets), error-recovery: one bit choosing
//   redundancy (0) or FEC (1), then, aligned, the number of secondary packets and each as length
//   and octets, newest first.

// MarshalUDPTL builds a datagram: the packet, and the packets before it (newest first) as
// redundancy.
func MarshalUDPTL(seq uint16, primary []byte, previous [][]byte) []byte {
	b := []byte{byte(seq >> 8), byte(seq)}
	b = appendLength(b, len(primary))
	b = append(b, primary...)
	b = append(b, 0x00) // error-recovery: secondary-ifp-packets
	b = appendLength(b, len(previous))
	for _, s := range previous {
		b = appendLength(b, len(s))
		b = append(b, s...)
	}
	return b
}

// UDPTLPacket is a decoded datagram: its sequence number, IFP packet and the copies of the packets
// before it (Secondary[0] is seq-1, and so on; empty when the sender uses FEC).
type UDPTLPacket struct {
	Seq       uint16
	Primary   []byte
	Secondary [][]byte
}

// ParseUDPTL decodes a datagram.
func ParseUDPTL(b []byte) (UDPTLPacket, error) {
	if len(b) < 3 {
		return UDPTLPacket{}, errShort
	}
	p := UDPTLPacket{Seq: uint16(b[0])<<8 | uint16(b[1])}
	n, used, err := readLength(b[2:])
	if err != nil {
		return p, err
	}
	at := 2 + used
	if at+n > len(b) {
		return p, errShort
	}
	p.Primary = b[at : at+n]
	at += n
	if at >= len(b) || b[at]&0x80 != 0 {
		return p, nil // no error recovery, or FEC (not used for recovery here)
	}
	at++
	count, used, err := readLength(b[at:])
	if err != nil {
		return p, nil
	}
	at += used
	for i := 0; i < count && at < len(b); i++ {
		l, used, err := readLength(b[at:])
		if err != nil || at+used+l > len(b) {
			break
		}
		at += used
		p.Secondary = append(p.Secondary, b[at:at+l])
		at += l
	}
	return p, nil
}
