package isdn

import (
	"errors"
	"strings"
)

// Q.931 message types.
const (
	MsgAlerting        = 0x01
	MsgCallProceeding  = 0x02
	MsgProgress        = 0x03
	MsgSetup           = 0x05
	MsgConnect         = 0x07
	MsgSetupAck        = 0x0D
	MsgConnectAck      = 0x0F
	MsgDisconnect      = 0x45
	MsgRelease         = 0x4D
	MsgReleaseComplete = 0x5A
	MsgRestart         = 0x46
	MsgRestartAck      = 0x4E
	MsgStatus          = 0x7D
	MsgStatusEnquiry   = 0x75
	MsgInformation     = 0x7B
	MsgNotify          = 0x6E
)

// Information elements.
const (
	ieBearer          = 0x04
	ieCause           = 0x08
	ieCallState       = 0x14
	ieChannelID       = 0x18
	ieProgress        = 0x1E
	ieDisplay         = 0x28
	ieConnectedNum    = 0x4C
	ieCallingNumber   = 0x6C
	ieCalledNumber    = 0x70
	ieRestartInd      = 0x79
	ieSendingComplete = 0xA1
)

// Cause values (Q.850) BitVoice uses.
const (
	CauseUnallocated        = 1
	CauseNormal             = 16
	CauseBusy               = 17
	CauseNoAnswer           = 19
	CauseRejected           = 21
	CauseInvalidNumber      = 28
	CauseStatusEnquiry      = 30
	CauseNormalUnspecified  = 31
	CauseNoCircuit          = 34
	CauseTemporaryFailure   = 41
	CauseChannelUnavailable = 44
	CauseNoCallReference    = 81
	CauseIncompatible       = 88
	CauseMandatoryMissing   = 96
	CauseWrongMessage       = 98
	CauseRecoveryTimer      = 102
)

// Message is one Q.931 message.
type Message struct {
	CallRef  int  // 15 bits
	FromSide bool // the call reference flag: set by the side that did not allocate the reference
	Type     byte
	IEs      []IE
}

// IE is one information element: Single octet ones have no Data.
type IE struct {
	ID   byte
	Data []byte
}

// ErrShort is returned for a message too short to be Q.931.
var ErrShort = errors.New("isdn: not a Q.931 message")

// Parse reads a Q.931 message (protocol discriminator 8, a call reference of 0 to 2 octets).
func Parse(b []byte) (*Message, error) {
	if len(b) < 3 || b[0] != 0x08 {
		return nil, ErrShort
	}
	crl := int(b[1] & 0x0F)
	if crl > 2 || len(b) < 3+crl {
		return nil, ErrShort
	}
	m := &Message{}
	if crl > 0 {
		m.FromSide = b[2]&0x80 != 0
		v := int(b[2] & 0x7F)
		for i := 1; i < crl; i++ {
			v = v<<8 | int(b[2+i])
		}
		m.CallRef = v
	}
	p := 2 + crl
	m.Type = b[p] & 0x7F
	p++
	codeset := 0
	for p < len(b) {
		id := b[p]
		if id&0x80 != 0 {
			// Single octet: a shift changes the codeset of what follows (locking), or of the next one.
			if id&0xF0 == 0x90 {
				if id&0x08 == 0 {
					codeset = int(id & 7)
				}
			} else if codeset == 0 {
				m.IEs = append(m.IEs, IE{ID: id})
			}
			p++
			continue
		}
		if p+1 >= len(b) {
			break
		}
		n := int(b[p+1])
		if p+2+n > len(b) {
			break
		}
		if codeset == 0 {
			m.IEs = append(m.IEs, IE{ID: id, Data: b[p+2 : p+2+n]})
		}
		p += 2 + n
	}
	return m, nil
}

