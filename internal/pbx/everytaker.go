package pbx

import (
	"strconv"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// everyTakerRing is how long the emergency last resort rings before it looks again for phones.
var everyTakerRing = 2 * time.Minute

// everyTaker is the last resort for a voice emergency call from the ESInet or a CAMA trunk that its
// route would refuse or that nobody answered: every enabled extension is rung, a phone that connects
// meanwhile is rung too, and it goes on ringing until someone answers or the caller hangs up. A text
// call waits on the 911 screen instead (answerText). Refused only when no extension is enabled at all.
func (e *Engine) everyTaker(cfg *config.Config) plan {
	st := stage{timeout: everyTakerRing}
	for _, x := range cfg.Extensions {
		if !x.Enabled {
			continue
		}
		for _, b := range e.reg.contacts(x.Number) {
			st.targets = append(st.targets, target{ext: x.Number, name: x.Name, binding: b})
		}
		st.wake = append(st.wake, waking{x.Number, x.Name})
	}
	if len(st.wake) == 0 {
		e.Log.Error("an emergency call could not be offered to anyone: no extension is enabled")
		return plan{reject: 480, reason: "No Call-Takers", why: []string{"No extension is enabled, so nobody could be rung."}}
	}
	return plan{toName: "Every call-taker", endless: true, stages: []stage{st}}
}

// needsEveryTaker says whether a plan that ended without an answer must fall back to every call-taker:
// a voice emergency call taken from the ESInet or a CAMA trunk, not a test call, and not one an NG911
// core passes on to other PSAPs (the ESRP keeps its own chain to the end).
func (c *Call) needsEveryTaker(p plan) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.esinet && !c.testCall && !c.textOnly && !c.localAnswer && !p.toTrunks() && c.direction == "inbound"
}

// refusedWhy says, for the call's routing trail, how a route would have refused the call.
func refusedWhy(route string, p plan) string {
	if route == "(none)" {
		return "No incoming route takes this number."
	}
	if p.reject == 0 {
		return route + " had nothing to ring."
	}
	code := strconv.Itoa(p.reject)
	if p.reason != "" {
		code += " " + p.reason
	}
	return route + " would have refused it (" + code + ")."
}

// toTrunks reports whether a plan sends the call out on trunks (an ESRP passing it to PSAPs).
func (p plan) toTrunks() bool {
	for _, s := range p.stages {
		for _, t := range s.targets {
			if t.trunk != nil || t.viaTrunk != "" {
				return true
			}
		}
	}
	return false
}
