package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Toggle is a day/night switch (call flow control): calls sent to it go to Normal, or to Override
// while someone has switched it over, with its feature code (*28 then its code), its busy lamp key, the
// web interface or the API. It stays as it was left, across restarts, until switched back.
type Toggle struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Code is the digits dialled after the toggle feature code: *28 then 1 switches toggle 1. A phone's
	// busy lamp key watching *281 is lit while it is switched over.
	Code     string      `json:"code"`
	Normal   Destination `json:"normal"`
	Override Destination `json:"override"`
	// Allowed are the extensions that may switch it from a phone; empty lets any extension.
	Allowed []string `json:"allowed"`
}

// PINSet is a list of PINs an outbound route asks for before it calls out: callers enter one after
// dialling, and the call goes through only with a right one.
type PINSet struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	PINs []string `json:"pins"`
	// Record writes the PIN used into the call record as its account code, for billing calls to
	// clients or projects.
	Record bool `json:"record"`
}

// CallerIDLookup names callers the provider sent no name for: from a web address (any service or
// CRM answering with a name) or OpenCNAM. An inbound route chooses which one it uses.
type CallerIDLookup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is http (URL, with {number} where the caller's number goes) or opencnam.
	Kind string `json:"kind"`
	URL  string `json:"url"`
	// Field, for http, is where the name is in a JSON answer (a dotted path such as data.name); empty
	// takes the first line of a plain-text answer.
	Field string `json:"field"`
	// Username and Password are basic authentication for http; for opencnam, the account SID and the
	// auth token.
	Username string `json:"username"`
	Password string `json:"password"`
	// TimeoutMS is how long a call waits for the answer (default 1500, at most 5000); a slow or broken
	// service never holds a call up longer, and the call goes on without a name.
	TimeoutMS int `json:"timeoutMs"`
	// CacheHours keeps each answer (default 24; 0 for the default, -1 to never keep).
	CacheHours int `json:"cacheHours"`
}

// FindToggle returns a toggle by ID.
func (c *Config) FindToggle(id string) *Toggle {
	for i := range c.Toggles {
		if c.Toggles[i].ID == id {
			return &c.Toggles[i]
		}
	}
	return nil
}

// FindToggleCode returns a toggle by its code.
func (c *Config) FindToggleCode(code string) *Toggle {
	for i := range c.Toggles {
		if code != "" && c.Toggles[i].Code == code {
			return &c.Toggles[i]
		}
	}
	return nil
}

// FindPINSet returns a PIN set by ID.
func (c *Config) FindPINSet(id string) *PINSet {
	for i := range c.PINSets {
		if c.PINSets[i].ID == id {
			return &c.PINSets[i]
		}
	}
	return nil
}

// FindLookup returns a caller ID lookup by ID.
func (c *Config) FindLookup(id string) *CallerIDLookup {
	for i := range c.Lookups {
		if c.Lookups[i].ID == id {
			return &c.Lookups[i]
		}
	}
	return nil
}

var (
	toggleCodeRe = regexp.MustCompile(`^[0-9]{1,3}$`)
	jsonPathRe   = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
)

