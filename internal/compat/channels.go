// Package compat speaks Asterisk's two control interfaces, so tools written for Asterisk (call-centre
// wallboards, CRM click-to-dial, monitoring) work with BitVoice unchanged: AMI, the line-based manager
// protocol on TCP port 5038, and ARI, the REST and WebSocket interface. Both show BitVoice calls as
// Asterisk channels, and support the common actions: list, originate, redirect, hang up, endpoints and
// queues. ARI's Stasis media applications are not offered: BitVoice's own scripts (JavaScript or Lua)
// do that job.
package compat

import (
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// PBX is what the interfaces need from the engine.
type PBX interface {
	Config() *config.Config
	Calls() []pbx.CallInfo
	Hangup(id, by string) bool
	Originate(ext, number string, ring time.Duration) (string, error)
	Redirect(id, leaving, number string) error
	Registrations() []pbx.Binding
	Queues() []pbx.QueueStatus
}

// Channel is one side of a BitVoice call, as Asterisk would name it.
type Channel struct {
	ID            string // Asterisk "Uniqueid": call ID plus -1 (caller) or -2 (callee)
	Call          string
	Side          string // caller or callee
	Name          string // PJSIP/100-0001a2b3
	State         int    // Asterisk state number: 4 Ring, 5 Ringing, 6 Up
	CallerNum     string
	CallerName    string
	ConnectedNum  string
	ConnectedName string
	Exten         string
	Created       time.Time
}

// StateName is Asterisk's word for a channel state number.
func StateName(s int) string {
	switch s {
	case 4:
		return "Ring"
	case 5:
		return "Ringing"
	case 6:
		return "Up"
	}
	return "Down"
}

func tech(num, trunk string) string {
	if trunk != "" {
		return "PJSIP/" + strings.ReplaceAll(trunk, " ", "_")
	}
	return "PJSIP/" + num
}

// Channels turns a call into its one or two channels.
func Channels(c pbx.CallInfo) []Channel {
	short := c.ID
	if len(short) > 8 {
		short = short[:8]
	}
	up := c.State == "answered" || c.State == "held"
	callerTrunk, calleeTrunk := "", ""
	switch c.Direction {
	case "inbound":
		callerTrunk = c.Trunk
	case "outbound", "emergency":
		calleeTrunk = c.Trunk
	}
	state := 4
	if up {
		state = 6
	}
	out := []Channel{{ID: c.ID + "-1", Call: c.ID, Side: "caller", Name: tech(c.From, callerTrunk) + "-" + short + "1",
		State: state, CallerNum: c.From, CallerName: c.FromName, ConnectedNum: c.To, ConnectedName: c.ToName,
		Exten: c.To, Created: c.Start}}
	if up || len(c.Legs) > 1 {
		cs := 5
		if up {
			cs = 6
		}
		out = append(out, Channel{ID: c.ID + "-2", Call: c.ID, Side: "callee", Name: tech(c.To, calleeTrunk) + "-" + short + "2",
			State: cs, CallerNum: c.To, CallerName: c.ToName, ConnectedNum: c.From, ConnectedName: c.FromName,
			Exten: c.To, Created: c.Start})
	}
	return out
}

// Change is what happened to a channel between two looks at its call.
type Change struct {
	Kind string // new, state, gone
	Ch   Channel
}

// Tracker remembers the channels it has reported, so each call event becomes Asterisk-style channel
// events: a new channel, a state change, or a hang-up.
type Tracker struct {
	mu    sync.Mutex
	known map[string]Channel // by ID
}

func NewTracker(now []pbx.CallInfo) *Tracker {
	t := &Tracker{known: map[string]Channel{}}
	for _, c := range now {
		for _, ch := range Channels(c) {
			t.known[ch.ID] = ch
		}
	}
	return t
}

// Update reports the changes a call event brings; ended is true for call.ended.
func (t *Tracker) Update(c pbx.CallInfo, ended bool) []Change {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Change
	now := map[string]bool{}
	if !ended {
		for _, ch := range Channels(c) {
			now[ch.ID] = true
			old, ok := t.known[ch.ID]
			t.known[ch.ID] = ch
			switch {
			case !ok:
				out = append(out, Change{"new", ch})
			case old.State != ch.State || old.ConnectedNum != ch.ConnectedNum:
				out = append(out, Change{"state", ch})
			}
		}
	}
	for _, side := range []string{"-1", "-2"} {
		id := c.ID + side
		if ch, ok := t.known[id]; ok && !now[id] {
			delete(t.known, id)
			out = append(out, Change{"gone", ch})
		}
	}
	return out
}

// Find returns the call and side of a channel named by its ID or its name.
func Find(p PBX, id string) (pbx.CallInfo, Channel, bool) {
	for _, c := range p.Calls() {
		for _, ch := range Channels(c) {
			if ch.ID == id || ch.Name == id {
				return c, ch, true
			}
		}
	}
	return pbx.CallInfo{}, Channel{}, false
}

// Endpoint is an extension as Asterisk lists a PJSIP endpoint.
type Endpoint struct {
	Resource, State string
	Channels        []string
}

// Endpoints lists every extension and whether a phone is registered for it.
func Endpoints(p PBX) []Endpoint {
	online := map[string]bool{}
	for _, b := range p.Registrations() {
		online[b.Extension] = true
	}
	busy := map[string][]string{}
	for _, c := range p.Calls() {
		for _, ch := range Channels(c) {
			busy[ch.CallerNum] = append(busy[ch.CallerNum], ch.ID)
		}
	}
	var out []Endpoint
	for _, x := range p.Config().Extensions {
		st := "offline"
		if online[x.Number] {
			st = "online"
		}
		out = append(out, Endpoint{Resource: x.Number, State: st, Channels: busy[x.Number]})
	}
	return out
}

// Extension returns the number from "PJSIP/100", "SIP/100", "Local/100@context" or "100".
func Extension(channel string) string {
	if i := strings.IndexByte(channel, '/'); i >= 0 {
		channel = channel[i+1:]
	}
	if i := strings.IndexAny(channel, "@-"); i >= 0 {
		channel = channel[:i]
	}
	return channel
}
