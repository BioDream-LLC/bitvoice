package pbx

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
)

// NameBox is where an extension's spoken name is kept (beside the greetings): the directory says it.
func NameBox(ext string) string { return "name-" + ext }

// directoryMatches lists the extensions whose name spelled on the keypad starts with digits, by name.
func directoryMatches(cfg *config.Config, d *config.Directory, digits string) []config.Extension {
	var out []config.Extension
	for _, x := range cfg.Extensions {
		if !x.Enabled || (x.Kind != "" && x.Kind != "phone") || (len(d.Members) > 0 && !slices.Contains(d.Members, x.Number)) {
			continue
		}
		for _, k := range config.NameKeys(x.Name, d.Match) {
			if digits != "" && strings.HasPrefix(k, digits) {
				out = append(out, x)
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b config.Extension) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// collectKeys reads up to n digits, starting with first: # ends early, and a pause of wait ends it.
func (c *Call) collectKeys(keys <-chan byte, first string, n int, wait time.Duration) (string, bool) {
	got := first
	for len(got) < n {
		select {
		case k := <-keys:
			if k == '#' {
				return got, false
			}
			if k >= '0' && k <= '9' {
				got += string(k)
			}
		case <-time.After(wait):
			return got, false
		case <-c.cancelled:
			return got, true
		}
	}
	return got, false
}

// nameAudio is an extension's recorded name, or its number read out.
func (e *Engine) nameAudio(cfg *config.Config, lang string, x config.Extension) []int16 {
	if e.Mail != nil {
		if b, err := e.Mail.Greeting(NameBox(x.Number)); err == nil {
			if pcm, err := media.DecodeWAV(b); err == nil && len(pcm) > 0 {
				return pcm
			}
		}
	}
	pcm, _ := e.promptSamples(cfg, lang, prompts.Digits(x.Number)...)
	return pcm
}

// runDirectory lets a caller spell a name and be put through to who they choose.
func (c *Call) runDirectory(id string) {
	cfg := c.e.Config()
	d := cfg.FindDirectory(id)
	if d == nil {
		c.hangup("directory removed", "system")
		return
	}
	lang := promptLang(cfg, d.Language)
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	defer c.caller.ep.Record(nil)
	for try := 0; try < 3; try++ {
		k, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "directory-intro")
		if ended {
			return
		}
		first := ""
		if k >= '0' && k <= '9' {
			first = string(k)
		}
		digits, ended := c.collectKeys(capt.Keys, first, 3, 5*time.Second)
		if ended {
			return
		}
		matches := directoryMatches(cfg, d, digits)
		switch len(matches) {
		case 0:
			if _, ended := c.sayPrompts(lang, nil, toneDeleted, "directory-none"); ended {
				return
			}
			continue
		case 1:
			c.e.publish("call.updated", c.info())
			c.goTo(cfg, config.Destination{Type: "extension", Target: matches[0].Number})
			return
		}
		if x, ended := c.chooseFrom(cfg, lang, matches, capt.Keys); ended {
			return
		} else if x != "" {
			c.e.publish("call.updated", c.info())
			c.goTo(cfg, config.Destination{Type: "extension", Target: x})
			return
		}
	}
	c.sayPrompts(lang, nil, media.ToneConfirm, "goodbye")
	c.hangup("directory: nobody chosen", "system")
}

// chooseFrom reads the matches out eight at a time ("Dr Lee: to call this person, press 1"), with 9
// for the next eight, and returns the extension chosen ("" when none).
func (c *Call) chooseFrom(cfg *config.Config, lang string, matches []config.Extension, keys <-chan byte) (string, bool) {
	const page = 8
	for start, rounds := 0, 0; rounds < 2*((len(matches)+page-1)/page); rounds++ {
		end := min(start+page, len(matches))
		var pcm []int16
		gap := make([]int16, media.Rate/4)
		for i, x := range matches[start:end] {
			pcm = append(pcm, c.e.nameAudio(cfg, lang, x)...)
			press, _ := c.e.promptSamples(cfg, lang, "directory-press", "digit-"+strconv.Itoa(i+1))
			pcm = append(append(append(pcm, gap...), press...), gap...)
		}
		if len(matches) > page {
			more, _ := c.e.promptSamples(cfg, lang, "directory-more")
			pcm = append(pcm, more...)
		}
		if len(pcm) == 0 {
			pcm = media.ToneSamples(media.ToneBeep)
		}
		k, ended := c.play(pcm, keys)
		if ended {
			return "", true
		}
		if k == 0 {
			select {
			case k = <-keys:
			case <-time.After(5 * time.Second):
			case <-c.cancelled:
				return "", true
			}
		}
		switch {
		case k >= '1' && k <= '8' && start+int(k-'1') < end:
			return matches[start+int(k-'1')].Number, false
		case k == '9' && len(matches) > page:
			start = end % len(matches)
		case k == 0:
			// nothing pressed: read the same names again
		}
	}
	return "", false
}
