package pbx

import (
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
)

// promptLang picks the language a caller hears: the one asked for, else the system's, else English.
func promptLang(cfg *config.Config, want string) string {
	if want != "" {
		return want
	}
	if cfg.System.Language != "" {
		return cfg.System.Language
	}
	return "en"
}

// promptAudio returns a prompt's audio in a language, falling back to the system's language and then
// English; nil when none of them has it recorded.
func (e *Engine) promptAudio(cfg *config.Config, lang, key string) []int16 {
	if e.Mail == nil {
		return nil
	}
	for _, l := range []string{lang, cfg.System.Language, "en"} {
		if l == "" {
			continue
		}
		if b, err := e.Mail.Greeting(prompts.Box(l, key)); err == nil {
			if pcm, err := media.DecodeWAV(b); err == nil && len(pcm) > 0 {
				return pcm
			}
		}
	}
	return nil
}

// promptSamples joins prompts into one piece of audio with a short pause between them. Prompts with no
// audio are left out; ok is false when none had audio, so the caller can fall back to a tone.
func (e *Engine) promptSamples(cfg *config.Config, lang string, keys ...string) (pcm []int16, ok bool) {
	gap := make([]int16, media.Rate/8)
	for _, k := range keys {
		a := e.promptAudio(cfg, lang, k)
		if a == nil {
			continue
		}
		if ok {
			pcm = append(pcm, gap...)
		}
		pcm, ok = append(pcm, a...), true
	}
	return pcm, ok
}

// sayPrompts plays prompts to the caller, or the fallback tone when none of them is recorded. It
// returns a key pressed during it, and whether the call ended.
func (c *Call) sayPrompts(lang string, keys <-chan byte, fallback media.Tone, names ...string) (byte, bool) {
	cfg := c.e.Config()
	pcm, ok := c.e.promptSamples(cfg, promptLang(cfg, lang), names...)
	if !ok {
		pcm = media.ToneSamples(fallback)
	}
	return c.play(pcm, keys)
}
