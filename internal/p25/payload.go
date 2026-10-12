// Package p25 speaks Project 25's wireline interfaces: the ISSI (TIA-102.BACA, between RF
// subsystems) and the CSSI (between a console subsystem and an RF subsystem), which carry group
// calls over SIP and their voice and push-to-talk over RTP, IMBE voice frames in the "P25 block"
// payload described here.
//
// The block layouts follow NIST's ISSI reference implementation (DIETS, the P25 ISSI tester, a
// work of the US government in the public domain), and were checked against captures of ISSI
// equipment that come with it.
package p25

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Block types (the 7-bit Block PT of a block header, E=1).
const (
	BlockIMBE       = 0  // an IMBE voice frame
	BlockPacketType = 1  // the ISSI packet type: what the packet is (always first)
	BlockHeaderWord = 5  // the ISSI header word: group, encryption, NID
	BlockPTT        = 11 // the PTT control word: who is talking
	BlockConsolePTT = 15 // the CSSI console PTT control word
)

// Packet types (the ISSI packet type block's PT).
const (
	PTTRequest  = 0
	PTTGrant    = 1
	PTTProgress = 2
	PTTEnd      = 3
	PTTStart    = 4
	PTTMute     = 5
	PTTUnmute   = 6
	PTTWait     = 7
	PTTDeny     = 8
	Heartbeat   = 9
	HeartbeatQ  = 10 // heartbeat query
	HeartbeatC  = 11 // heartbeat connection
	HeartbeatM  = 12 // heartbeat, mute transmission
	HeartbeatU  = 13 // heartbeat, unmute transmission
)

// PacketTypeName names a packet type for logs.
func PacketTypeName(pt int) string {
	names := []string{"PTT transmit request", "PTT transmit grant", "PTT transmit progress", "PTT transmit end",
		"PTT transmit start", "PTT transmit mute", "PTT transmit unmute", "PTT transmit wait", "PTT transmit deny",
		"heartbeat", "heartbeat query", "heartbeat connection", "heartbeat mute", "heartbeat unmute"}
	if pt >= 0 && pt < len(names) {
		return names[pt]
	}
	return fmt.Sprintf("packet type %d", pt)
}

// ServiceOptions are a call's options: emergency, protected (encrypted), duplex, mode, priority 0-7.
type ServiceOptions struct {
	Emergency, Protected, Duplex, Packet bool
	Priority                             int
}

func (s ServiceOptions) byte() byte {
	var b byte
	if s.Emergency {
		b |= 1 << 7
	}
	if s.Protected {
		b |= 1 << 6
	}
	if s.Duplex {
		b |= 1 << 5
	}
	if s.Packet {
		b |= 1 << 4
	}
	return b | byte(s.Priority&7)
}

func optionsOf(b byte) ServiceOptions {
	return ServiceOptions{Emergency: b&0x80 != 0, Protected: b&0x40 != 0, Duplex: b&0x20 != 0, Packet: b&0x10 != 0, Priority: int(b & 7)}
}

// PacketType is the ISSI packet type block: what the packet means, a transmission sequence number
// for the talk spurt, and the heartbeat interval.
type PacketType struct {
	M        bool // mute
	Type     int
	Options  ServiceOptions
	TSN      int // transmission sequence number, 0-127
	L        bool
	Interval int // heartbeat interval, seconds
}

func (p PacketType) bytes() []byte {
	v := uint32(0)
	if p.M {
		v = 1
	}
	v = v<<7 | uint32(p.Type&0x7f)
	v = v<<8 | uint32(p.Options.byte())
	v = v<<7 | uint32(p.TSN&0x7f)
	v <<= 1
	if p.L {
		v |= 1
	}
	v = v<<8 | uint32(p.Interval&0xff)
	return binary.BigEndian.AppendUint32(nil, v)
}

func packetTypeOf(b []byte) PacketType {
	v := binary.BigEndian.Uint32(b)
	return PacketType{M: v>>31 != 0, Type: int(v >> 24 & 0x7f), Options: optionsOf(byte(v >> 16)), TSN: int(v >> 9 & 0x7f),
		L: v>>8&1 != 0, Interval: int(v & 0xff)}
}

// Unit names a radio or console in P25: WACN (20 bits), system (12 bits) and unit (24 bits).
type Unit struct {
	WACN, System, ID uint32
}

func (u Unit) String() string { return fmt.Sprintf("%05x.%03x.%06x", u.WACN, u.System, u.ID) }

// PTTWord is the PTT control word: the unit talking (or asking to) and its transmit priority.
type PTTWord struct {
	Unit     Unit
	Priority int // transmit priority: 0-255 (a larger number wins)
}

func (w PTTWord) bytes() []byte {
	v := uint64(w.Unit.WACN&0xfffff)<<44 | uint64(w.Unit.System&0xfff)<<32 | uint64(w.Unit.ID&0xffffff)<<8 | uint64(w.Priority&0xff)
	return binary.BigEndian.AppendUint64(nil, v)
}

