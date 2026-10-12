package pbx

import (
	"fmt"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// BriefRequest asks for a briefing for the person a call is being passed to: what was said so far,
// as the two sides' audio, or as text (an AI receptionist's conversation).
type BriefRequest struct {
	CallID   string
	Target   string // the number the call goes to
	From     string
	FromName string
	By       string // who passed it on
	Caller   []int16
	Callee   []int16
	Text     string
}

// briefSeconds is how much of a call is kept for a briefing.
const briefSeconds = 300

// keepForBrief starts keeping both sides' audio of a bridged call for a briefing, when briefings are
// on and the call is not being recorded (a recording is used instead).
func (c *Call) keepForBrief() {
	a := c.e.Config().System.AI
	if !a.Enabled || !a.Briefings || c.e.Brief == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec != nil || c.briefing != nil || c.caller == nil || c.callee == nil || c.caller.ep == nil || c.callee.ep == nil {
		return
	}
	now := time.Now()
	r := &recording{start: now, caller: media.NewTrack(now, briefSeconds), callee: media.NewTrack(now, briefSeconds)}
	c.briefing = r
	c.caller.ep.Record(r.caller.Packet)
	c.callee.ep.Record(r.callee.Packet)
}

// briefFor asks for a briefing for target with what the call has heard so far.
func (c *Call) briefFor(target, by string) {
	if c.e.Brief == nil || !c.e.Config().System.AI.Briefings {
		return
	}
	c.mu.Lock()
	r := c.rec
	if r == nil {
		r = c.briefing
	}
	from, name := c.from, c.fromName
	c.mu.Unlock()
	if r == nil {
		return
	}
	c.e.brief(BriefRequest{CallID: c.ID, Target: target, From: from, FromName: name, By: by, Caller: r.caller.Samples(), Callee: r.callee.Samples()})
}

func (e *Engine) brief(b BriefRequest) {
	if e.Brief != nil && e.Config().System.AI.Briefings {
		go e.Brief(b)
	}
}

// SendText sends a SIP MESSAGE (a text on the phone's screen) to every phone of an extension. It
// reports how many phones took it.
func (e *Engine) SendText(ext, fromName, text string) int {
	if e.stack == nil {
		return 0
	}
	return e.sendLocalText(ext, fromName, text) + e.textElsewhere(ext, fromName, text)
}

// sendLocalText shows a text on the extension's phones registered with this server.
func (e *Engine) sendLocalText(ext, fromName, text string) int {
	took := 0
	for _, b := range e.reg.localContacts(ext) {
		host := e.stack.LocalHost(b.peer)
		m := &sip.Message{IsRequest: true, Method: sip.MESSAGE, RequestURI: b.requestURI()}
		m.Set("From", fmt.Sprintf("%s<sip:bitvoice@%s>;tag=%s", display(fromName), host, sip.NewTag()))
		m.Set("To", "<sip:"+ext+"@"+host+">")
		m.Set("Call-ID", sip.NewCallID())
		m.Set("CSeq", "1 MESSAGE")
		m.Set("Content-Type", "text/plain;charset=UTF-8")
		m.Body = []byte(text)
		tx := e.stack.Request(m, b.peer)
		for r := range tx.Responses {
			if r.StatusCode >= 200 && r.StatusCode < 300 {
				took++
			}
		}
	}
	return took
}
