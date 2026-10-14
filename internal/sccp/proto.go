// Package sccp lets Cisco desk phones running Skinny (SCCP) firmware use BitVoice. Skinny is a
// stimulus protocol: the phone reports keys, hook and soft keys, and the server drives its tones,
// lamps, display and audio channels. Each phone's first line is registered to the engine as one
// more device of its extension, through the same loopback bridge as browser phones, so it rings, is
// rung and transfers like any phone. Message layouts follow protocol version 11, which 79xx and
// later phones speak.
package sccp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// Messages, phone to server.
const (
	msgKeepAlive              = 0x0000
	msgRegister               = 0x0001
	msgIPPort                 = 0x0002
	msgKeypadButton           = 0x0003
	msgEnblocCall             = 0x0004
	msgStimulus               = 0x0005
	msgOffHook                = 0x0006
	msgOnHook                 = 0x0007
	msgSpeedDialStatReq       = 0x000A
	msgLineStatReq            = 0x000B
	msgConfigStatReq          = 0x000C
	msgTimeDateReq            = 0x000D
	msgButtonTemplateReq      = 0x000E
	msgVersionReq             = 0x000F
	msgCapabilitiesRes        = 0x0010
	msgServerReq              = 0x0012
	msgAlarm                  = 0x0020
	msgOpenReceiveChannelAck  = 0x0022
	msgSoftKeySetReq          = 0x0025
	msgSoftKeyEvent           = 0x0026
	msgUnregister             = 0x0027
	msgSoftKeyTemplateReq     = 0x0028
	msgRegisterAvailableLines = 0x002D
)

// Messages, server to phone.
const (
	msgRegisterAck         = 0x0081
	msgStartTone           = 0x0082
	msgStopTone            = 0x0083
	msgSetRinger           = 0x0085
	msgSetLamp             = 0x0086
	msgSetSpeakerMode      = 0x0088
	msgStartMediaTransmit  = 0x008A
	msgStopMediaTransmit   = 0x008B
	msgCallInfo            = 0x008F
	msgSpeedDialStat       = 0x0091
	msgLineStat            = 0x0092
	msgConfigStat          = 0x0093
	msgDefineTimeDate      = 0x0094
	msgButtonTemplate      = 0x0097
	msgVersion             = 0x0098
	msgCapabilitiesReq     = 0x009B
	msgRegisterReject      = 0x009D
	msgServerRes           = 0x009E
	msgKeepAliveAck        = 0x0100
	msgOpenReceiveChannel  = 0x0105
	msgCloseReceiveChannel = 0x0106
	msgSoftKeyTemplateRes  = 0x0108
	msgSoftKeySetRes       = 0x0109
	msgSelectSoftKeys      = 0x0110
	msgCallState           = 0x0111
	msgDisplayPromptStatus = 0x0112
	msgClearPromptStatus   = 0x0113
	msgActivateCallPlane   = 0x0116
	msgUnregisterAck       = 0x0118
	msgDialedNumber        = 0x011D
)

// Call states, tones, lamps, ringer modes, soft keys and soft key sets.
const (
	stateOffHook   = 1
	stateOnHook    = 2
	stateRingOut   = 3
	stateRingIn    = 4
	stateConnected = 5
	stateBusy      = 6

	toneDial    = 0x21
	toneBusy    = 0x23
	toneAlert   = 0x24 // ringback
	toneReorder = 0x25

	lampOff   = 1
	lampOn    = 2
	lampBlink = 5

	ringOff    = 1
	ringInside = 2

	stimulusRedial = 1
	stimulusLine   = 9

	keyRedial  = 1
	keyNewCall = 2
	keyEndCall = 9
	keyAnswer  = 11

	setOnHook    = 0
	setConnected = 1
	setRingIn    = 3
	setOffHook   = 4
	setRingOut   = 8

	codecULaw = 4 // Media_Payload_G711Ulaw64k
)

// maxMessage bounds what a phone may send in one message.
const maxMessage = 4096

var errFrame = errors.New("sccp: bad message")

// readMessage reads one message: a length (of the id and data), a reserved word, the id and data.
func readMessage(r io.Reader) (id uint32, data []byte, err error) {
	var h [12]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint32(h[0:])
	if n < 4 || n > maxMessage {
		return 0, nil, errFrame
	}
	id = binary.LittleEndian.Uint32(h[8:])
	data = make([]byte, n-4)
	_, err = io.ReadFull(r, data)
	return id, data, err
}

// builder assembles a message's data in little-endian words and fixed-size strings.
type builder struct{ b bytes.Buffer }

func (w *builder) u32(vs ...uint32) *builder {
	for _, v := range vs {
		_ = binary.Write(&w.b, binary.LittleEndian, v)
	}
	return w
}

func (w *builder) u8(vs ...uint8) *builder {
	w.b.Write(vs)
	return w
}

func (w *builder) str(s string, size int) *builder {
	b := make([]byte, size)
	copy(b, s)
	if len(s) >= size && size > 0 {
		b[size-1] = 0
	}
	w.b.Write(b)
	return w
}

func (w *builder) zero(n int) *builder {
	w.b.Write(make([]byte, n))
	return w
}

func frame(id uint32, data []byte) []byte {
	out := make([]byte, 12+len(data))
	binary.LittleEndian.PutUint32(out[0:], uint32(4+len(data)))
	binary.LittleEndian.PutUint32(out[8:], id)
	copy(out[12:], data)
	return out
}

func le32(b []byte, off int) uint32 {
	if off+4 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint32(b[off:])
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