func pttWordOf(b []byte) PTTWord {
	v := binary.BigEndian.Uint64(b)
	return PTTWord{Unit: Unit{WACN: uint32(v >> 44 & 0xfffff), System: uint32(v >> 32 & 0xfff), ID: uint32(v >> 8 & 0xffffff)}, Priority: int(v & 0xff)}
}

// ConsolePTTWord is the CSSI console PTT control word: the PTT word, the console (56 bits) and
// its transmission descriptor, and the voice source and its console transmit priority.
type ConsolePTTWord struct {
	PTTWord
	Console     uint64 // 56 bits
	Descriptor  int
	VoiceSource uint64 // 56 bits
	ConsolePrio int
}

func (w ConsolePTTWord) bytes() []byte {
	b := w.PTTWord.bytes()
	b = binary.BigEndian.AppendUint64(b, (w.Console&0xffffffffffffff)<<8|uint64(w.Descriptor&0xff))
	return binary.BigEndian.AppendUint64(b, (w.VoiceSource&0xffffffffffffff)<<8|uint64(w.ConsolePrio&0xff))
}

func consolePTTOf(b []byte) ConsolePTTWord {
	w := ConsolePTTWord{PTTWord: pttWordOf(b[:8])}
	v := binary.BigEndian.Uint64(b[8:])
	w.Console, w.Descriptor = v>>8, int(v&0xff)
	v = binary.BigEndian.Uint64(b[16:])
	w.VoiceSource, w.ConsolePrio = v>>8, int(v&0xff)
	return w
}

// HeaderWord is the ISSI header word, sent with the voice of a talk spurt: the encryption message
// indicator, algorithm and key, the manufacturer, the talkgroup and the network ID.
type HeaderWord struct {
	MI    [9]byte
	AlgID int // 0x80: clear (not encrypted)
	KeyID int
	MFID  int
	Group int
	NID   int
	SF    int
	VBB   int
	Rsvd  int
}

func (h HeaderWord) bytes() []byte {
	b := append([]byte(nil), h.MI[:]...)
	b = append(b, byte(h.AlgID), byte(h.KeyID>>8), byte(h.KeyID), byte(h.MFID), byte(h.Group>>8), byte(h.Group), byte(h.NID>>8), byte(h.NID))
	return append(b, byte(h.SF&3)<<6|byte(h.VBB&3)<<4|byte(h.Rsvd&0xf))
}

func headerWordOf(b []byte) HeaderWord {
	h := HeaderWord{AlgID: int(b[9]), KeyID: int(b[10])<<8 | int(b[11]), MFID: int(b[12]), Group: int(b[13])<<8 | int(b[14]),
		NID: int(b[15])<<8 | int(b[16]), SF: int(b[17] >> 6), VBB: int(b[17] >> 4 & 3), Rsvd: int(b[17] & 0xf)}
	copy(h.MI[:], b[:9])
	return h
}

// Clear is the algorithm ID of unencrypted voice.
const Clear = 0x80

// IMBEBlock is one IMBE voice frame as the ISSI carries it: its frame type (the frame's place in
// the over-the-air superframe, 0xC2-0xCC or 0x62-0x73 for CAI frames), its 88 information bits as
// the vectors u0-u7 (no forward error correction on the wire), the error counts the RF side saw,
// and frame-type-specific data (link control, encryption sync, low-speed data).
type IMBEBlock struct {
	FT     int
	U      [8]uint16 // u0-u3 12 bits, u4-u6 11 bits, u7 7 bits
	Et, Er int       // total and correctable errors
	Mute   bool
	Lost   bool
	E4     int
	E1     int
	SF     int
	Rsvd   int
	Extra  []byte
}

// Info is the frame's 88 information bits, packed (11 bytes), u0 first, most significant bit first.
func (f IMBEBlock) Info() []byte {
	widths := [8]int{12, 12, 12, 12, 11, 11, 11, 7}
	out := make([]byte, 11)
	pos := 0
	for v, w := range widths {
		for i := w - 1; i >= 0; i-- {
			if f.U[v]>>i&1 != 0 {
				out[pos/8] |= 0x80 >> (pos % 8)
			}
			pos++
		}
	}
	return out
}

// SetInfo sets u0-u7 from 88 packed information bits.
func (f *IMBEBlock) SetInfo(b []byte) {
	widths := [8]int{12, 12, 12, 12, 11, 11, 11, 7}
	pos := 0
	for v, w := range widths {
		f.U[v] = 0
		for range w {
			f.U[v] = f.U[v]<<1 | uint16(b[pos/8]>>(7-pos%8)&1)
			pos++
		}
	}
}

func (f IMBEBlock) bytes() []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(f.FT&0xff)<<24|uint32(f.U[0]&0xfff)<<12|uint32(f.U[1]&0xfff))
	v := uint64(f.U[2]&0xfff)<<52 | uint64(f.U[3]&0xfff)<<40 | uint64(f.U[4]&0x7ff)<<29 | uint64(f.U[5]&0x7ff)<<18 |
		uint64(f.U[6]&0x7ff)<<7 | uint64(f.U[7]&0x7f)
	b = binary.BigEndian.AppendUint64(b, v)
	w := uint16(f.Et&7)<<13 | uint16(f.Er&7)<<10 | uint16(f.E4&1)<<7 | uint16(f.E1&7)<<4 | uint16(f.SF&3)<<2 | uint16(f.Rsvd&3)
	if f.Mute {
		w |= 1 << 9
	}
	if f.Lost {
		w |= 1 << 8
	}
	b = binary.BigEndian.AppendUint16(b, w)
	return append(b, f.Extra...)
}