func (c *Config) validateFlow(p *Problems, checkID func(field string, id *string), checkDest func(field string, d *Destination)) {
	codes := map[string]bool{}
	for i := range c.Toggles {
		t := &c.Toggles[i]
		f := fmt.Sprintf("toggles[%d]", i)
		checkID(f+".id", &t.ID)
		t.Name = strings.TrimSpace(t.Name)
		if t.Name == "" {
			p.add(f+".name", "enter a name")
		}
		t.Code = strings.TrimSpace(t.Code)
		switch {
		case !toggleCodeRe.MatchString(t.Code):
			p.add(f+".code", "a toggle's code is 1 to 3 digits, dialled after %s", c.System.Codes.Toggle)
		case codes[t.Code]:
			p.add(f+".code", "another toggle has code %s", t.Code)
		}
		codes[t.Code] = true
		for _, d := range []*Destination{&t.Normal, &t.Override} {
			if d.Type == "toggle" {
				p.add(f+".override", "a toggle cannot lead to another toggle")
			}
		}
		checkDest(f+".normal", &t.Normal)
		checkDest(f+".override", &t.Override)
		for _, m := range t.Allowed {
			if c.FindExtension(m) == nil {
				p.add(f+".allowed", "no extension %s", m)
			}
		}
	}
	// One toggle code may not be the start of another (*281 and *2812): the shorter would always win.
	for a := range codes {
		for b := range codes {
			if a != b && strings.HasPrefix(b, a) {
				for i := range c.Toggles {
					if c.Toggles[i].Code == b {
						p.add(fmt.Sprintf("toggles[%d].code", i), "code %s starts with code %s of another toggle", b, a)
					}
				}
			}
		}
	}
	for i := range c.PINSets {
		s := &c.PINSets[i]
		f := fmt.Sprintf("pinSets[%d]", i)
		checkID(f+".id", &s.ID)
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			p.add(f+".name", "enter a name")
		}
		seen := map[string]bool{}
		out := s.PINs[:0]
		for _, pin := range s.PINs {
			pin = strings.TrimSpace(pin)
			if pin == "" {
				continue
			}
			if !pinRe.MatchString(pin) {
				p.add(f+".pins", "%q is not a PIN: PINs are 4 to 10 digits", pin)
			} else if seen[pin] {
				p.add(f+".pins", "PIN %s is listed twice", pin)
			}
			seen[pin] = true
			out = append(out, pin)
		}
		s.PINs = out
		if len(s.PINs) == 0 {
			p.add(f+".pins", "add at least one PIN")
		}
	}
	for i := range c.OutboundRoutes {
		r := &c.OutboundRoutes[i]
		if r.PINSet != "" && c.FindPINSet(r.PINSet) == nil {
			p.add(fmt.Sprintf("outboundRoutes[%d].pinSet", i), "no PIN set %s", r.PINSet)
		}
	}
	for i := range c.Lookups {
		l := &c.Lookups[i]
		f := fmt.Sprintf("lookups[%d]", i)
		checkID(f+".id", &l.ID)
		l.Name = strings.TrimSpace(l.Name)
		if l.Name == "" {
			p.add(f+".name", "enter a name")
		}
		l.URL = strings.TrimSpace(l.URL)
		switch l.Kind {
		case "http":
			u, err := url.Parse(strings.ReplaceAll(l.URL, "{number}", "0"))
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				p.add(f+".url", "enter an http:// or https:// address, with {number} where the number goes")
			} else if !strings.Contains(l.URL, "{number}") {
				p.add(f+".url", "put {number} in the address where the caller's number goes")
			}
			l.Field = strings.TrimSpace(l.Field)
			if l.Field != "" && !jsonPathRe.MatchString(l.Field) {
				p.add(f+".field", "a field is a dotted path such as name or data.callerName")
			}
		case "opencnam":
			if l.Username == "" || l.Password == "" {
				p.add(f+".username", "enter the OpenCNAM account SID and auth token")
			}
			if l.URL != "" {
				if u, err := url.Parse(l.URL); err != nil || u.Scheme != "https" || u.Host == "" {
					p.add(f+".url", "leave empty for OpenCNAM's own address, or enter an https:// address")
				}
			}
		default:
			p.add(f+".kind", "choose a web address (http) or OpenCNAM")
		}
		if l.TimeoutMS == 0 {
			l.TimeoutMS = 1500
		}
		if l.TimeoutMS < 100 || l.TimeoutMS > 5000 {
			p.add(f+".timeoutMs", "wait 100 to 5000 milliseconds: calls wait while it looks")
		}
		if l.CacheHours < -1 || l.CacheHours > 24*90 {
			p.add(f+".cacheHours", "keep answers 1 to 2160 hours, 0 for a day, or -1 to never keep them")
		}
	}
	for i := range c.InboundRoutes {
		r := &c.InboundRoutes[i]
		if r.Lookup != "" && c.FindLookup(r.Lookup) == nil {
			p.add(fmt.Sprintf("inboundRoutes[%d].lookup", i), "no caller ID lookup %s", r.Lookup)
		}
	}
}

// ToggleAllowed says whether an extension may switch a toggle from its phone.
func (t *Toggle) ToggleAllowed(ext string) bool {
	return len(t.Allowed) == 0 || slices.Contains(t.Allowed, ext)
}
