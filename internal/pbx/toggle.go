package pbx

import (
	"slices"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// toggles holds which day/night switches are switched over.
type toggles struct {
	mu sync.Mutex
	on map[string]bool
}

// ToggleOn says whether a toggle is switched over (calls go to its override destination).
func (e *Engine) ToggleOn(id string) bool {
	e.tog.mu.Lock()
	defer e.tog.mu.Unlock()
	return e.tog.on[id]
}

// TogglesOn lists the IDs of the toggles switched over.
func (e *Engine) TogglesOn() []string {
	e.tog.mu.Lock()
	defer e.tog.mu.Unlock()
	var out []string
	for id, on := range e.tog.on {
		if on {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// SetToggle switches a toggle over (on) or back; by says who (an extension, a user, the API). It is
// kept across restarts and lights or puts out the busy lamps watching it.
func (e *Engine) SetToggle(id string, on bool, by string) bool {
	t := e.Config().FindToggle(id)
	if t == nil {
		return false
	}
	e.tog.mu.Lock()
	if e.tog.on == nil {
		e.tog.on = map[string]bool{}
	}
	was := e.tog.on[id]
	if on {
		e.tog.on[id] = true
	} else {
		delete(e.tog.on, id)
	}
	e.tog.mu.Unlock()
	if was != on {
		e.Log.Info("toggle switched", "toggle", t.Name, "override", on, "by", by)
		e.publish("toggle.changed", map[string]any{"id": id, "name": t.Name, "override": on, "by": by})
		e.saveDiverts()
		go e.presenceChanged()
	}
	return true
}

// loadToggles restores the switched-over toggles, dropping removed ones.
func (e *Engine) loadToggles(ids []string) {
	cfg := e.Config()
	e.tog.mu.Lock()
	defer e.tog.mu.Unlock()
	e.tog.on = map[string]bool{}
	for _, id := range ids {
		if cfg.FindToggle(id) != nil {
			e.tog.on[id] = true
		}
	}
}

// toggleLamp finds the toggle a busy lamp key watches: the toggle code and its own code (*281).
func toggleLamp(cfg *config.Config, user string) *config.Toggle {
	code := cfg.System.Codes.Toggle
	if code == "" || code == "off" || len(user) <= len(code) || user[:len(code)] != code {
		return nil
	}
	return cfg.FindToggleCode(user[len(code):])
}

// toggleCode switches a toggle from a phone: the rising tone when it is now switched over, the falling
// one when it is back to normal.
func (c *Call) toggleCode(id string) {
	defer func() {
		time.Sleep(300 * time.Millisecond)
		c.hangup("toggle", "system")
	}()
	c.play(make([]int16, media.Rate/4), nil)
	c.mu.Lock()
	ext := c.callerExt
	c.mu.Unlock()
	on := !c.e.ToggleOn(id)
	if !c.e.SetToggle(id, on, "extension "+ext) {
		c.play(media.ToneSamples(media.ToneOff), nil)
		return
	}
	if on {
		c.play(media.ToneSamples(media.ToneConfirm), nil)
	} else {
		c.play(media.ToneSamples(media.ToneOff), nil)
	}
}
