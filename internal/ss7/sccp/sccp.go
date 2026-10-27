// Package sccp is SS7's Signalling Connection Control Part for connectionless messages (ITU-T Q.713):
// unitdata (UDT, XUDT) and the service messages that return one that could not be delivered (UDTS,
// XUDTS), with called and calling party addresses by point code and subsystem number or by global
// title, and global title translation to a point code and subsystem. (Cisco's Skinny, also called
// SCCP, is internal/sccp.)
package sccp

import (
	"errors"
	"fmt"
	"strings"
)

// Message types (Q.713 2.1).
const (
	TypeUDT   = 0x09
	TypeUDTS  = 0x0a
	TypeXUDT  = 0x11
	TypeXUDTS = 0x12
)

// SI is SCCP's service indicator in MTP3.
const SI = 3

// Address is a called or calling party address (Q.713 3.4, ITU format). RouteOnSSN: route on the
// point code and subsystem number; otherwise on the global title.
type Address struct {
	RouteOnSSN bool
	PC         int // -1: absent
	SSN        int // 0: absent
	GT         *GT
}

// GT is a global title. Indicator 4 (translation type, numbering plan, encoding scheme, nature of
// address) is the common one; 1 (nature of address only) and 2 (translation type only) are read too.
type GT struct {
	Indicator       int
	TranslationType int
	NumberingPlan   int // 1: E.164
	NatureOfAddress int // 4: international number
	Digits          string
	odd             bool
}

// Message is a connectionless SCCP message.
type Message struct {
	Type          int
	Class         int  // protocol class 0 or 1
	ReturnOnError bool // ask for a UDTS when it cannot be delivered
	HopCounter    int  // XUDT
	Cause         int  // UDTS/XUDTS: the return cause (Q.713 3.12)
	Called        Address
	Calling       Address
	Data          []byte
}

// Return causes (Q.713 3.12) BitVoice sends.
const (
	CauseNoTranslationForAddress = 0x01
	CauseSubsystemFailure        = 0x03
	CauseUnequippedUser          = 0x04
	CauseMTPFailure              = 0x05
	CauseHopCounterViolation     = 0x0c
)

// ErrShort is a message too short for what it says it holds.
var ErrShort = errors.New("sccp: message too short")

func encodeAddress(a Address) ([]byte, error) {
	ai := byte(0)
	var b []byte
	if a.RouteOnSSN {
		ai |= 0x40
	}
	if a.PC >= 0 {
		ai |= 0x01
		b = append(b, byte(a.PC), byte(a.PC>>8)&0x3F)
	}
	if a.SSN > 0 {
		ai |= 0x02
		b = append(b, byte(a.SSN))
	}
	if a.GT != nil {
		g := a.GT
		ind := g.Indicator
		if ind == 0 {
			ind = 4
		}
		ai |= byte(ind) << 2
		digits, odd, err := bcd(g.Digits)
		if err != nil {
			return nil, err
		}
		switch ind {
		case 1:
			noa := byte(g.NatureOfAddress & 0x7F)
			if odd {
				noa |= 0x80
			}
			b = append(b, noa)
		case 2:
			b = append(b, byte(g.TranslationType))
		case 4:
			es := byte(2)
			if odd {
				es = 1
			}
			b = append(b, byte(g.TranslationType), byte(g.NumberingPlan)<<4|es, byte(g.NatureOfAddress&0x7F))
		default:
			return nil, fmt.Errorf("sccp: global title indicator %d not supported", ind)
		}
		b = append(b, digits...)
	}
	return append([]byte{ai}, b...), nil
}

func bcd(s string) ([]byte, bool, error) {
	var out []byte
	for i := 0; i < len(s); i += 2 {
		lo, err := nibble(s[i])
		if err != nil {
			return nil, false, err
		}
		hi := byte(0)
		if i+1 < len(s) {
			if hi, err = nibble(s[i+1]); err != nil {
				return nil, false, err
			}
		}
		out = append(out, hi<<4|lo)
	}
	return out, len(s)%2 == 1, nil
}

