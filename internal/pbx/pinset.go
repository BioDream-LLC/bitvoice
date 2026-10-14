package pbx

import (
	"crypto/subtle"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// pinOut asks for a PIN from the outbound route's PIN set, then places the call: three tries, and the
// PIN (when the set says so) goes into the call record as its account code.
func (c *Call) pinOut(p plan) {
	cfg := c.e.Config()
	set := cfg.FindPINSet(p.box)
	if set == nil || p.pinThen == nil {
		c.hangup("PIN set removed", "system")
		return
	}
	c.mu.Lock()
	ext, to := c.callerExt, c.to
	c.mu.Unlock()
	lang := c.e.langOf(cfg, ext)
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	good := ""
	for try := 0; try < 3 && good == ""; try++ {
		k, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "enter-pin")
		if ended {
			c.caller.ep.Record(nil)
			return
		}
		first := ""
		if k >= '0' && k <= '9' {
			first = string(k)
		}
		pin, ended := c.collectKeys(capt.Keys, first, 10, 6*time.Second)
		if ended {
			c.caller.ep.Record(nil)
			return
		}
		for _, want := range set.PINs {
			if subtle.ConstantTimeCompare([]byte(pin), []byte(want)) == 1 {
				good = want
			}
		}
		if good == "" {
			c.e.Log.Warn("outbound PIN wrong", "set", set.Name, "extension", ext, "to", to)
			if _, ended := c.sayPrompts(lang, nil, toneDeleted, "pin-wrong"); ended {
				c.caller.ep.Record(nil)
				return
			}
		}
	}
	c.caller.ep.Record(nil)
	if good == "" {
		c.e.publish("security.pinset", map[string]any{"set": set.Name, "extension": ext, "to": to, "result": "wrong PIN three times"})
		c.sayPrompts(lang, nil, media.ToneConfirm, "goodbye")
		c.hangup("outbound PIN wrong", "system")
		return
	}
	if set.Record {
		c.mu.Lock()
		c.account = good
		c.mu.Unlock()
	}
	if !c.ringAnswered(*p.pinThen) {
		c.hangup("no answer", "system")
	}
}
