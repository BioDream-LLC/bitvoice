package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	// Signalling is how the span's calls are signalled: isdn (PRI or BRI, the default), em (a T1's
	// channels as E&M trunks, robbed-bit signalling, e&m in system.conf) or r2 (an E1's channels as
	// MFC-R2, cas in system.conf).
	Signalling string `json:"signalling,omitempty"`
	// Start (em) is the start signal: wink (the usual), immediate, delay, or fgd (Feature Group D, the
	// caller's number sent too). Address (em) is how numbers go: dtmf or mf (MF R1).
	Start   string `json:"start,omitempty"`
	Address string `json:"address,omitempty"`
	// Number (em, r2) is the number a call in reaches when the far end sends none.
	Number string `json:"number,omitempty"`
	// NPD (em, start cama) are the area codes a CAMA trunk's numbering plan digit stands for: the
	// first for digit 0, the next for 1, and so on (traditional CAMA sends 7 digits and one NPD; enhanced
	// CAMA sends all 10 digits and needs none).
	NPD []string `json:"npd,omitempty"`
	// Variant (r2) is the country's MFC-R2 variant: itu, and the national ones; (ss7) itu or ansi.
	Variant string `json:"variant,omitempty"`
	// SS7 (signalling ss7): this end's point code (OPC), the adjacent signalling point's (APC: the
	// carrier's STP or switch), the network indicator (national or international), the signalling
	// link code, and the CIC of the span's first voice channel. Point codes are a number, or dashed:
	// network-cluster-member for ANSI, zone-area-point (3-8-3) for ITU.
	OPC      string `json:"opc,omitempty"`
	APC      string `json:"apc,omitempty"`
	NI       string `json:"ni,omitempty"`
	SLC      int    `json:"slc,omitempty"`
	FirstCIC int    `json:"firstCic,omitempty"`
}

// PointCode reads a point code: a number, or n-c-m (ANSI, 8 bits each) or z-a-s (ITU, 3-8-3 bits).
func PointCode(s string, ansi bool) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, "-")
	if len(parts) == 1 {
		v, err := strconv.Atoi(s)
		max := 1<<14 - 1
		if ansi {
			max = 1<<24 - 1
		}
		return v, err == nil && v > 0 && v <= max
	}
	if len(parts) != 3 {
		return 0, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return 0, false
		}
		n[i] = v
	}
	if ansi {
		if n[0] > 255 || n[1] > 255 || n[2] > 255 {
			return 0, false
		}
		return n[0]<<16 | n[1]<<8 | n[2], true
	}
	if n[0] > 7 || n[1] > 255 || n[2] > 7 {
		return 0, false
	}
	return n[0]<<11 | n[1]<<3 | n[2], true
}

// CAS says whether the span's channels are signalled one by one (E&M, R2) rather than on a D-channel.
func (s CardSpan) CAS() bool { return s.Signalling == "em" || s.Signalling == "r2" }

