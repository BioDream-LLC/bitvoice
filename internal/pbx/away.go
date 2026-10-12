package pbx

import (
	"slices"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Away is an extension that should not ring now: in a meeting or on time off (from its calendar), or
// set by hand. Its calls go to voicemail, or are forwarded, as the extension's calendar settings say.
type Away struct {
	Ext     string    `json:"ext"`
	Reason  string    `json:"reason"` // the meeting's title, "Holiday", "Do not disturb"
	Until   time.Time `json:"until,omitzero"`
	Source  string    `json:"source"`            // calendar or manual
	OffSite bool      `json:"offSite,omitempty"` // time off, not a meeting
}

type aways struct {
	mu sync.Mutex
	m  map[string]Away // by extension; a manual one wins over the calendar's
}

// SetAway marks an extension away (replacing what that source said before).
func (e *Engine) SetAway(a Away) {
	e.away.mu.Lock()
	if e.away.m == nil {
		e.away.m = map[string]Away{}
	}
	cur, had := e.away.m[a.Ext]
	if had && cur.Source == "manual" && a.Source == "calendar" {
		e.away.mu.Unlock()
		return // the person's own setting wins
	}
	changed := !had || cur != a
	e.away.m[a.Ext] = a
	e.away.mu.Unlock()
	if changed {
		e.publish("presence.changed", map[string]string{"extension": a.Ext})
	}
}

// ClearAway ends an extension's away set by source ("" for any).
func (e *Engine) ClearAway(ext, source string) {
	e.away.mu.Lock()
	cur, had := e.away.m[ext]
	if had && (source == "" || cur.Source == source) {
		delete(e.away.m, ext)
	}
	e.away.mu.Unlock()
	if had && (source == "" || cur.Source == source) {
		e.publish("presence.changed", map[string]string{"extension": ext})
	}
}

// Aways lists the extensions away now.
func (e *Engine) Aways() []Away {
	e.away.mu.Lock()
	defer e.away.mu.Unlock()
	now := time.Now()
	out := []Away{}
	for k, a := range e.away.m {
		if !a.Until.IsZero() && now.After(a.Until) {
			delete(e.away.m, k)
			continue
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y Away) int { return compareNumbers(x.Ext, y.Ext) })
	return out
}

// awayNow says whether an extension is away at this moment.
func (e *Engine) awayNow(ext string) (Away, bool) {
	e.away.mu.Lock()
	defer e.away.mu.Unlock()
	a, ok := e.away.m[ext]
	if ok && !a.Until.IsZero() && time.Now().After(a.Until) {
		delete(e.away.m, ext)
		return Away{}, false
	}
	return a, ok
}

// whileAway is where a call to an away extension goes: forwarded, or to its voicemail (else its
// no-answer destination). ok is false when it should ring as usual (calendar set to off).
func whileAway(x *config.Extension) (config.Destination, bool) {
	c := x.Calendar
	switch c.WhenBusy {
	case "forward":
		if c.ForwardTo != "" {
			if c.ForwardTo == x.Number {
				return config.Destination{}, false
			}
			return config.Destination{Type: "number", Target: c.ForwardTo}, true
		}
	case "ring":
		return config.Destination{}, false
	}
	if x.Voicemail.Enabled {
		return config.Destination{Type: "voicemail", Target: x.Number}, true
	}
	if x.NoAnswer != nil {
		return *x.NoAnswer, true
	}
	return config.Destination{}, false
}
