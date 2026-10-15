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
	// Spans are the card's digital lines: T1 or E1 primary rate ISDN (PRI), or basic rate ISDN (BRI).
	// Their B-channels are lines of the card trunk, with the called number (DID) and caller ID carried.
	Spans []CardSpan `json:"spans"`
}

// CardSpan is one T1 or E1 line run as PRI, or one BRI. Its channels are the ones dahdi_cfg gives
// the span: First is the first (a T1's 24 channels are First to First+23, the D-channel last; an E1's
// 31 are First to First+30, the D-channel the 16th; a BRI's are B1, B2 and the D-channel).
type CardSpan struct {
	Type  string `json:"type"`  // t1, e1 or bri
	First int    `json:"first"` // the span's first DAHDI channel
	// Role: user (BitVoice is the PBX on a phone company's PRI, the usual) or network (another PBX
	// connects to BitVoice as if it were the phone company).
	Role string `json:"role"`
	// Switch is the phone company's switch type: ni2, 5ess, dms100 (North America), euroisdn, qsig.
	// It decides how callers' names and transfers are signalled.
	Switch string `json:"switch"`
	// NetworkTransfer hands a transfer joining two calls on this span to the phone company (Two
	// B-Channel Transfer on ni2 and 5ess, explicit call transfer on euroisdn), freeing both channels.
	// It needs the service on the line; without it, transfers stay on the server, both channels in use.
	// On dms100 it is Release Link Trunk transfer, for transfers where one of the two calls is one
	// BitVoice placed.
	NetworkTransfer bool `json:"networkTransfer,omitempty"`
	// Charges asks the phone company for advice of charge on calls out (euroisdn, user side): its own
	// figure for each call, kept on the call's record.
	Charges bool `json:"charges,omitempty"`
	// Multipoint (BRI): several terminals share the line, each given a TEI, and calls to the line are
	// offered to all (a "multi-device" line; or, on the network side, ISDN phones on BitVoice's bus).
	// Off: point to point (a PBX line, one device).
	Multipoint bool `json:"multipoint,omitempty"`
}

// Channels of a span: its B-channels and D-channel, as DAHDI numbers them.
func (s CardSpan) Channels() (b []int, d int) {
	if s.Type == "bri" {
		return []int{s.First, s.First + 1}, s.First + 2
	}
	if s.Type == "e1" {
		for ts := 1; ts <= 31; ts++ {
			if ts != 16 {
				b = append(b, s.First+ts-1)
			}
		}
		return b, s.First + 15
	}
	for i := 0; i < 23; i++ {
		b = append(b, s.First+i)
	}
	return b, s.First + 23
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
	used := map[int]string{}
	for ch := range seen {
		used[ch] = "a port"
	}
	for i := range k.Spans {
		s := &k.Spans[i]
		f := fmt.Sprintf("system.cards.spans[%d]", i)
		switch s.Type {
		case "t1", "e1", "bri":
		default:
			p.add(f+".type", "choose T1, E1 or BRI")
		}
		if s.Role == "" {
			s.Role = "user"
		}
		if s.Role != "user" && s.Role != "network" {
			p.add(f+".role", "choose user (on a phone company's line) or network")
		}
		if s.Switch == "" {
			s.Switch = "ni2"
			if s.Type == "e1" || s.Type == "bri" {
				s.Switch = "euroisdn"
			}
		}
		if s.Type == "bri" && s.Switch != "euroisdn" && s.Switch != "ni2" {
			p.add(f+".switch", "a BRI is euroisdn, or ni2 in North America")
		}
		if s.Multipoint && s.Type != "bri" {
			s.Multipoint = false // only a BRI has several terminals on one line
		}
		switch s.Switch {
		case "ni2", "5ess", "dms100", "euroisdn", "qsig":
		default:
			p.add(f+".switch", "choose ni2, 5ess, dms100, euroisdn or qsig")
		}
		if s.NetworkTransfer && (s.Role != "user" || s.Switch == "qsig") {
			p.add(f+".networkTransfer", "the phone company takes transfers only on a user-side line with ni2, 5ess, dms100 or euroisdn")
		}
		if s.Charges && (s.Role != "user" || s.Switch != "euroisdn") {
			p.add(f+".charges", "advice of charge comes from the phone company on a user-side euroisdn line")
		}
		if s.First < 1 || s.First > 1000 {
			p.add(f+".first", "use the span's first DAHDI channel number")
			continue
		}
		b, d := s.Channels()
		for _, ch := range append(b, d) {
			if prev, ok := used[ch]; ok {
				p.add(f+".first", "channel %d is also %s", ch, prev)
				break
			}
			used[ch] = fmt.Sprintf("in span %d", i+1)
		}
	}
	for i, t := range c.Trunks {
		if t.Protocol == "dahdi" && !k.Enabled {
			p.add(fmt.Sprintf("trunks[%d].protocol", i), "switch on Telephony cards in Settings first")
		}
	}
}
