// Package iax speaks IAX2 (RFC 5456), Asterisk's own protocol, so BitVoice can trunk to an Asterisk or
// FreePBX server, or to an IAX2 provider, without SIP. One UDP port carries signalling and audio for
// every call; full frames are acknowledged and resent, mini frames carry the audio.
package iax

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// Frame types.
const (
	TypeDTMFEnd   = 0x01
	TypeVoice     = 0x02
	TypeVideo     = 0x03
	TypeControl   = 0x04
	TypeNull      = 0x05
	TypeIAX       = 0x06
	TypeText      = 0x07
	TypeCNG       = 0x0A
	TypeDTMFBegin = 0x0C
)

// Control subclasses.
const (
	CtlHangup     = 0x01
	CtlRinging    = 0x03
	CtlAnswer     = 0x04
	CtlBusy       = 0x05
	CtlCongestion = 0x08
	CtlProceeding = 0x0E
	CtlHold       = 0x10
	CtlUnhold     = 0x11
)

// IAX subclasses.
const (
	IaxNew       = 0x01
	IaxPing      = 0x02
	IaxPong      = 0x03
	IaxAck       = 0x04
	IaxHangup    = 0x05
	IaxReject    = 0x06
	IaxAccept    = 0x07
	IaxAuthReq   = 0x08
	IaxAuthRep   = 0x09
	IaxInval     = 0x0A
	IaxLagRq     = 0x0B
	IaxLagRp     = 0x0C
	IaxRegReq    = 0x0D
	IaxRegAuth   = 0x0E
	IaxRegAck    = 0x0F
	IaxRegRej    = 0x10
	IaxRegRel    = 0x11
	IaxVNAK      = 0x12
	IaxPoke      = 0x1E
	IaxUnsupport = 0x21
	IaxCallToken = 0x28
)

// Information elements.
const (
	IECalledNumber  = 0x01
	IECallingNumber = 0x02
	IECallingName   = 0x04
	IECalledContext = 0x05
	IEUsername      = 0x06
	IECapability    = 0x08
	IEFormat        = 0x09
	IEVersion       = 0x0B
	IEDNID          = 0x0D
	IEAuthMethods   = 0x0E
	IEChallenge     = 0x0F
	IEMD5Result     = 0x10
	IEApparentAddr  = 0x12
	IERefresh       = 0x13
	IECause         = 0x16
	IEDateTime      = 0x1F
	IECauseCode     = 0x2A
	IECallToken     = 0x36
)

// Audio formats (a bit each, as IAX2 numbers them).
const (
	FormatGSM      = 1 << 1
	FormatULAW     = 1 << 2
	FormatALAW     = 1 << 3
	FormatG726     = 1 << 4 // RFC 3551 packing, as Asterisk's "g726"
	FormatG729     = 1 << 8
	FormatG726AAL2 = 1 << 11
	FormatG722     = 1 << 12
)

// AuthMD5 is the MD5 challenge and response, the method every IAX2 server offers.
const AuthMD5 = 2

// Frame is a full frame: everything but the audio of an established call.
type Frame struct {
	Src, Dst  uint16 // call numbers (15 bits)
	Retrans   bool
	TS        uint32
	OSeq      uint8
	ISeq      uint8
	Type      uint8
	Subclass  uint32 // for voice, the format bit; else the subclass number
	IEs       IEs    // for IAX frames
	Data      []byte // voice payload, text
	miniCodec bool
}

// IEs are a frame's information elements, by number; one value each.
type IEs map[uint8][]byte

// Str is an element as text.
func (e IEs) Str(id uint8) string { return string(e[id]) }

// U16 is a two-octet element.
func (e IEs) U16(id uint8) (uint16, bool) {
	if v := e[id]; len(v) == 2 {
		return binary.BigEndian.Uint16(v), true
	}
	return 0, false
}

// U32 is a four-octet element.
func (e IEs) U32(id uint8) (uint32, bool) {
	if v := e[id]; len(v) == 4 {
		return binary.BigEndian.Uint32(v), true
	}
	return 0, false
}

// SetStr sets a text element.
func (e IEs) SetStr(id uint8, s string) IEs { e[id] = []byte(s); return e }

// SetU16 sets a two-octet element.
func (e IEs) SetU16(id uint8, v uint16) IEs {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	e[id] = b
	return e
}