func nibble(c byte) (byte, error) {
	if c >= '0' && c <= '9' {
		return c - '0', nil
	}
	return 0, fmt.Errorf("sccp: %q is not a digit of a global title", c)
}

func unbcd(b []byte, odd bool) string {
	var s strings.Builder
	for i, x := range b {
		s.WriteByte('0' + x&0xF)
		if i == len(b)-1 && odd {
			break
		}
		if x>>4 <= 9 {
			s.WriteByte('0' + x>>4)
		}
	}
	return s.String()
}

func decodeAddress(b []byte) (Address, error) {
	if len(b) < 1 {
		return Address{}, ErrShort
	}
	ai := b[0]
	if ai&0x80 != 0 {
		return Address{}, errors.New("sccp: national address format not supported")
	}
	a := Address{RouteOnSSN: ai&0x40 != 0, PC: -1}
	p := b[1:]
	if ai&0x01 != 0 {
		if len(p) < 2 {
			return a, ErrShort
		}
		a.PC = int(p[0]) | int(p[1]&0x3F)<<8
		p = p[2:]
	}
	if ai&0x02 != 0 {
		if len(p) < 1 {
			return a, ErrShort
		}
		a.SSN = int(p[0])
		p = p[1:]
	}
	switch ind := int(ai>>2) & 0xF; ind {
	case 0:
	case 1:
		if len(p) < 1 {
			return a, ErrShort
		}
		a.GT = &GT{Indicator: 1, NatureOfAddress: int(p[0] & 0x7F), odd: p[0]&0x80 != 0}
		a.GT.Digits = unbcd(p[1:], a.GT.odd)
	case 2:
		if len(p) < 1 {
			return a, ErrShort
		}
		a.GT = &GT{Indicator: 2, TranslationType: int(p[0])}
		a.GT.Digits = unbcd(p[1:], false)
	case 4:
		if len(p) < 3 {
			return a, ErrShort
		}
		odd := p[1]&0xF == 1
		a.GT = &GT{Indicator: 4, TranslationType: int(p[0]), NumberingPlan: int(p[1] >> 4), NatureOfAddress: int(p[2] & 0x7F), odd: odd}
		a.GT.Digits = unbcd(p[3:], odd)
	default:
		return a, fmt.Errorf("sccp: global title indicator %d not supported", ind)
	}
	return a, nil
}

// Encode writes a message.
func (m Message) Encode() ([]byte, error) {
	cd, err := encodeAddress(m.Called)
	if err != nil {
		return nil, err
	}
	cg, err := encodeAddress(m.Calling)
	if err != nil {
		return nil, err
	}
	if len(cd) > 255 || len(cg) > 255 {
		return nil, errors.New("sccp: address too long")
	}
	var head []byte
	switch m.Type {
	case TypeUDT, TypeXUDT:
		pc := byte(m.Class & 0x0F)
		if m.ReturnOnError {
			pc |= 0x80
		}
		head = []byte{byte(m.Type), pc}
	case TypeUDTS, TypeXUDTS:
		head = []byte{byte(m.Type), byte(m.Cause)}
	default:
		return nil, fmt.Errorf("sccp: message type %#x not supported", m.Type)
	}
	extended := m.Type == TypeXUDT || m.Type == TypeXUDTS
	if extended {
		hc := m.HopCounter
		if hc <= 0 || hc > 15 {
			hc = 15
		}
		head = append(head, byte(hc))
	}
	if !extended && len(m.Data) > 255 {
		return nil, errors.New("sccp: data longer than 255 octets needs XUDT")
	}
	if len(m.Data) > 255 {
		return nil, errors.New("sccp: data longer than 255 octets (segmentation not supported)")
	}
	// Pointers, each from its own octet to the length octet of its part (Q.713 1.7).
	nptr := 3
	if extended {
		nptr = 4 // and an optional part, none
	}
	b := append([]byte(nil), head...)
	base := len(b)
	b = append(b, make([]byte, nptr)...)
	parts := [][]byte{cd, cg, m.Data}
	for i, part := range parts {
		b[base+i] = byte(len(b) - (base + i))
		b = append(b, byte(len(part)))
		b = append(b, part...)
	}
	if extended {
		b[base+3] = 0 // no optional part
	}
	return b, nil
}

