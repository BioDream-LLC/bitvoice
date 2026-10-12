package pbx

import (
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// promptBox is where a menu's recorded prompt is kept (beside the mailbox greetings).
func promptBox(menuID string) string { return "menu-" + menuID }

// runMenu plays a menu's prompt and sends the caller where the key they press says.
func (c *Call) runMenu(id string) {
	cfg := c.e.Config()
	m := cfg.FindMenu(id)
	if m == nil {
		c.hangup("menu removed", "system")
		return
	}
	var prompt []int16
	if c.e.Mail != nil {
		if b, err := c.e.Mail.Greeting(promptBox(m.ID)); err == nil {
			prompt, _ = media.DecodeWAV(b)
		}
	}
	if len(prompt) == 0 {
		prompt = media.ToneSamples(media.ToneBeep) // no prompt recorded yet: a beep invites a key
	}
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	stopKeys := func() { c.caller.ep.Record(nil) }

	for round := 0; round < m.Repeats; round++ {
		key, ended := c.play(prompt, capt.Keys)
		if ended {
			stopKeys()
			return
		}
		if key == 0 {
			select {
			case key = <-capt.Keys:
			case <-time.After(time.Duration(m.TimeoutSeconds) * time.Second):
			case <-c.cancelled:
				stopKeys()
				return
			}
		}
		if key == 0 {
			continue
		}
		for _, o := range m.Options {
			if o.Key == string(key) {
				stopKeys()
				c.e.publish("call.updated", c.info())
				c.goTo(cfg, o.Destination)
				return
			}
		}
		if m.DialExtensions && key >= '0' && key <= '9' {
			number := c.collectDigits(capt.Keys, string(key))
			if x := cfg.FindExtension(number); x != nil {
				stopKeys()
				c.goTo(cfg, config.Destination{Type: "extension", Target: number})
				return
			}
		}
		// A key that leads nowhere: a short low tone, then the prompt again.
		if _, ended := c.play(media.ToneSamples(toneDeleted), nil); ended {
			stopKeys()
			return
		}
	}
	stopKeys()
	if m.Timeout != nil {
		c.goTo(cfg, *m.Timeout)
		return
	}
	c.play(media.ToneSamples(media.ToneConfirm), nil)
	c.hangup("no choice made at the menu", "system")
}

// collectDigits gathers an extension number: digits until two seconds pass without one.
func (c *Call) collectDigits(keys <-chan byte, first string) string {
	var b strings.Builder
	b.WriteString(first)
	for b.Len() < 8 {
		select {
		case k := <-keys:
			if k == '#' {
				return b.String()
			}
			if k >= '0' && k <= '9' {
				b.WriteByte(k)
			}
		case <-time.After(2 * time.Second):
			return b.String()
		case <-c.cancelled:
			return ""
		}
	}
	return b.String()
}

// goTo sends a caller BitVoice has already answered to a destination: someone to ring, voicemail,
// another menu, or goodbye.
func (c *Call) goTo(cfg *config.Config, d config.Destination) {
	for depth := 1; depth < 5; depth++ {
		c.mu.Lock()
		sc := scriptCall{from: c.from, fromName: c.fromName, to: c.to, direction: c.direction, trunk: c.trunk}
		c.mu.Unlock()
		p := c.e.resolveFor(cfg, d, depth, sc)
		switch {
		case p.reject != 0:
			c.play(media.ToneSamples(media.ToneConfirm), nil)
			c.hangup("nobody to take the call ("+p.reason+")", "system")
			return
		case p.local != "":
			c.mu.Lock()
			c.toName = p.toName
			c.mu.Unlock()
			c.answerLocal(p)
			return
		}
		if c.ringAnswered(p) {
			return
		}
		select {
		case <-c.cancelled:
			return
		default:
		}
		if p.noAnswer == nil {
			c.hangup("no answer", "system")
			return
		}
		d = *p.noAnswer
	}
	c.hangup("too many forwards", "system")
}

// ringAnswered rings a plan's stages for an answered caller, who hears ringback, and reports whether
// someone answered.
func (c *Call) ringAnswered(p plan) bool {
	c.mu.Lock()
	c.toName = p.toName
	pt := 0
	if c.caller.codecName() == "PCMA" {
		pt = 8
	}
	if c.player != nil {
		c.player.Stop()
	}
	c.player = media.NewPlayer(c.caller.ep, pt)
	c.player.Play(media.Ringback(c.e.Config().System.Country))
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.callee == nil && c.player != nil {
			c.player.Stop()
			c.player = nil
		}
		c.mu.Unlock()
	}()
	for _, st := range p.stages {
		c.mu.Lock()
		c.stageNo++
		stageNo := c.stageNo
		c.mu.Unlock()
		started := 0
		for _, t := range st.targets {
			if c.startFork(t, stageNo) {
				started++
			}
		}
		if started == 0 {
			continue
		}
		timer := time.NewTimer(st.timeout)
		for failed := 0; failed < started; {
			select {
			case <-c.cancelled:
				timer.Stop()
				return false
			case r := <-c.results:
				if r.stage != stageNo {
					continue
				}
				if r.code == 200 {
					timer.Stop()
					return true
				}
				failed++
			case <-timer.C:
				c.cancelForks(nil)
				failed = started
			}
		}
		timer.Stop()
	}
	return false
}
