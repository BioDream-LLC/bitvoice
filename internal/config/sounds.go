package config

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// MusicClass is a named set of hold music tracks, played in name order or shuffled.
type MusicClass struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Shuffle bool   `json:"shuffle"`
	// Stream, when set, is an internet radio or Icecast/Shoutcast address (MP3) played instead of the
	// class's tracks; the tracks (or the hold tone) play while it cannot be reached.
	Stream string `json:"stream"`
}

var (
	langRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,30}$`)
)

// FindMusic returns a hold music class by ID.
func (c *Config) FindMusic(id string) *MusicClass {
	for i := range c.System.Music {
		if c.System.Music[i].ID == id {
			return &c.System.Music[i]
		}
	}
	return nil
}

func (c *Config) validateSounds(p *Problems) {
	s := &c.System
	if s.Language == "" {
		s.Language = "en"
	}
	if !langRe.MatchString(s.Language) {
		p.add("system.language", "use a language code such as en, es or pt-BR")
	}
	seen := map[string]bool{}
	for i, l := range s.PromptLanguages {
		l = strings.TrimSpace(l)
		s.PromptLanguages[i] = l
		if !langRe.MatchString(l) {
			p.add("system.promptLanguages", "%q is not a language code such as en, es or pt-BR", l)
		} else if seen[l] {
			p.add("system.promptLanguages", "%s is listed twice", l)
		}
		seen[l] = true
	}
	ids := map[string]bool{}
	for i := range s.Music {
		m := &s.Music[i]
		m.Name = strings.TrimSpace(m.Name)
		f := "system.music." + m.ID
		if !idRe.MatchString(m.ID) || ids[m.ID] || m.ID == "tone" {
			p.add(f+".id", "each hold music class needs its own ID")
		}
		ids[m.ID] = true
		if m.Name == "" {
			p.add(f+".name", "give the hold music a name")
		}
		m.Stream = strings.TrimSpace(m.Stream)
		if m.Stream != "" {
			if u, err := url.Parse(m.Stream); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				p.add(f+".stream", "enter the stream's http:// or https:// address (an MP3 stream)")
			}
		}
	}
	music := func(field, v string) {
		if v != "" && v != "tone" && !ids[v] {
			p.add(field, "no hold music %q", v)
		}
	}
	music("system.holdMusic", s.HoldMusic)
	if s.HoldMusic == "tone" {
		s.HoldMusic = ""
	}
	lang := func(field, v string) {
		if v != "" && !langRe.MatchString(v) {
			p.add(field, "use a language code such as en, es or pt-BR")
		}
	}
	for i := range c.Extensions {
		x := &c.Extensions[i]
		music("extensions."+x.Number+".holdMusic", x.HoldMusic)
		lang("extensions."+x.Number+".language", x.Language)
	}
	for i := range c.Queues {
		q := &c.Queues[i]
		music("queues."+q.Number+".music", q.Music)
		lang("queues."+q.Number+".language", q.Language)
	}
}

// PromptLanguageCodes is every language callers can hear: the shipped ones and those added.
func (c *Config) PromptLanguageCodes(shipped []string) []string {
	out := slices.Clone(shipped)
	for _, l := range c.System.PromptLanguages {
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}