func imbeOf(b []byte) IMBEBlock {
	v := binary.BigEndian.Uint32(b)
	f := IMBEBlock{FT: int(v >> 24)}
	f.U[0], f.U[1] = uint16(v>>12&0xfff), uint16(v&0xfff)
	u := binary.BigEndian.Uint64(b[4:])
	f.U[2], f.U[3], f.U[4], f.U[5], f.U[6], f.U[7] = uint16(u>>52&0xfff), uint16(u>>40&0xfff), uint16(u>>29&0x7ff), uint16(u>>18&0x7ff), uint16(u>>7&0x7ff), uint16(u&0x7f)
	w := binary.BigEndian.Uint16(b[12:])
	f.Et, f.Er, f.Mute, f.Lost = int(w>>13), int(w>>10&7), w>>9&1 != 0, w>>8&1 != 0
	f.E4, f.E1, f.SF, f.Rsvd = int(w>>7&1), int(w>>4&7), int(w>>2&3), int(w&3)
	if len(b) > 14 {
		f.Extra = append([]byte(nil), b[14:]...)
	}
	return f
}

// Payload is a P25 block payload: the RTP payload of ISSI and CSSI voice and PTT packets.
type Payload struct {
	S          bool // the control octet's signal bit
	Packet     PacketType
	PTT        *PTTWord
	ConsolePTT *ConsolePTTWord
	Header     *HeaderWord
	Voice      []IMBEBlock
	// Other holds blocks of types not handled here (manufacturer specific, ...), by type.
	Other map[int][]byte
}

// Marshal makes the RTP payload: the control octet, one header per block, then the blocks, in the
// order the ISSI uses (packet type, PTT word, console PTT word, header word, voice).
func (p *Payload) Marshal() []byte {
	type block struct {
		bt   int
		body []byte
	}
	blocks := []block{{BlockPacketType, p.Packet.bytes()}}
	if p.PTT != nil {
		blocks = append(blocks, block{BlockPTT, p.PTT.bytes()})
	}
	if p.ConsolePTT != nil {
		blocks = append(blocks, block{BlockConsolePTT, p.ConsolePTT.bytes()})
	}
	if p.Header != nil {
		blocks = append(blocks, block{BlockHeaderWord, p.Header.bytes()})
	}
	for _, v := range p.Voice {
		blocks = append(blocks, block{BlockIMBE, v.bytes()})
	}
	out := []byte{byte(len(blocks) & 0x3f)}
	if p.S {
		out[0] |= 0x80
	}
	for _, b := range blocks {
		out = binary.BigEndian.AppendUint32(out, 1<<31|uint32(b.bt&0x7f)<<24|uint32(len(b.body)&0x3ff))
	}
	for _, b := range blocks {
		out = append(out, b.body...)
	}
	return out
}

// ErrShort is a payload cut short.
var ErrShort = errors.New("p25: payload cut short")

// Parse reads an RTP payload of P25 blocks.
func Parse(b []byte) (*Payload, error) {
	if len(b) < 1 {
		return nil, ErrShort
	}
	p := &Payload{S: b[0]&0x80 != 0}
	n := int(b[0] & 0x3f)
	if b[0]&0x40 != 0 {
		return nil, errors.New("p25: compact block headers are not used on the ISSI")
	}
	if len(b) < 1+4*n {
		return nil, ErrShort
	}
	type hdr struct{ bt, size int }
	hs := make([]hdr, n)
	for i := range hs {
		v := binary.BigEndian.Uint32(b[1+4*i:])
		hs[i] = hdr{int(v >> 24 & 0x7f), int(v & 0x3ff)}
	}
	off := 1 + 4*n
	gotType := false
	for _, h := range hs {
		if off+h.size > len(b) {
			return nil, ErrShort
		}
		body := b[off : off+h.size]
		off += h.size
		switch {
		case h.bt == BlockPacketType && h.size >= 4:
			p.Packet, gotType = packetTypeOf(body), true
		case h.bt == BlockPTT && h.size >= 8:
			w := pttWordOf(body)
			p.PTT = &w
		case h.bt == BlockConsolePTT && h.size >= 24:
			w := consolePTTOf(body)
			p.ConsolePTT = &w
		case h.bt == BlockHeaderWord && h.size >= 18:
			w := headerWordOf(body)
			p.Header = &w
		case h.bt == BlockIMBE && h.size >= 14:
			p.Voice = append(p.Voice, imbeOf(body))
		default:
			if p.Other == nil {
				p.Other = map[int][]byte{}
			}
			p.Other[h.bt] = append([]byte(nil), body...)
		}
	}
	if !gotType {
		return nil, errors.New("p25: no packet type block")
	}
	return p, nil
}
