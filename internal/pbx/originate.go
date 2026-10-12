package pbx

import (
	"crypto/subtle"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// ErrNoAnswer is returned by Originate when the extension did not pick up.
var ErrNoAnswer = errors.New("the extension did not answer")

// Originate is click-to-dial: it rings extension ext and, once someone picks up, calls number for them,
// as if they had dialled it (their routes, caller ID and limits apply). It returns the call's ID as soon
// as the extension answers; the second half proceeds in the background, with ringback on the extension.
// ring is how long the extension may ring (default 30 s).
func (e *Engine) Originate(ext, number string, ring time.Duration) (string, error) {
	cfg := e.Config()
	x := cfg.FindExtension(ext)
	if x == nil || !x.Enabled {
		return "", errors.New("no extension " + ext)
	}
	number = cleanNumber(number)
	if number == "" {
		return "", errors.New("no number to call")
	}
	if ring <= 0 {
		ring = 30 * time.Second
	}
	if e.activeCallsFor(x.Number) >= cfg.Security.MaxCallsPerExtension {
		return "", errors.New("extension " + ext + " is on too many calls")
	}
	onward := e.route(cfg, x, number)
	if onward.reject != 0 {
		return "", errors.New(plainRefusal(number, onward.reason))
	}
	targets := e.phoneTargets(cfg, x)
	if len(targets) == 0 {
		return "", errors.New("extension " + ext + " has no phone connected")
	}

	// First half: ring the extension. Its phone shows the number it is about to call.
	h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: number, fromName: "Call to " + number,
		answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		xfer: make(chan *Leg, 1)}
	go h.run(plan{stages: []stage{{targets: targets, timeout: ring}}, direction: "internal"})
	leg := <-h.xfer
	if leg == nil {
		return "", ErrNoAnswer
	}

	c := &Call{e: e, ID: config.NewID(), start: time.Now(), answered: make(chan struct{}),
		cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		from: x.Number, fromName: x.Name, callerExt: x.Number, to: number,
		direction: onward.direction, toName: onward.toName, trunk: onward.trunk}
	leg.call = c
	leg.Number, leg.Name, leg.Ext = x.Number, x.Name, x.Number
	c.caller = leg
	e.mu.Lock()
	e.calls[c.ID] = c
	e.mu.Unlock()
	e.publish("call.started", c.info())
	if onward.direction == "emergency" {
		var from netip.Addr
		if leg.peer.Addr.IsValid() {
			from = leg.peer.Addr.Addr()
		}
		pl := e.emergency(cfg, x, number, from, c.ID)
		c.place = &pl
	}
	go c.originateOnward(leg, onward)
	return c.ID, nil
}

// phoneTargets is every phone of an extension, as the router rings them.
func (e *Engine) phoneTargets(cfg *config.Config, x *config.Extension) []target {
	p := e.route(cfg, nil, x.Number)
	for _, st := range p.stages {
		if len(st.targets) > 0 {
			return st.targets
		}
	}
	return nil
}

// originateOnward places the second half of a click-to-dial call, with ringback to the extension.
func (c *Call) originateOnward(stay *Leg, p plan) {
	c.mu.Lock()
	pt := 0
	if stay.codecName() == "PCMA" {
		pt = 8
	}
	c.player = media.NewPlayer(stay.ep, pt)
	c.player.Play(media.Ringback(c.e.Config().System.Country))
	c.mu.Unlock()

	other := c.dialForTransfer(stay, p)
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
		c.player = nil
	}
	if other == nil || c.ended || stay.ended {
		c.mu.Unlock()
		if other != nil {
			other.bye()
			other.close()
		}
		c.hangup("the number did not answer", "system")
		return
	}
	c.callee = other
	c.answer = time.Now()
	c.answeredBy = other.party()
	c.ansOnce.Do(func() { close(c.answered) })
	c.mu.Unlock()
	connect(stay, other)
	c.mu.Lock()
	c.startRecording(other)
	c.mu.Unlock()
	c.bridged()
	c.e.publish("call.updated", c.info())
}

// Redirect sends an answered call's other party to number (a blind transfer from the outside, as AMI
// Redirect and ARI do), releasing the party named by leaving ("caller" or "callee").
func (e *Engine) Redirect(id, leaving, number string) error {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	c.mu.Lock()
	a, b := c.caller, c.callee
	c.mu.Unlock()
	if b == nil {
		return errors.New("the call has not been answered")
	}
	if leaving == "callee" {
		a, b = b, a
	}
	go c.blindTransfer(a, b, cleanNumber(number))
	return nil
}

// SetLoadToken lets calls carrying this token in X-BitVoice-Load, from this computer, past the
// per-extension call limit (the built-in call generator). Empty turns it off.
func (e *Engine) SetLoadToken(token string) { e.loadToken.Store(token) }

func (e *Engine) isLoadTest(m *sip.Message, ip netip.Addr) bool {
	tok, _ := e.loadToken.Load().(string)
	return tok != "" && (ip.IsLoopback() || ip == e.LocalSIP().Addr()) && subtle.ConstantTimeCompare([]byte(m.Get("X-BitVoice-Load")), []byte(tok)) == 1
}

// plainRefusal says in words why number cannot be called from an extension (the routing gives SIP
// reason phrases, which are meant for phones).
func plainRefusal(number, reason string) string {
	switch reason {
	case "No Route":
		return "no outbound route matches " + number + ": add one in Call routing"
	case "No Trunk Available":
		return "no trunk is connected to call " + number + " on"
	case "Outbound Calls Not Allowed":
		return "this extension may not call outside numbers"
	case "Premium Number Blocked":
		return number + " is a premium-rate number, which is blocked"
	case "Not Registered":
		return number + " has no phone connected"
	case "Nobody Available":
		return "nobody in " + number + " is available"
	}
	return number + " cannot be called: " + strings.ToLower(reason)
}
