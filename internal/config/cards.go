package config

import (
	"fmt"
	"regexp"
)

// Cards is the telephony cards (DAHDI) of this computer, joined to BitVoice by the separate
// bitvoice-dahdi program: analogue ports with phones become devices of extensions, ports with phone
// company lines become lines of the card trunk.
type Cards struct {
	Enabled bool `json:"enabled"`
	// Token is the secret bitvoice-dahdi signs in with (it learns the extensions' passwords with it).
	Token string `json:"token"`
	// LinePort is the loopback SIP port bitvoice-dahdi answers on for the card trunk (5091 when 0).
	LinePort int  `json:"linePort"`
	ALaw     bool `json:"alaw"` // A-law (Europe and most of the world) rather than µ-law (North America, Japan)
	// Channels are the card's ports BitVoice uses, by DAHDI channel number.
	Channels []CardChannel `json:"channels"`
}

// CardChannel is one port: phone (an FXS port, a phone plugged in) or line (an FXO port, a phone
// company line plugged in).
type CardChannel struct {
	Channel   int    `json:"channel"`
	Kind      string `json:"kind"`      // phone or line
	Extension string `json:"extension"` // phone: the extension it is a device of
	Number    string `json:"number"`    // line: its phone number, matched by inbound routes
}

// DefaultCardLinePort is the loopback port of the card trunk when none is set.
const DefaultCardLinePort = 5091

var (
	tokenRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{24,128}$`)
	lineNumberRe = regexp.MustCompile(`^\+?[0-9]{1,20}$`)
)

func (c *Config) validateCards(p *Problems) {
	k := &c.System.Cards
	if k.LinePort == 0 {
		k.LinePort = DefaultCardLinePort
	}
	if k.LinePort < 1024 || k.LinePort > 65535 {
		p.add("system.cards.linePort", "use a port from 1024 to 65535")
	}
	if k.Enabled && !tokenRe.MatchString(k.Token) {
		p.add("system.cards.token", "make a token (24 or more letters and digits): bitvoice-dahdi signs in with it")
	}
	seen := map[int]bool{}
	for i := range k.Channels {
		ch := &k.Channels[i]
		f := fmt.Sprintf("system.cards.channels[%d]", i)
		if ch.Channel < 1 || ch.Channel > 1024 {
			p.add(f+".channel", "use the DAHDI channel number, 1 to 1024")
		} else if seen[ch.Channel] {
			p.add(f+".channel", "channel %d is listed twice", ch.Channel)
		}
		seen[ch.Channel] = true
		switch ch.Kind {
		case "phone":
			if ch.Extension == "" {
				p.add(f+".extension", "choose the extension this phone belongs to")
			} else if c.FindExtension(ch.Extension) == nil {
				p.add(f+".extension", "there is no extension %s", ch.Extension)
			}
		case "line":
			if ch.Number != "" && !lineNumberRe.MatchString(ch.Number) {
				p.add(f+".number", "use digits, with + in front for an international number")
			}
		default:
			p.add(f+".kind", "choose phone or line")
		}
	}
	for i, t := range c.Trunks {
		if t.Protocol == "dahdi" && !k.Enabled {
			p.add(fmt.Sprintf("trunks[%d].protocol", i), "switch on Telephony cards in Settings first")
		}
	}
}
