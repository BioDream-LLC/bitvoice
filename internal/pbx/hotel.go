package pbx

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// WakeUpBook stores wake-up calls. *store.Store implements it.
type WakeUpBook interface {
	WakeUps() []store.WakeUp
	PutWakeUp(w store.WakeUp) (store.WakeUp, error)
	DeleteWakeUp(ext string) error
	WakeUpFor(ext string) (store.WakeUp, bool)
}

// Guest is who is checked in to a hotel room, as the hotel system said.
type Guest struct {
	Room     string    `json:"room"` // the extension
	Name     string    `json:"name"`
	Account  string    `json:"account,omitempty"` // the hotel system's reservation or guest number
	Language string    `json:"language,omitempty"`
	Since    time.Time `json:"since"`
	Status   string    `json:"status,omitempty"` // the room status housekeeping last reported (1-9)
	StatusAt time.Time `json:"statusAt,omitzero"`
}

// guests holds the checked-in guests, saved through SaveGuests.
type guests struct {
	mu sync.Mutex
	m  map[string]Guest
}

// Guests lists the rooms with a guest checked in, or a room status.
func (e *Engine) Guests() []Guest {
	e.guests.mu.Lock()
	defer e.guests.mu.Unlock()
	out := make([]Guest, 0, len(e.guests.m))
	for _, g := range e.guests.m {
		out = append(out, g)
	}
	return out
}

// GuestIn says who is checked in to a room.
func (e *Engine) GuestIn(room string) (Guest, bool) {
	e.guests.mu.Lock()
	defer e.guests.mu.Unlock()
	g, ok := e.guests.m[room]
	return g, ok && !g.Since.IsZero()
}

// LoadGuests restores the guests saved at the last change.
func (e *Engine) LoadGuests(gs []Guest) {
	e.guests.mu.Lock()
	defer e.guests.mu.Unlock()
	e.guests.m = map[string]Guest{}
	for _, g := range gs {
		e.guests.m[g.Room] = g
	}
}

func (e *Engine) saveGuests() {
	if e.SaveGuests != nil {
		if err := e.SaveGuests(e.Guests()); err != nil {
			e.Log.Warn("guests not saved", "err", err)
		}
	}
}

// CheckIn checks a guest in to a room: the room may call out (when vacant rooms are barred) and
// shows the guest's name on calls it makes inside the hotel.
func (e *Engine) CheckIn(g Guest) error {
	cfg := e.Config()
	x := cfg.FindExtension(g.Room)
	if x == nil || !x.GuestRoom {
		return ErrNoExtension
	}
	e.guests.mu.Lock()
	if e.guests.m == nil {
		e.guests.m = map[string]Guest{}
	}
	old := e.guests.m[g.Room]
	if g.Since.IsZero() {
		g.Since = time.Now()
	}
	g.Status, g.StatusAt = old.Status, old.StatusAt
	e.guests.m[g.Room] = g
	e.guests.mu.Unlock()
	e.saveGuests()
	e.publish("hotel.checkin", g)
	return nil
}

// CheckOut checks the guest out of a room, and clears what the guest left when the settings say so.
func (e *Engine) CheckOut(room string) error {
	cfg := e.Config()
	x := cfg.FindExtension(room)
	if x == nil || !x.GuestRoom {
		return ErrNoExtension
	}
	e.guests.mu.Lock()
	g := e.guests.m[room]
	st, stAt := g.Status, g.StatusAt
	delete(e.guests.m, room)
	if st != "" {
		e.guests.m[room] = Guest{Room: room, Status: st, StatusAt: stAt}
	}
	e.guests.mu.Unlock()
	e.saveGuests()
	if cfg.System.Hotel.CheckOutClear {
		if e.WakeUps != nil {
			_ = e.WakeUps.DeleteWakeUp(room)
		}
		_ = e.SetDivert(Divert{Ext: room})
		e.SetDND(room, false)
		if e.Mail != nil {
			if vs, err := e.Mail.Voicemails(room); err == nil {
				for _, v := range vs {
					_ = e.Mail.DeleteVoicemail(room, v.ID)
				}
			}
			e.MailboxChanged(room)
		}
	}
	e.publish("hotel.checkout", map[string]string{"room": room, "name": g.Name})
	return nil
}

