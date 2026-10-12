package pbx

import (
	"sort"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Agent presence for queues: an agent can log out (no queue calls at all) or pause (a break, a
// meeting, after a difficult call) with a reason. Agents are logged in by default; presence is kept in
// memory and starts afresh with the program, so a forgotten pause never lasts for ever.

// Presence is one agent's queue presence.
type Presence struct {
	Extension string    `json:"extension"`
	LoggedOut bool      `json:"loggedOut"`
	Paused    bool      `json:"paused"`
	Reason    string    `json:"reason,omitempty"`
	Since     time.Time `json:"since"`
}

type presences struct {
	mu sync.Mutex
	by map[string]Presence
}

func (p *presences) get(ext string) Presence {
	p.mu.Lock()
	defer p.mu.Unlock()
	if x, ok := p.by[ext]; ok {
		return x
	}
	return Presence{Extension: ext}
}

func (p *presences) set(x Presence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.by == nil {
		p.by = map[string]Presence{}
	}
	x.Since = time.Now().UTC()
	if !x.LoggedOut && !x.Paused {
		delete(p.by, x.Extension)
		return
	}
	p.by[x.Extension] = x
}

// SetAgent logs an agent in or out, or pauses or resumes them, publishing the change.
func (e *Engine) SetAgent(ext string, loggedOut, paused bool, reason string) Presence {
	if loggedOut {
		paused, reason = false, ""
	}
	if !paused && !loggedOut {
		reason = ""
	}
	x := Presence{Extension: ext, LoggedOut: loggedOut, Paused: paused, Reason: reason}
	e.presence.set(x)
	e.publish("queue.changed", map[string]any{"agent": ext})
	return e.presence.get(ext)
}

// Agents lists the agents who are not simply logged in and available.
func (e *Engine) Agents() []Presence {
	e.presence.mu.Lock()
	defer e.presence.mu.Unlock()
	out := make([]Presence, 0, len(e.presence.by))
	for _, x := range e.presence.by {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Extension < out[j].Extension })
	return out
}

// agentCode toggles the caller's queue presence: rising tones mean available, a low tone not.
func (c *Call) agentCode(code string) {
	cfg := c.e.Config()
	ext := c.callerExt
	pr := c.e.presence.get(ext)
	var now Presence
	if code == cfg.System.AgentLogin {
		now = c.e.SetAgent(ext, !pr.LoggedOut, false, "")
	} else {
		now = c.e.SetAgent(ext, false, !pr.Paused, map[bool]string{true: "paused from the phone"}[!pr.Paused])
	}
	tone := media.ToneSamples(media.ToneConfirm)
	if now.LoggedOut || now.Paused {
		tone = media.ToneSamples(media.ToneOff)
	}
	c.play(tone, nil)
	time.Sleep(300 * time.Millisecond)
	c.hangup("agent code", "system")
}
