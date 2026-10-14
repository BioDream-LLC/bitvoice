package pbx

import "github.com/biodream-llc/bitvoice/internal/config"

// Colleague is one person's presence, as colleagues see it in the staff portal: on a call, ringing,
// away (do not disturb, a meeting, time off) with why, offline (no phone connected) or available.
type Colleague struct {
	Extension  string `json:"extension"`
	Name       string `json:"name"`
	Department string `json:"department,omitempty"`
	State      string `json:"state"` // available, on-call, ringing, away, offline
	Reason     string `json:"reason,omitempty"`
}

// Colleagues is the presence of every enabled person (not door stations, speakers or guest rooms).
func (e *Engine) Colleagues(cfg *config.Config) []Colleague {
	busy, ringing := e.busyExtensions()
	out := []Colleague{}
	for _, x := range cfg.Extensions {
		if !x.Enabled || x.GuestRoom || (x.Kind != "" && x.Kind != "phone" && x.Kind != "analog") {
			continue
		}
		c := Colleague{Extension: x.Number, Name: x.Name, Department: x.Department, State: "available"}
		aw, away := e.awayNow(x.Number)
		switch {
		case busy[x.Number]:
			c.State = "on-call"
		case ringing[x.Number]:
			c.State = "ringing"
		case away:
			c.State, c.Reason = "away", aw.Reason
		case len(e.reg.contacts(x.Number)) == 0 && len(e.remoteContacts([]string{x.Number})) == 0:
			c.State = "offline"
		}
		out = append(out, c)
	}
	return out
}