// Decode reads a message.
func Decode(b []byte) (Message, error) {
	if len(b) < 2 {
		return Message{}, ErrShort
	}
	m := Message{Type: int(b[0])}
	var base int
	switch m.Type {
	case TypeUDT, TypeXUDT:
		m.Class, m.ReturnOnError = int(b[1]&0x0F), b[1]&0x80 != 0
	case TypeUDTS, TypeXUDTS:
		m.Cause = int(b[1])
	default:
		return m, fmt.Errorf("sccp: message type %#x not supported", m.Type)
	}
	base = 2
	if m.Type == TypeXUDT || m.Type == TypeXUDTS {
		if len(b) < 3 {
			return m, ErrShort
		}
		m.HopCounter = int(b[2])
		base = 3
	}
	part := func(i int) ([]byte, error) {
		at := base + i
		if at >= len(b) {
			return nil, ErrShort
		}
		start := at + int(b[at])
		if b[at] == 0 || start >= len(b) {
			return nil, ErrShort
		}
		n := int(b[start])
		if start+1+n > len(b) {
			return nil, ErrShort
		}
		return b[start+1 : start+1+n], nil
	}
	cd, err := part(0)
	if err != nil {
		return m, err
	}
	cg, err := part(1)
	if err != nil {
		return m, err
	}
	data, err := part(2)
	if err != nil {
		return m, err
	}
	if m.Called, err = decodeAddress(cd); err != nil {
		return m, err
	}
	if m.Calling, err = decodeAddress(cg); err != nil {
		return m, err
	}
	m.Data = append([]byte(nil), data...)
	return m, nil
}

// Return builds the service message that returns m to its sender with a cause (UDT -> UDTS,
// XUDT -> XUDTS), the addresses swapped; ok is false when m did not ask for it.
func Return(m Message, cause int) (Message, bool) {
	if !m.ReturnOnError || (m.Type != TypeUDT && m.Type != TypeXUDT) {
		return Message{}, false
	}
	t := TypeUDTS
	if m.Type == TypeXUDT {
		t = TypeXUDTS
	}
	return Message{Type: t, Cause: cause, HopCounter: 15, Called: m.Calling, Calling: m.Called, Data: m.Data}, true
}

// Translation is one global title translation: titles starting with Prefix (and of TranslationType,
// when that is not -1) go to point code PC, subsystem SSN (0: keep the message's own).
type Translation struct {
	Prefix          string `json:"prefix"`
	TranslationType int    `json:"translationType"`
	PC              int    `json:"pc"`
	SSN             int    `json:"ssn,omitempty"`
}

// Translate finds the longest prefix that matches a message's called global title and rewrites the
// called address to route on that point code and subsystem (the global title kept, as Q.714 2.4 does).
// ok is false when the address routes on its subsystem already or nothing matches.
func Translate(a Address, table []Translation) (Address, bool) {
	if a.RouteOnSSN || a.GT == nil {
		return a, false
	}
	best := -1
	for i, t := range table {
		if t.TranslationType >= 0 && t.TranslationType != a.GT.TranslationType {
			continue
		}
		if strings.HasPrefix(a.GT.Digits, t.Prefix) && (best < 0 || len(t.Prefix) > len(table[best].Prefix)) {
			best = i
		}
	}
	if best < 0 {
		return a, false
	}
	t := table[best]
	out := a
	out.RouteOnSSN, out.PC = true, t.PC
	if t.SSN > 0 {
		out.SSN = t.SSN
	}
	if out.SSN == 0 {
		return a, false
	}
	return out, true
}
