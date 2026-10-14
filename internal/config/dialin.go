package config

import (
	"fmt"
	"slices"
	"strings"
)

// Directory is a dial-by-name directory: callers spell the first letters of a name on the keypad,
// hear who matches and press a key to be put through.
type Directory struct {
	ID     string `json:"id"`
	Number string `json:"number"` // optional: dial it directly
	Name   string `json:"name"`
	// Match is the part of the name spelled: last (the last word, the default), first, or any word.
	Match string `json:"match"`
	// Members are the extensions listed; empty lists every enabled extension with a phone.
	Members []string `json:"members"`
	// Language of its prompts; empty for the system's.
	Language string `json:"language,omitempty"`
}

// DialIn (DISA) lets someone call in from outside, prove who they are, and call out again as the
// company: from a mobile, to make calls with the office's caller ID and rates.
type DialIn struct {
	ID     string `json:"id"`
	Number string `json:"number"` // optional: dial it directly, or route a phone number to it
	Name   string `json:"name"`
	// PIN callers enter first. AllowedCallers, when set, are the only numbers that may use it (with
	// the PIN, when one is set too). At least one of the two is required.
	PIN            string   `json:"pin"`
	AllowedCallers []string `json:"allowedCallers"`
	// As is the extension the calls are made as: its outside-call permission, caller ID and routes.
	As string `json:"as"`
	// Language of its prompts; empty for the system's.
	Language string `json:"language,omitempty"`
}

// FindDirectory returns a directory by ID.
func (c *Config) FindDirectory(id string) *Directory {
	for i := range c.Directories {
		if c.Directories[i].ID == id {
			return &c.Directories[i]
		}
	}
	return nil
}

// FindDirectoryNumber returns a directory by the number dialled.
func (c *Config) FindDirectoryNumber(n string) *Directory {
	for i := range c.Directories {
		if n != "" && c.Directories[i].Number == n {
			return &c.Directories[i]
		}
	}
	return nil
}

// FindDialIn returns a dial-in by ID.
func (c *Config) FindDialIn(id string) *DialIn {
	for i := range c.DialIns {
		if c.DialIns[i].ID == id {
			return &c.DialIns[i]
		}
	}
	return nil
}

// FindDialInNumber returns a dial-in by the number dialled.
func (c *Config) FindDialInNumber(n string) *DialIn {
	for i := range c.DialIns {
		if n != "" && c.DialIns[i].Number == n {
			return &c.DialIns[i]
		}
	}
	return nil
}

func (c *Config) validateDialIn(p *Problems, claim func(field, number, owner string), checkID func(field string, id *string)) {
	for i := range c.Directories {
		d := &c.Directories[i]
		f := fmt.Sprintf("directories[%d]", i)
		checkID(f+".id", &d.ID)
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if d.Number != "" {
			if !numberRe.MatchString(d.Number) {
				p.add(f+".number", "directory numbers are 2 to 8 digits")
			} else {
				claim(f+".number", d.Number, "directory "+d.Name)
			}
		}
		switch d.Match {
		case "":
			d.Match = "last"
		case "last", "first", "any":
		default:
			p.add(f+".match", "choose last, first or any")
		}
		for _, m := range d.Members {
			if c.FindExtension(m) == nil {
				p.add(f+".members", "no extension %s", m)
			}
		}
		if d.Language != "" && !langRe.MatchString(d.Language) {
			p.add(f+".language", "use a language code such as en, es or pt-BR")
		}
	}
	for i := range c.DialIns {
		d := &c.DialIns[i]
		f := fmt.Sprintf("dialIns[%d]", i)
		checkID(f+".id", &d.ID)
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if d.Number != "" {
			if !numberRe.MatchString(d.Number) {
				p.add(f+".number", "dial-in numbers are 2 to 8 digits")
			} else {
				claim(f+".number", d.Number, "dial-in "+d.Name)
			}
		}
		if d.PIN != "" && !pinRe.MatchString(d.PIN) {
			p.add(f+".pin", "a PIN is 4 to 10 digits")
		}
		for j, a := range d.AllowedCallers {
			a = strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' || r == '+' {
					return r
				}
				return -1
			}, a)
			d.AllowedCallers[j] = a
			if !dialRe.MatchString(a) {
				p.add(f+".allowedCallers", "%q is not a phone number", a)
			}
		}
		d.AllowedCallers = slices.DeleteFunc(d.AllowedCallers, func(s string) bool { return s == "" })
		if d.PIN == "" && len(d.AllowedCallers) == 0 {
			p.add(f+".pin", "set a PIN or the numbers allowed to call in: an open dial-in lets anyone call out at your cost")
		}
		if x := c.FindExtension(d.As); x == nil {
			p.add(f+".as", "choose the extension the calls are made as")
		} else if !x.Outbound {
			p.add(f+".as", "extension %s may not call outside numbers", d.As)
		}
		if d.Language != "" && !langRe.MatchString(d.Language) {
			p.add(f+".language", "use a language code such as en, es or pt-BR")
		}
	}
}

// Keypad turns letters into the digits they are on (ITU E.161: 2 abc … 9 wxyz); accents are dropped
// and anything that is not a letter is left out.
func Keypad(s string) string {
	const keys = "22233344455566677778889999"
	var b strings.Builder
	for _, r := range strings.ToLower(foldAccents(s)) {
		if r >= 'a' && r <= 'z' {
			b.WriteByte(keys[r-'a'])
		}
	}
	return b.String()
}

func foldAccents(s string) string {
	return strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "ą", "a",
		"Á", "a", "À", "a", "Â", "a", "Ä", "a", "Ã", "a", "Å", "a",
		"ç", "c", "ć", "c", "č", "c", "Ç", "c", "Č", "c",
		"é", "e", "è", "e", "ê", "e", "ë", "e", "ę", "e", "É", "e", "È", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i", "Í", "i",
		"ł", "l", "Ł", "l", "ñ", "n", "ń", "n", "Ñ", "n",
		"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "Ó", "o", "Ö", "o",
		"ś", "s", "š", "s", "ß", "ss", "Ś", "s", "Š", "s",
		"ú", "u", "ù", "u", "û", "u", "ü", "u", "Ú", "u", "Ü", "u",
		"ý", "y", "ÿ", "y", "ź", "z", "ż", "z", "ž", "z", "Ž", "z",
	).Replace(s)
}

// NameKeys returns the keypad digits of the part of a name a directory matches on.
func NameKeys(name, match string) []string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return nil
	}
	switch match {
	case "first":
		return []string{Keypad(words[0])}
	case "any":
		out := make([]string, len(words))
		for i, w := range words {
			out[i] = Keypad(w)
		}
		return out
	}
	return []string{Keypad(words[len(words)-1])}
}
