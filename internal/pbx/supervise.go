package pbx

import (
	"errors"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// Supervision: a supervisor's phone is rung and joins a call through a mixing bridge, to listen (heard
// by nobody), whisper (heard by the agent only) or barge (heard by both).

// SupervisionModes are the ways a supervisor can join.
var SupervisionModes = []string{"listen", "whisper", "barge"}

func hearing(mode, agent string) func(listener, speaker string) bool {
	return func(listener, speaker string) bool {
		if speaker != "sup" {
			return true // the supervisor hears everyone, and the two parties hear each other
		}
		switch mode {
		case "barge":
			return true
		case "whisper":
			return listener == agent
		}
		return false
	}
}

// Supervise rings supervisor and, when they answer, joins them to call id in the given mode. It
// returns once the supervisor's phone is ringing; the rest happens as they answer.
func (e *Engine) Supervise(id, supervisor, mode string) error {
	cfg := e.Config()
	sx := cfg.FindExtension(supervisor)
	if sx == nil || !sx.Enabled {
		return errors.New("no extension " + supervisor)
	}
	if mode != "listen" && mode != "whisper" && mode != "barge" {
		return errors.New("choose listen, whisper or barge")
	}
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	c.mu.Lock()
	a, b, busy := c.caller, c.callee, c.bridge != nil
	c.mu.Unlock()
	if a == nil || b == nil {
		return errors.New("only a call between two people can be supervised, and this one is not (yet)")
	}
	if busy {
		return errors.New("someone is already supervising this call")
	}
	if a.Ext == supervisor || b.Ext == supervisor {
		return errors.New("the supervisor is on the call")
	}
	targets := e.phoneTargets(cfg, sx)
	if len(targets) == 0 {
		return errors.New("extension " + supervisor + " has no phone connected")
	}
	// The agent is the party on an extension, the callee when both are.
	agent := "callee"
	if b.Ext == "" && a.Ext != "" {
		agent = "caller"
	}
	name := a.Name
	if name == "" {
		name = a.Number
	}
	h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: c.to, fromName: "Supervise " + name,
		answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		xfer: make(chan *Leg, 1)}
	go h.run(plan{stages: []stage{{targets: targets, timeout: 30 * time.Second}}, direction: "internal"})
	go func() {
		leg := <-h.xfer
		if leg == nil {
			return
		}
		sc := &Call{e: e, ID: config.NewID(), start: time.Now(), answered: make(chan struct{}),
			cancelled: make(chan struct{}), results: make(chan forkResult, 64),
			from: sx.Number, fromName: sx.Name, callerExt: sx.Number, to: c.to, toName: mode + ": " + name, direction: "internal"}
		leg.call, leg.Number, leg.Name, leg.Ext = sc, sx.Number, sx.Name, sx.Number
		sc.caller = leg
		sc.answer = time.Now()
		sc.localAnswer = true
		sc.ansOnce.Do(func() { close(sc.answered) })

		c.mu.Lock()
		if c.ended || c.bridge != nil || c.caller != a || c.callee != b {
			c.mu.Unlock()
			leg.bye()
			leg.close()
			return
		}
		br := media.NewBridge(hearing(mode, agent))
		c.bridge, c.supervision, c.supAgent = br, mode, agent
		c.mu.Unlock()
		br.Add("caller", a.ep)
		br.Add("callee", b.ep)
		br.Add("sup", leg.ep)

		e.mu.Lock()
		e.calls[sc.ID] = sc
		e.mu.Unlock()
		e.publish("call.started", sc.info())
		// Whoever leaves first, the bridge goes and the two parties are joined directly again.
		sc.onEnd = []func(){func() { c.endSupervision(br, nil) }}
		c.mu.Lock()
		c.onEnd = append(c.onEnd, func() { c.endSupervision(br, sc) })
		c.mu.Unlock()
		e.publish("call.updated", c.info())
	}()
	return nil
}

// SetSupervision changes a supervised call's mode (listen to barge, say).
func (e *Engine) SetSupervision(id, mode string) error {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	c.mu.Lock()
	br, agent := c.bridge, c.supAgent
	if br != nil {
		c.supervision = mode
	}
	c.mu.Unlock()
	if br == nil {
		return errors.New("the call is not being supervised")
	}
	if mode != "listen" && mode != "whisper" && mode != "barge" {
		return errors.New("choose listen, whisper or barge")
	}
	br.SetHearing(hearing(mode, agent))
	e.publish("call.updated", c.info())
	return nil
}

func (c *Call) endSupervision(br *media.Bridge, sup *Call) {
	c.mu.Lock()
	mine := c.bridge == br
	if mine {
		c.bridge, c.supervision = nil, ""
	}
	a, b, ended := c.caller, c.callee, c.ended
	c.mu.Unlock()
	if !mine {
		return
	}
	br.Close()
	if !ended && a != nil && b != nil {
		connect(a, b)
	}
	if sup != nil {
		sup.hangup("the supervised call ended", "system")
	}
	c.e.publish("call.updated", c.info())
}
