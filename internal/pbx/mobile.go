package pbx

import (
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Mobile apps: an extension with paired phones running the BitVoice app is rung even when no phone is
// registered. BitVoice asks Mobile to wake the apps, keeps the extension's stage ringing, and rings each
// app as soon as it connects (a "late" fork into the call already ringing).

// Mobile wakes an extension's apps. The API server implements it with the push relay or Apple and Google.
type Mobile interface {
	HasDevices(ext string) bool
	Wake(ext string, call CallInfo)
}

// lateRing is a call waiting for an extension's app to connect.
type lateRing struct {
	call  *Call
	stage int
	name  string
	until time.Time
}

type lateRings struct {
	mu sync.Mutex
	by map[string][]lateRing // by extension
}

// wake asks the apps of ext to connect for this call's stage.
func (c *Call) wake(ext, name string, stage int, ring time.Duration) {
	e := c.e
	if e.Mobile == nil {
		return
	}
	e.late.mu.Lock()
	if e.late.by == nil {
		e.late.by = map[string][]lateRing{}
	}
	now := time.Now()
	kept := e.late.by[ext][:0]
	for _, l := range e.late.by[ext] {
		if now.Before(l.until) {
			kept = append(kept, l)
		}
	}
	e.late.by[ext] = append(kept, lateRing{call: c, stage: stage, name: name, until: now.Add(ring)})
	e.late.mu.Unlock()
	go e.Mobile.Wake(ext, c.info())
}

// registered rings a newly registered binding into every call waiting for its extension.
func (e *Engine) registered(ext string, b *Binding) {
	e.late.mu.Lock()
	waiting := append([]lateRing(nil), e.late.by[ext]...)
	e.late.mu.Unlock()
	now := time.Now()
	for _, l := range waiting {
		c := l.call
		c.mu.Lock()
		live := !c.ended && c.callee == nil && c.stageNo == l.stage && now.Before(l.until)
		c.mu.Unlock()
		if live {
			c.startFork(target{ext: ext, name: l.name, binding: b}, l.stage)
		}
	}
}

// toggleEmergencyMode switches emergency mode from a phone: two rising tones for on, falling for off,
// a low tone when it could not be changed.
func (c *Call) toggleEmergencyMode() {
	on := !c.e.Config().System.EmergencyMode.On
	tone := media.ToneSamples(media.ToneConfirm)
	if c.e.SetEmergencyMode == nil {
		tone = media.ToneSamples(media.ToneError)
	} else if err := c.e.SetEmergencyMode(on, "extension "+c.callerExt); err != nil {
		c.e.Log.Warn("emergency mode not changed", "err", err)
		tone = media.ToneSamples(media.ToneError)
	} else if !on {
		tone = media.ToneSamples(media.ToneOff)
	}
	c.play(tone, nil)
	time.Sleep(300 * time.Millisecond)
	c.hangup("emergency mode switched", "system")
}