// SetRoomStatus records the status housekeeping reported for a room, and tells the hotel system.
func (e *Engine) SetRoomStatus(room, status string) {
	e.guests.mu.Lock()
	if e.guests.m == nil {
		e.guests.m = map[string]Guest{}
	}
	g := e.guests.m[room]
	g.Room, g.Status, g.StatusAt = room, status, time.Now()
	e.guests.m[room] = g
	e.guests.mu.Unlock()
	e.saveGuests()
	e.publish("hotel.roomstatus", map[string]string{"room": room, "status": status})
	if e.PMSEvent != nil {
		e.PMSEvent("roomstatus", map[string]string{"room": room, "status": status})
	}
}

// vacantBarred says whether a room may not call out because nobody is checked in.
func (e *Engine) vacantBarred(cfg *config.Config, x *config.Extension) bool {
	if x == nil || !x.GuestRoom || !cfg.System.Hotel.BarVacant {
		return false
	}
	_, in := e.GuestIn(x.Number)
	return !in
}

// wakeUpCode is *62 on a phone: hear the wake-up call set, book one as four digits (hhmm), or press
// star to cancel it.
func (c *Call) wakeUpCode() {
	e := c.e
	cfg := e.Config()
	c.mu.Lock()
	ext := c.callerExt
	c.mu.Unlock()
	if e.WakeUps == nil || ext == "" {
		c.hangup("wake-up calls are not kept here", "system")
		return
	}
	lang := e.langOf(cfg, ext)
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	defer c.caller.ep.Record(nil)
	loc := cfg.System.Location()
	if w, ok := e.WakeUps.WakeUpFor(ext); ok {
		if _, ended := c.sayPrompts(lang, nil, media.ToneConfirm, append([]string{"wakeup-set"}, prompts.Digits(w.At.In(loc).Format("1504"))...)...); ended {
			return
		}
	} else if _, ended := c.sayPrompts(lang, nil, toneNoMessages, "wakeup-none"); ended {
		return
	}
	for try := 0; try < 3; try++ {
		k, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "wakeup-menu")
		if ended {
			return
		}
		if k == 0 {
			select {
			case k = <-capt.Keys:
			case <-time.After(6 * time.Second):
			case <-c.cancelled:
				return
			}
		}
		if k == '*' {
			_ = e.WakeUps.DeleteWakeUp(ext)
			e.publish("wakeup.changed", map[string]string{"extension": ext})
			c.sayPrompts(lang, nil, toneDeleted, "wakeup-cancelled")
			c.hangup("wake-up call cancelled", "system")
			return
		}
		if k < '0' || k > '9' {
			continue
		}
		hhmm, ended := c.collectKeys(capt.Keys, string(k), 4, 5*time.Second)
		if ended {
			return
		}
		at, ok := nextTime(hhmm, time.Now().In(loc))
		if !ok {
			if _, ended := c.sayPrompts(lang, nil, toneDeleted, "invalid"); ended {
				return
			}
			continue
		}
		if _, err := e.WakeUps.PutWakeUp(store.WakeUp{Extension: ext, At: at, By: "phone"}); err != nil {
			c.hangup("wake-up call not saved", "system")
			return
		}
		e.publish("wakeup.changed", map[string]string{"extension": ext})
		if e.PMSEvent != nil {
			e.PMSEvent("wakeup.set", map[string]string{"room": ext, "at": at.Format(time.RFC3339)})
		}
		c.sayPrompts(lang, nil, media.ToneConfirm, append([]string{"wakeup-set"}, prompts.Digits(hhmm)...)...)
		c.hangup("wake-up call set", "system")
		return
	}
	c.hangup("wake-up call: nothing entered", "system")
}

// nextTime is the next time of day hhmm happens after now (today, or tomorrow when it has passed).
func nextTime(hhmm string, now time.Time) (time.Time, bool) {
	if len(hhmm) != 4 {
		return time.Time{}, false
	}
	h, err1 := strconv.Atoi(hhmm[:2])
	m, err2 := strconv.Atoi(hhmm[2:])
	if err1 != nil || err2 != nil || h > 23 || m > 59 {
		return time.Time{}, false
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, true
}

// roomStatusCode is *68 then a digit: housekeeping reports the status of the room they call from.
func (c *Call) roomStatusCode(status string) {
	c.mu.Lock()
	ext := c.callerExt
	c.mu.Unlock()
	c.e.SetRoomStatus(ext, status)
	c.play(media.ToneSamples(media.ToneConfirm), nil)
	c.hangup("room status "+status, "system")
}

// langOf is the language an extension hears prompts in: a checked-in guest's, the extension's, the system's.
func (e *Engine) langOf(cfg *config.Config, ext string) string {
	if g, ok := e.GuestIn(ext); ok && g.Language != "" {
		return g.Language
	}
	if x := cfg.FindExtension(ext); x != nil && x.Language != "" {
		return x.Language
	}
	return promptLang(cfg, "")
}

// wakeUpLoop rings the wake-up calls that are due, each in a call of its own.
func (e *Engine) wakeUpLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	busy := map[string]bool{}
	var mu sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if e.WakeUps == nil || (e.Cluster != nil && !e.Cluster.Leader()) {
			continue
		}
		for _, w := range e.WakeUps.WakeUps() {
			mu.Lock()
			if busy[w.Extension] || time.Now().Before(w.At) {
				mu.Unlock()
				continue
			}
			busy[w.Extension] = true
			mu.Unlock()
			go func(w store.WakeUp) {
				e.ringWakeUp(w)
				mu.Lock()
				delete(busy, w.Extension)
				mu.Unlock()
			}(w)
		}
	}
}