// Channels of a span: its B-channels and D-channel, as DAHDI numbers them.
func (s CardSpan) Channels() (b []int, d int) {
	if s.Signalling == "em" && s.Type == "t1" {
		for i := 0; i < 24; i++ {
			b = append(b, s.First+i) // robbed-bit signalling: all 24 carry calls
		}
		return b, 0
	}
	if s.Type == "bri" {
		return []int{s.First, s.First + 1}, s.First + 2
	}
	if s.Type == "e1" {
		for ts := 1; ts <= 31; ts++ {
			if ts != 16 {
				b = append(b, s.First+ts-1)
			}
		}
		if s.Signalling == "r2" {
			return b, 0 // timeslot 16 carries the circuits' bits, not a D-channel
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
		switch s.Signalling {
		case "", "isdn":
			s.Signalling = ""
		case "em":
			if s.Type != "t1" {
				p.add(f+".signalling", "E&M is for T1 spans")
			}
			switch s.Start {
			case "":
				s.Start = "wink"
			case "wink", "immediate", "delay", "fgd", "cama":
			default:
				p.add(f+".start", "choose wink, immediate, delay, fgd or cama")
			}
			if s.Start == "cama" {
				if len(s.NPD) > 4 {
					p.add(f+".npd", "at most four area codes, for numbering plan digits 0 to 3")
				}
				for _, a := range s.NPD {
					if len(a) != 3 || strings.Trim(a, "0123456789") != "" {
						p.add(f+".npd", "area codes of three digits, like 717")
						break
					}
				}
			}
			switch s.Address {
			case "":
				s.Address = "dtmf"
			case "dtmf", "mf":
			default:
				p.add(f+".address", "choose dtmf or mf")
			}
		case "r2":
			if s.Type != "e1" {
				p.add(f+".signalling", "MFC-R2 is for E1 spans")
			}
		case "ss7":
			if s.Type == "bri" {
				p.add(f+".signalling", "SS7 is for T1 and E1 spans")
			}
			if s.Variant == "" {
				s.Variant = "itu"
				if s.Type == "t1" {
					s.Variant = "ansi"
				}
			}
			if s.Variant != "itu" && s.Variant != "ansi" {
				p.add(f+".variant", "choose itu or ansi")
			}
			ansi := s.Variant == "ansi"
			if _, ok := PointCode(s.OPC, ansi); !ok {
				p.add(f+".opc", "give this end's point code (a number, or %s)", map[bool]string{true: "network-cluster-member", false: "zone-area-point"}[ansi])
			}
			if v, ok := PointCode(s.APC, ansi); !ok {
				p.add(f+".apc", "give the adjacent signalling point's code")
			} else if o, _ := PointCode(s.OPC, ansi); o == v {
				p.add(f+".apc", "the adjacent point code is the same as this end's")
			}
			if s.NI == "" {
				s.NI = "national"
			}
			if s.NI != "national" && s.NI != "international" {
				p.add(f+".ni", "choose national or international")
			}
			if s.SLC < 0 || s.SLC > 15 {
				p.add(f+".slc", "use 0 to 15")
			}
			if s.FirstCIC <= 0 {
				s.FirstCIC = 1
			}
			if max := map[bool]int{true: 1<<14 - 1, false: 1<<12 - 1}[ansi]; s.FirstCIC+30 > max {
				p.add(f+".firstCic", "the circuits' codes go up to %d", max)
			}
		default:
			p.add(f+".signalling", "choose isdn, em, r2 or ss7")
		}
		if s.Number != "" && !lineNumberRe.MatchString(s.Number) {
			p.add(f+".number", "use digits, with + in front for an international number")
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
		if d != 0 {
			b = append(b, d)
		}
		for _, ch := range b {
			if prev, ok := used[ch]; ok {
				p.add(f+".first", "channel %d is also %s", ch, prev)
				break
			}
			used[ch] = fmt.Sprintf("in span %d", i+1)
		}
	}
	// SS7 spans with the same point codes, network indicator and variant are one link set: each
	// span's signalling link needs its own code in it, and the circuits' codes may not overlap.
	type lsKey struct{ variant, opc, apc, ni string }
	slcs, cics := map[lsKey]map[int]int{}, map[lsKey]map[int]int{}
	for i, s := range k.Spans {
		if s.Signalling != "ss7" {
			continue
		}
		f := fmt.Sprintf("system.cards.spans[%d]", i)
		ansi := s.Variant == "ansi"
		o, _ := PointCode(s.OPC, ansi)
		a, _ := PointCode(s.APC, ansi)
		key := lsKey{s.Variant, strconv.Itoa(o), strconv.Itoa(a), s.NI}
		if slcs[key] == nil {
			slcs[key], cics[key] = map[int]int{}, map[int]int{}
		}
		if prev, ok := slcs[key][s.SLC]; ok {
			p.add(f+".slc", "span %d has signalling link code %d in this link set already: give each span's link its own (0 to 15)", prev+1, s.SLC)
		}
		slcs[key][s.SLC] = i
		b, _ := s.Channels()
		for j := range b {
			if prev, ok := cics[key][s.FirstCIC+j]; ok {
				p.add(f+".firstCic", "circuit %d is also on span %d of this link set", s.FirstCIC+j, prev+1)
				break
			}
			cics[key][s.FirstCIC+j] = i
		}
	}
	for i, t := range c.Trunks {
		if t.Protocol == "dahdi" && !k.Enabled {
			p.add(fmt.Sprintf("trunks[%d].protocol", i), "switch on Telephony cards in Settings first")
		}
	}
}