// SetU32 sets a four-octet element.
func (e IEs) SetU32(id uint8, v uint32) IEs {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	e[id] = b
	return e
}

// encodeSubclass packs a subclass: values above 127 are sent as a power of two with the C bit.
func encodeSubclass(v uint32) uint8 {
	if v < 0x80 {
		return uint8(v)
	}
	return 0x80 | uint8(bits.TrailingZeros32(v))
}

func decodeSubclass(b uint8) uint32 {
	if b&0x80 != 0 {
		return 1 << (b & 0x7F)
	}
	return uint32(b)
}

// Marshal encodes a full frame.
func (f *Frame) Marshal() []byte {
	b := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(b[0:], 0x8000|f.Src&0x7FFF)
	dst := f.Dst & 0x7FFF
	if f.Retrans {
		dst |= 0x8000
	}
	binary.BigEndian.PutUint16(b[2:], dst)
	binary.BigEndian.PutUint32(b[4:], f.TS)
	b[8], b[9], b[10] = f.OSeq, f.ISeq, f.Type
	b[11] = encodeSubclass(f.Subclass)
	if f.Type == TypeIAX {
		// The order of elements is free; a stable order makes frames easy to compare in traces.
		for id := 0; id < 256; id++ {
			v, ok := f.IEs[uint8(id)]
			if !ok {
				continue
			}
			b = append(b, uint8(id), uint8(len(v)))
			b = append(b, v...)
		}
		return b
	}
	return append(b, f.Data...)
}

// Errors from parsing.
var (
	ErrShort = errors.New("iax: frame too short")
	ErrIE    = errors.New("iax: malformed information element")
)

// Packet is what arrived: a full frame, or a mini frame's audio.
type Packet struct {
	Full *Frame
	// Mini frames: the sender's call number, the low 16 bits of the timestamp and the audio.
	MiniSrc uint16
	MiniTS  uint16
	Audio   []byte
}

// Parse decodes a packet.
func Parse(b []byte) (Packet, error) {
	if len(b) < 4 {
		return Packet{}, ErrShort
	}
	if b[0]&0x80 == 0 {
		if b[0] == 0 && b[1] == 0 {
			return Packet{}, fmt.Errorf("iax: meta frames (trunking, video) are not used")
		}
		return Packet{MiniSrc: binary.BigEndian.Uint16(b) & 0x7FFF, MiniTS: binary.BigEndian.Uint16(b[2:]), Audio: b[4:]}, nil
	}
	if len(b) < 12 {
		return Packet{}, ErrShort
	}
	f := &Frame{
		Src:     binary.BigEndian.Uint16(b) & 0x7FFF,
		Dst:     binary.BigEndian.Uint16(b[2:]) & 0x7FFF,
		Retrans: b[2]&0x80 != 0,
		TS:      binary.BigEndian.Uint32(b[4:]),
		OSeq:    b[8], ISeq: b[9], Type: b[10],
		Subclass: decodeSubclass(b[11]),
	}
	rest := b[12:]
	if f.Type == TypeIAX {
		f.IEs = IEs{}
		for len(rest) > 0 {
			if len(rest) < 2 || int(rest[1]) > len(rest)-2 {
				return Packet{}, ErrIE
			}
			f.IEs[rest[0]] = append([]byte(nil), rest[2:2+rest[1]]...)
			rest = rest[2+rest[1]:]
		}
	} else {
		f.Data = append([]byte(nil), rest...)
	}
	return Packet{Full: f}, nil
}

// MarshalMini encodes a mini frame of audio.
func MarshalMini(src uint16, ts uint32, audio []byte) []byte {
	b := make([]byte, 4, 4+len(audio))
	binary.BigEndian.PutUint16(b, src&0x7FFF)
	binary.BigEndian.PutUint16(b[2:], uint16(ts))
	return append(b, audio...)
}

// reliable says whether a full frame must be acknowledged (and is counted in the sequence numbers).
func (f *Frame) reliable() bool {
	if f.Type != TypeIAX {
		return true
	}
	switch f.Subclass {
	case IaxAck, IaxInval, IaxVNAK, IaxCallToken:
		return false
	}
	return true
}

// counted says whether a frame advances the sender's out sequence number (all but ACK and a few).
func (f *Frame) counted() bool {
	if f.Type != TypeIAX {
		return true
	}
	switch f.Subclass {
	case IaxAck, IaxInval, IaxVNAK, IaxCallToken:
		return false
	}
	return true
}