// ringWakeUp makes one try of a wake-up call, then books the next try, the next day (daily), or
// clears it; after the last unanswered try the hotel's reception is rung.
func (e *Engine) ringWakeUp(w store.WakeUp) {
	cfg := e.Config()
	h := cfg.System.Hotel
	answered := e.playToExtension(cfg, w.Extension, "Wake-up call", "wakeup-call", 45*time.Second)
	w.Tries++
	w.LastAt = time.Now()
	outcome := map[bool]string{true: "answered", false: "no answer"}[answered]
	w.Last = outcome
	e.Log.Info("wake-up call", "extension", w.Extension, "outcome", outcome, "try", w.Tries)
	e.publish("wakeup.rung", map[string]any{"extension": w.Extension, "outcome": outcome, "try": w.Tries})
	if e.PMSEvent != nil {
		e.PMSEvent("wakeup.result", map[string]string{"room": w.Extension, "outcome": outcome})
	}
	switch {
	case !answered && w.Tries < h.Tries():
		w.At = time.Now().Add(time.Duration(h.RetryMinutes()) * time.Minute)
		_, _ = e.WakeUps.PutWakeUp(w)
		return
	case !answered && h.Notify != "":
		go e.playToExtension(cfg, h.Notify, "Wake-up not answered: "+w.Extension, "wakeup-call", 45*time.Second)
	}
	if w.Daily {
		first := w.At
		for !first.After(time.Now()) {
			first = first.AddDate(0, 0, 1)
		}
		w.At, w.Tries = first, 0
		_, _ = e.WakeUps.PutWakeUp(w)
		return
	}
	_ = e.WakeUps.DeleteWakeUp(w.Extension)
	e.publish("wakeup.changed", map[string]string{"extension": w.Extension})
}

// playToExtension rings an extension's phones with the caller name shown, says a prompt (three times)
// when it is answered, then hangs up. It reports whether it was answered.
func (e *Engine) playToExtension(cfg *config.Config, ext, name, prompt string, ring time.Duration) bool {
	x := cfg.FindExtension(ext)
	if x == nil {
		return false
	}
	targets := e.phoneTargets(cfg, x)
	if len(targets) == 0 {
		return false
	}
	h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: cfg.System.Name, fromName: name,
		answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		xfer: make(chan *Leg, 1)}
	go h.run(plan{stages: []stage{{targets: targets, timeout: ring}}, direction: "internal"})
	leg := <-h.xfer
	if leg == nil {
		return false
	}
	c := &Call{e: e, ID: config.NewID(), start: time.Now(), answered: make(chan struct{}),
		cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		from: cfg.System.Name, fromName: name, callerExt: x.Number, to: x.Number, toName: x.Name, direction: "internal"}
	leg.call = c
	leg.Number, leg.Name, leg.Ext = x.Number, x.Name, x.Number
	c.caller = leg
	c.localAnswer = true
	c.answer = time.Now()
	e.mu.Lock()
	e.calls[c.ID] = c
	e.mu.Unlock()
	e.publish("call.started", c.info())
	lang := e.langOf(cfg, ext)
	pcm, ok := e.promptSamples(cfg, lang, prompt)
	if !ok {
		pcm = media.ToneSamples(media.Tone{Freqs: []float64{880, 1320}, Level: 0.2, On: 500 * time.Millisecond, Off: 500 * time.Millisecond, Repeat: 6})
	}
	for range 3 {
		if _, ended := c.play(pcm, nil); ended {
			break
		}
		if _, ended := c.play(make([]int16, media.Rate), nil); ended {
			break
		}
	}
	c.hangup("wake-up call played", "system")
	return true
}
