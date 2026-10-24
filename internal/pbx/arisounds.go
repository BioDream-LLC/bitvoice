package pbx

import (
	"strings"

	"github.com/biodream-llc/bitvoice/internal/prompts"
)

// ARISound is one sound for ARI's GET /sounds: its id, its words, and the languages it is in.
type ARISound struct {
	ID        string
	Text      string
	Languages []string
}

// ARISounds lists the sounds a program can play (ARI's GET /sounds): BitVoice's prompts, in the
// languages each is recorded in, and the sounds uploaded for AGI scripts.
func (e *Engine) ARISounds() []ARISound {
	cfg := e.Config()
	var langs []string
	for _, l := range prompts.Languages {
		langs = append(langs, l.Code)
	}
	langs = cfg.PromptLanguageCodes(langs)
	var out []ARISound
	for _, p := range prompts.All {
		s := ARISound{ID: p.Key, Text: p.Text["en"]}
		if e.Mail != nil {
			for _, l := range langs {
				if _, err := e.Mail.Greeting(prompts.Box(l, p.Key)); err == nil {
					s.Languages = append(s.Languages, l)
				}
			}
		}
		out = append(out, s)
	}
	if e.AGI != nil && e.AGI.Library != nil {
		for _, x := range e.AGI.Library.List() {
			if x.Recorded {
				continue
			}
			id, lang := x.Name, "en"
			if first, rest, ok := strings.Cut(x.Name, "/"); ok && len(first) == 2 {
				id, lang = rest, first
			}
			out = append(out, ARISound{ID: id, Languages: []string{lang}})
		}
	}
	return out
}
