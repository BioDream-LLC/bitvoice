// Package dahdi drives telephony cards through Linux's DAHDI drivers, for bitvoice-dahdi: the
// separate program that joins a card's analogue ports to BitVoice. A port with a phone plugged in
// (an FXS port) becomes one more device of an extension; a port with a phone company's line (an
// FXO port) becomes a line of BitVoice's card trunk. The program speaks SIP to BitVoice on this
// computer, so BitVoice itself never touches the kernel driver.
//
// It is written from the DAHDI user interface (its ioctl numbers and the meaning of its events),
// called with the standard library's system calls: no C, no cgo.
package dahdi

import (
	"errors"
	"time"
)

// Hook operations (DAHDI_HOOK values).
const (
	HookOn   = 0
	HookOff  = 1
	HookWink = 2
	HookRing = 5
	RingOff  = 6
)

// Events (DAHDI_GETEVENT values).
const (
	EventOnHook      = 1
	EventRingOffHook = 2 // a phone was lifted (FXS port), or the far end answered (FXO, when supervised)
	EventWinkFlash   = 3
	EventAlarm       = 4
	EventNoAlarm     = 5
	EventRingerOn    = 10
	EventRingerOff   = 11
	EventHookDone    = 12
	EventPolarity    = 17
	EventRingBegin   = 18 // a line is ringing (FXO port)
)

// Tones the driver plays (DAHDI_SENDTONE values, from the zone dahdi_cfg loaded for the country).
const (
	ToneStop       = -1
	ToneDial       = 0
	ToneBusy       = 1
	ToneRingback   = 2
	ToneCongestion = 3
)

// FrameBytes is 20 ms of G.711 at 8 kHz: what each read and write carries.
const FrameBytes = 160

// ErrEvent is returned by Read when the driver has an event waiting (Event reads it).
var ErrEvent = errors.New("dahdi: event pending")

// Channel is one port of a card.
type Channel interface {
	// Read fills b with received audio (G.711, the channel's law) or returns ErrEvent.
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Event() (int, error)
	Hook(op int) error
	Tone(t int) error
	Close() error
}

// Open opens a DAHDI channel by number for 20 ms G.711 frames, µ-law or A-law.
func Open(number int, alaw bool) (Channel, error) { return openChannel(number, alaw) }

// eventPoll is how long a write waits when the driver's buffers are full.
const eventPoll = 5 * time.Millisecond