// Bytes writes the message with a two-octet call reference (primary rate).
func (m *Message) Bytes() []byte {
	b := []byte{0x08, 0x02, byte(m.CallRef >> 8 & 0x7F), byte(m.CallRef)}
	if m.FromSide {
		b[2] |= 0x80
	}
	if m.CallRef == 0 {
		b = []byte{0x08, 0x02, 0, 0}
		if m.FromSide {
			b[2] = 0x80
		}
	}
	b = append(b, m.Type)
	for _, ie := range m.IEs {
		if ie.ID&0x80 != 0 {
			b = append(b, ie.ID)
			continue
		}
		b = append(b, ie.ID, byte(len(ie.Data)))
		b = append(b, ie.Data...)
	}
	return b
}

// Get returns an element's contents.
func (m *Message) Get(id byte) ([]byte, bool) {
	for _, ie := range m.IEs {
		if ie.ID == id {
			return ie.Data, true
		}
	}
	return nil, false
}

// GetAll returns every element with the id (a message may carry several Facility elements).
func (m *Message) GetAll(id byte) [][]byte {
	var out [][]byte
	for _, ie := range m.IEs {
		if ie.ID == id {
			out = append(out, ie.Data)
		}
	}
	return out
}

func (m *Message) add(id byte, data []byte) { m.IEs = append(m.IEs, IE{ID: id, Data: data}) }

// bearer is speech, 64 kbit/s circuit mode, G.711 at the line's law.
func bearer(alaw bool) []byte {
	l1 := byte(0xA2)
	if alaw {
		l1 = 0xA3
	}
	return []byte{0x80, 0x90, l1}
}

// channelID names one B-channel of a primary rate interface, exclusively or as preferred.
func channelID(ch int, exclusive bool) []byte {
	o3 := byte(0xA1)
	if exclusive {
		o3 |= 0x08
	}
	return []byte{o3, 0x83, 0x80 | byte(ch)}
}

// parseChannel reads a channel identification: the B-channel and whether only that one will do.
// ch is 0 when none is named ("any").
func parseChannel(d []byte) (ch int, exclusive bool, ok bool) {
	if len(d) < 1 {
		return 0, false, false
	}
	exclusive = d[0]&0x08 != 0
	if d[0]&0x03 == 0 {
		return 0, exclusive, true // no channel
	}
	if d[0]&0x03 == 3 {
		return 0, false, true // any channel
	}
	i := 1
	if d[0]&0x40 != 0 { // an interface identifier is present: skip it
		for i < len(d) && d[i]&0x80 == 0 {
			i++
		}
		i++
	}
	if i+1 >= len(d) {
		return 0, exclusive, false
	}
	return int(d[i+1] & 0x7F), exclusive, true
}

// Number type and plan: unknown/ISDN, or national/ISDN for ten-digit North American numbers.
func numberIE(digits string, calling bool) []byte {
	tonNPI := byte(0x01) // unknown, ISDN/telephony (E.164)
	if len(digits) == 10 || strings.HasPrefix(digits, "+") {
		tonNPI = 0x21 // national
		if strings.HasPrefix(digits, "+") {
			tonNPI = 0x11 // international
		}
	}
	digits = strings.TrimPrefix(digits, "+")
	if calling {
		return append([]byte{tonNPI, 0x80}, digits...) // presentation allowed, user provided, not screened
	}
	return append([]byte{0x80 | tonNPI}, digits...)
}

func parseNumber(d []byte) string {
	if len(d) < 1 {
		return ""
	}
	i := 1
	if d[0]&0x80 == 0 {
		i = 2 // octet 3a: presentation and screening
	}
	if i > len(d) {
		return ""
	}
	digits := string(d[i:])
	if (d[0]>>4)&7 == 1 {
		digits = "+" + digits // international
	}
	return digits
}

func causeIE(cause int, network bool) []byte {
	loc := byte(0x80) // user
	if network {
		loc = 0x82 // public network serving the local user
	}
	return []byte{loc, 0x80 | byte(cause)}
}

func parseCause(d []byte) int {
	i := 1
	if len(d) > 0 && d[0]&0x80 == 0 {
		i = 2
	}
	if i >= len(d) {
		return CauseNormal
	}
	return int(d[i] & 0x7F)
}
