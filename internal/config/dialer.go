package config

import (
	"fmt"
	"strings"
	"time"
)

// Campaign is an outbound calling campaign: a list of contacts called for a queue's agents.
type Campaign struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Mode: preview (the agent sees the contact, then calls), progressive (one call per free agent),
	// power (Ratio calls per free agent), predictive (the ratio adjusted to keep abandoned calls under
	// MaxAbandon percent).
	Mode   string `json:"mode"`
	Queue  string `json:"queue"`  // the queue whose agents take the calls
	Active bool   `json:"active"` // calling now (within Hours)
	// CallerID is the number contacts see; empty for the trunk's.
	CallerID string `json:"callerId"`
	// As is the extension the calls are made as (its outbound routes); empty for the first agent's.
	As           string      `json:"as"`
	Ratio        float64     `json:"ratio"`        // power: calls per free agent (1 to 3)
	MaxAbandon   float64     `json:"maxAbandon"`   // percent of answered calls left without an agent (3 by default)
	Hours        []OpenHours `json:"hours"`        // when calls may be made, in Timezone
	Timezone     string      `json:"timezone"`     // empty for the system's
	RingSeconds  int         `json:"ringSeconds"`  // how long a contact's phone rings (25)
	Attempts     int         `json:"attempts"`     // tries per contact (3)
	RetryMinutes int         `json:"retryMinutes"` // between tries (60)
	// Machine is what happens when an answering machine picks up: hangup, message (leave the
	// campaign's recorded message after the greeting), or agent (detection off: everything to agents).
	Machine string `json:"machine"`
}

// FindCampaign returns a campaign by ID.
func (c *Config) FindCampaign(id string) *Campaign {
	for i := range c.Campaigns {
		if c.Campaigns[i].ID == id {
			return &c.Campaigns[i]
		}
	}
	return nil
}

// CampaignOpen says whether a campaign may call at t.
func (c *Config) CampaignOpen(k *Campaign, t time.Time) bool {
	loc := c.System.Location()
	if k.Timezone != "" {
		if l, err := time.LoadLocation(k.Timezone); err == nil {
			loc = l
		}
	}
	s := Schedule{Hours: k.Hours}
	return len(k.Hours) > 0 && s.IsOpen(t.In(loc))
}

func (c *Config) validateCampaigns(p *Problems, checkID func(field string, id *string)) {
	for i := range c.Campaigns {
		k := &c.Campaigns[i]
		f := fmt.Sprintf("campaigns[%d]", i)
		checkID(f+".id", &k.ID)
		k.Name = strings.TrimSpace(k.Name)
		if k.Name == "" {
			p.add(f+".name", "enter a name")
		}
		switch k.Mode {
		case "":
			k.Mode = "progressive"
		case "preview", "progressive", "power", "predictive":
		default:
			p.add(f+".mode", "choose preview, progressive, power or predictive")
		}
		if q := c.FindQueue(k.Queue); q == nil {
			p.add(f+".queue", "choose the queue whose agents take the calls")
		}
		if k.As != "" {
			if x := c.FindExtension(k.As); x == nil {
				p.add(f+".as", "no extension %s", k.As)
			} else if !x.Outbound {
				p.add(f+".as", "extension %s may not call outside numbers", k.As)
			}
		}
		k.CallerID = cleanDial(k.CallerID)
		if k.CallerID != "" && !dialRe.MatchString(k.CallerID) {
			p.add(f+".callerId", "enter the number contacts see, digits only")
		}
		if k.Ratio == 0 {
			k.Ratio = 1.5
		}
		if k.Ratio < 1 || k.Ratio > 3 {
			p.add(f+".ratio", "1 to 3 calls per free agent")
		}
		if k.MaxAbandon == 0 {
			k.MaxAbandon = 3
		}
		if k.MaxAbandon < 0.5 || k.MaxAbandon > 10 {
			p.add(f+".maxAbandon", "keep abandoned calls between 0.5 and 10 percent (the US rule is 3)")
		}
		if len(k.Hours) == 0 {
			p.add(f+".hours", "set the hours calls may be made (in the US, 8 am to 9 pm where the contact is)")
		}
		for j, h := range k.Hours {
			hf := fmt.Sprintf("%s.hours[%d]", f, j)
			if !clockRe.MatchString(h.Start) || !clockRe.MatchString(h.End) {
				p.add(hf, "times are HH:MM")
			}
			if len(h.Days) == 0 {
				p.add(hf+".days", "choose at least one day")
			}
		}
		if k.Timezone != "" {
			if _, err := time.LoadLocation(k.Timezone); err != nil {
				p.add(f+".timezone", "not a time zone (use names like America/Chicago)")
			}
		}
		if k.RingSeconds == 0 {
			k.RingSeconds = 25
		}
		if k.RingSeconds < 10 || k.RingSeconds > 60 {
			p.add(f+".ringSeconds", "ring 10 to 60 seconds (15 seconds or 4 rings at least, by the US rule)")
		}
		if k.Attempts == 0 {
			k.Attempts = 3
		}
		if k.Attempts < 1 || k.Attempts > 10 {
			p.add(f+".attempts", "1 to 10 tries per contact")
		}
		if k.RetryMinutes == 0 {
			k.RetryMinutes = 60
		}
		if k.RetryMinutes < 5 || k.RetryMinutes > 7*24*60 {
			p.add(f+".retryMinutes", "wait 5 minutes to a week between tries")
		}
		switch k.Machine {
		case "":
			k.Machine = "hangup"
		case "hangup", "message", "agent":
		default:
			p.add(f+".machine", "choose hang up, leave the message, or pass to an agent")
		}
	}
}
