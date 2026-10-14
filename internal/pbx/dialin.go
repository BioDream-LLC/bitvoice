package pbx

import (
	"crypto/subtle"
	"slices"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// runDialIn (DISA) checks who is calling in, then lets them dial a number and calls it as the
// dial-in's extension. Three wrong PINs end the call; every attempt is logged.
func (c *Call) runDialIn(id string) {
	cfg := c.e.Config()
	d := cfg.FindDialIn(id)
	if d == nil {
		c.hangup("dial-in removed", "system")
		return
	}
	lang := promptLang(cfg, d.Language)
	c.mu.Lock()
	from := c.from
	c.mu.Unlock()
	if len(d.AllowedCallers) > 0 && !slices.ContainsFunc(d.AllowedCallers, func(n string) bool { return sameNumber(n, from) > 0 }) {
		c.e.Log.Warn("dial-in refused: caller not allowed", "dialin", d.Name, "from", from)
		c.e.publish("security.dialin", map[string]any{"dialin": d.Name, "from": from, "result": "caller not allowed"})
		c.sayPrompts(lang, nil, toneDeleted, "goodbye")
		c.hangup("dial-in: caller not allowed", "system")
		return
	}
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	defer c.caller.ep.Record(nil)
	if d.PIN != "" {
		ok := false
		for try := 0; try < 3 && !ok; try++ {
			k, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "enter-pin")
			if ended {
				return
			}
			first := ""
			if k >= '0' && k <= '9' {
				first = string(k)
			}
			pin, ended := c.collectKeys(capt.Keys, first, 10, 6*time.Second)
			if ended {
				return
			}
			if subtle.ConstantTimeCompare([]byte(pin), []byte(d.PIN)) == 1 {
				ok = true
				break
			}
			c.e.Log.Warn("dial-in: wrong PIN", "dialin", d.Name, "from", from)
			if _, ended := c.sayPrompts(lang, nil, toneDeleted, "pin-wrong"); ended {
				return
			}
		}
		if !ok {
			c.e.publish("security.dialin", map[string]any{"dialin": d.Name, "from": from, "result": "wrong PIN three times"})
			c.sayPrompts(lang, nil, media.ToneConfirm, "goodbye")
			c.hangup("dial-in: wrong PIN", "system")
			return
		}
	}
	for try := 0; try < 3; try++ {
		k, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "enter-number")
		if ended {
			return
		}
		first := ""
		if k >= '0' && k <= '9' || k == '*' {
			first = string(k)
		}
		number, ended := c.collectNumber(capt.Keys, first)
		if ended {
			return
		}
		if number == "" {
			continue
		}
		cfg = c.e.Config()
		as := cfg.FindExtension(d.As)
		if as == nil || !as.Outbound {
			c.hangup("dial-in: its extension may not call out", "system")
			return
		}
		p := c.e.route(cfg, as, number)
		if p.reject != 0 {
			if _, ended := c.sayPrompts(lang, nil, toneDeleted, "invalid"); ended {
				return
			}
			continue
		}
		c.e.Log.Info("dial-in call", "dialin", d.Name, "from", from, "to", number, "as", d.As)
		c.e.publish("security.dialin", map[string]any{"dialin": d.Name, "from": from, "to": number, "result": "connected"})
		c.mu.Lock()
		c.callerExt, c.to, c.direction = d.As, number, firstNonEmptyStr(p.direction, "outbound")
		c.mu.Unlock()
		stopKeys := func() { c.caller.ep.Record(nil) }
		stopKeys()
		if p.local != "" {
			c.answerLocal(p)
			return
		}
		if !c.ringAnswered(p) {
			c.hangup("dial-in: no answer", "system")
		}
		return
	}
	c.hangup("dial-in: no number dialled", "system")
}

// collectNumber reads a number to call: digits (and * for feature codes, + is never on a keypad),
// ended by # or a pause of four seconds.
func (c *Call) collectNumber(keys <-chan byte, first string) (string, bool) {
	got := first
	for len(got) < 20 {
		select {
		case k := <-keys:
			if k == '#' {
				return got, false
			}
			if k >= '0' && k <= '9' || k == '*' {
				got += string(k)
			}
		case <-time.After(4 * time.Second):
			return got, false
		case <-c.cancelled:
			return got, true
		}
	}
	return got, false
}
