package pbx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/script"
)

// CallScriptRun is the outcome of one call script, published for the script editor's live log.
type CallScriptRun struct {
	Script string    `json:"script"`
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	// Decision says how it ended: hung up, handed on, or the fallback (and why).
	Decision string   `json:"decision"`
	Error    string   `json:"error,omitempty"`
	Steps    []string `json:"steps,omitempty"`
	Log      []string `json:"log,omitempty"`
	Millis   float64  `json:"millis"`
}

// runCallScript answers to a call script and lets it run the call. Whatever goes wrong, the caller
// ends up at the script's fallback (unless they hung up, or the script handed the call on).
func (c *Call) runCallScript(id string) {
	cfg := c.e.Config()
	s := cfg.FindScript(id)
	if s == nil {
		c.hangup("script removed", "system")
		return
	}
	h := &callHost{c: c, cfg: cfg, keys: media.NewCapture(1)}
	c.caller.ep.Record(h.keys.Packet)
	c.mu.Lock()
	in := script.Call{From: c.from, FromName: c.fromName, To: c.to, Direction: c.direction, Trunk: c.trunk}
	c.mu.Unlock()
	now := time.Now().In(cfg.System.Location())
	in.Weekday, in.Hour, in.Minute, in.Date = int(now.Weekday()), now.Hour(), now.Minute(), now.Format("2006-01-02")

	start := time.Now()
	out, err := script.RunCall(s.Language, s.Source, in, h, time.Duration(max(s.MaxMinutes, 0))*time.Minute)
	run := CallScriptRun{Script: s.ID, At: start.UTC(), From: in.From, To: in.To, Steps: out.Steps, Log: out.Log,
		Millis: float64(time.Since(start).Microseconds()) / 1000}
	if !h.handedOver {
		c.caller.ep.Record(nil)
	}
	switch {
	case out.Ended:
		run.Decision = "ended: " + h.ending
	case err != nil:
		run.Error = err.Error()
		run.Decision = "fallback: " + s.Fallback.Type + " " + s.Fallback.Target
		c.e.Log.Warn("call script failed; the call goes to its fallback", "script", s.Name, "err", err)
		c.goTo(cfg, s.Fallback)
	case out.Next != nil:
		d, ok := h.destination(*out.Next)
		if !ok {
			run.Error = "the script chose " + out.Next.String() + ", which is not here"
			run.Decision = "fallback: " + s.Fallback.Type + " " + s.Fallback.Target
			c.goTo(cfg, s.Fallback)
			break
		}
		run.Decision = "to " + d.Type + " " + d.Target
		c.goTo(cfg, d)
	default:
		run.Decision = "fallback (the script ended without hanging up or handing the call on): " + s.Fallback.Type + " " + s.Fallback.Target
		c.goTo(cfg, s.Fallback)
	}
	c.e.publish("script.ran", run)
}

// callHost carries out a call script's steps on its call.
type callHost struct {
	c          *Call
	cfg        *config.Config
	keys       *media.Capture // keys pressed while the script runs (not while it records)
	handedOver bool
	ending     string
	// typeahead holds keys pressed during a sound or a wait: the next collect starts with them.
	typeahead string
}

func (h *callHost) gone() error {
	select {
	case <-h.c.cancelled:
		h.ending = "the caller hung up"
		return script.ErrCallEnded
	default:
	}
	return nil
}

// playKeys plays audio and returns a key pressed during it.
func (h *callHost) playKeys(pcm []int16) (string, error) {
	if err := h.gone(); err != nil {
		return "", err
	}
	k, ended := h.c.play(pcm, h.keys.Keys)
	if ended {
		h.ending = "the caller hung up"
		return "", script.ErrCallEnded
	}
	if k == 0 {
		return "", nil
	}
	h.typeahead += string(k)
	return string(k), nil
}

var scriptTones = map[string]media.Tone{"beep": media.ToneBeep, "confirm": media.ToneConfirm, "error": media.ToneError,
	"off": media.ToneOff, "hold": media.ToneHold}

// sound finds what a name plays: a prompt (in the caller's language), "digits:123", or a tone.
func (h *callHost) sound(name string) ([]int16, error) { return h.c.e.soundFor(h.cfg, name) }

// soundFor finds what a sound name plays, for call scripts and ARI: a prompt from Sounds in the system's
// language ("goodbye"), "digits:123" said digit by digit, or a tone (beep, confirm, error, off, hold).
// A prompt not recorded yet is a beep.
func (e *Engine) soundFor(cfg *config.Config, name string) ([]int16, error) {
	lang := promptLang(cfg, "")
	if t, ok := scriptTones[name]; ok {
		return media.ToneSamples(t), nil
	}
	if ds, ok := strings.CutPrefix(name, "digits:"); ok {
		var keys []string
		for _, d := range ds {
			if d >= '0' && d <= '9' {
				keys = append(keys, "digit-"+string(d))
			}
		}
		if pcm, ok := e.promptSamples(cfg, lang, keys...); ok {
			return pcm, nil
		}
		return media.ToneSamples(media.ToneBeep), nil
	}
	if !slices.ContainsFunc(prompts.All, func(p prompts.Prompt) bool { return p.Key == name }) {
		return nil, fmt.Errorf("there is no prompt or tone named %q (see Sounds)", name)
	}
	if pcm, ok := e.promptSamples(cfg, lang, name); ok {
		return pcm, nil
	}
	return media.ToneSamples(media.ToneBeep), nil // not recorded yet: a beep, as everywhere else
}

func (h *callHost) Play(name string) (string, error) {
	pcm, err := h.sound(name)
	if err != nil {
		return "", err
	}
	return h.playKeys(pcm)
}

// Say speaks text with the speech service in Settings, AI. Without one it beeps: a call never waits
// for speech that is not there.
func (h *callHost) Say(text string) (string, error) {
	a := h.cfg.System.AI
	if !a.Enabled || a.SpeechModel == "" || strings.TrimSpace(text) == "" {
		return h.playKeys(media.ToneSamples(media.ToneBeep))
	}
	client := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel, TranscribeURL: a.TranscribeURL, TranscribeKey: a.TranscribeKey, TranscribeModel: a.TranscribeModel, Allow: h.c.e.AIAllow}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wav, err := client.Speak(ctx, a.SpeechModel, a.Voice, text)
	if err != nil {
		h.c.e.Log.Warn("call script could not speak", "err", err)
		return h.playKeys(media.ToneSamples(media.ToneBeep))
	}
	pcm, err := media.DecodeWAV(wav)
	if err != nil {
		return h.playKeys(media.ToneSamples(media.ToneBeep))
	}
	return h.playKeys(pcm)
}

func (h *callHost) waitKey(d time.Duration) (string, error) {
	if d <= 0 {
		return "", h.gone()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case k := <-h.keys.Keys:
		return string(k), nil
	case <-t.C:
		return "", nil
	case <-h.c.cancelled:
		h.ending = "the caller hung up"
		return "", script.ErrCallEnded
	}
}

func (h *callHost) Wait(seconds float64) (string, error) {
	k, err := h.waitKey(time.Duration(seconds * float64(time.Second)))
	h.typeahead += k
	return k, err
}

func (h *callHost) Collect(o script.CollectOptions) (string, error) {
	var got strings.Builder
	var err error
	if h.typeahead == "" { // keys already pressed skip the prompt
		switch {
		case o.Say != "":
			_, err = h.Say(o.Say)
		case o.Prompt != "":
			_, err = h.Play(o.Prompt)
		}
		if err != nil {
			return "", err
		}
	}
	pending := h.typeahead
	h.typeahead = ""
	wait := time.Duration(o.Timeout * float64(time.Second))
	for got.Len() < o.Max {
		var k string
		if pending != "" {
			k, pending = pending[:1], pending[1:]
		} else if k, err = h.waitKey(wait); err != nil {
			return got.String(), err
		}
		if k == "" {
			break // time is up
		}
		if o.End != "" && k == o.End {
			break
		}
		got.WriteString(k)
		wait = time.Duration(o.InterDigit * float64(time.Second))
	}
	return got.String(), nil
}

func (h *callHost) Record(o script.RecordOptions) (int, error) {
	x := h.cfg.FindExtension(o.Box)
	if x == nil || !x.Voicemail.Enabled || h.c.e.Mail == nil {
		return 0, fmt.Errorf("there is no mailbox %s", o.Box)
	}
	limit := x.Voicemail.MaxSeconds
	if limit <= 0 {
		limit = 120
	}
	if o.Seconds > 0 && o.Seconds < limit {
		limit = o.Seconds
	}
	if o.Beep {
		if _, err := h.playKeys(media.ToneSamples(media.ToneBeep)); err != nil {
			return 0, err
		}
	}
	capt := media.NewCapture(limit)
	h.c.caller.ep.Record(capt.Packet)
	defer h.c.caller.ep.Record(h.keys.Packet)
	started := time.Now()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	limitAt := time.NewTimer(time.Duration(limit) * time.Second) // a phone that stops sending still ends
	defer limitAt.Stop()
	var ended error
wait:
	for {
		select {
		case <-limitAt.C:
			break wait
		case <-h.c.cancelled:
			h.ending = "the caller hung up"
			ended = script.ErrCallEnded
			break wait
		case k := <-capt.Keys:
			if k == '#' {
				break wait
			}
		case <-tick.C:
			if capt.Full() {
				break wait
			}
		}
	}
	pcm := media.TrimSilence(capt.Stop())
	h.c.saveMessage(o.Box, pcm, started)
	if len(pcm) < media.Rate {
		return 0, ended
	}
	return (len(pcm) + media.Rate/2) / media.Rate, ended
}

// destination turns what a script names into a destination here: a number is an extension, ring
// group or queue, else an outside number through the outbound routes.
func (h *callHost) destination(d script.Destination) (config.Destination, bool) {
	if d.Type == "number" {
		n := strings.TrimSpace(d.Target)
		switch {
		case h.cfg.FindExtension(n) != nil:
			return config.Destination{Type: "extension", Target: n}, true
		case h.cfg.FindRingGroup(n) != nil:
			return config.Destination{Type: "ringgroup", Target: n}, true
		case h.cfg.FindQueue(n) != nil:
			return config.Destination{Type: "queue", Target: n}, true
		}
		return config.Destination{Type: "number", Target: n}, n != ""
	}
	return config.Destination{Type: d.Type, Target: d.Target}, d.Type != ""
}

// planFor is what ringing a script's destination means; outside numbers go out through the routes.
func (h *callHost) planFor(d config.Destination) (plan, error) {
	if d.Type == "number" {
		n := cleanNumber(d.Target)
		if slices.Contains(h.cfg.System.EmergencyNumbers, n) {
			return plan{}, errors.New("scripts cannot call emergency numbers")
		}
		p := h.c.e.outbound(h.cfg, nil, n)
		if p.reject != 0 || p.pinSet != "" {
			return plan{}, fmt.Errorf("no outbound route takes %s", n)
		}
		p.toName = n
		return p, nil
	}
	h.c.mu.Lock()
	sc := scriptCall{from: h.c.from, fromName: h.c.fromName, to: h.c.to, direction: h.c.direction, trunk: h.c.trunk, real: true}
	h.c.mu.Unlock()
	p := h.c.e.resolveFor(h.cfg, d, 1, sc)
	if p.reject == 404 {
		return plan{}, fmt.Errorf("there is no %s %s here", d.Type, d.Target)
	}
	return p, nil
}

func (h *callHost) Dial(sd script.Destination, ringSeconds int) (bool, error) {
	if err := h.gone(); err != nil {
		return false, err
	}
	d, ok := h.destination(sd)
	if !ok {
		return false, fmt.Errorf("there is no %s here", sd)
	}
	p, err := h.planFor(d)
	if err != nil {
		return false, err
	}
	if p.reject != 0 {
		return false, nil // nobody to ring there now (no phone signed in, a hang-up): the script carries on
	}
	if p.local != "" {
		// Voicemail, a menu, a queue: BitVoice takes the call itself, so ringing it hands it over.
		if err := h.Transfer(sd); err != nil {
			return false, err
		}
		return true, script.ErrHandedOver
	}
	ring := time.Duration(ringSeconds) * time.Second
	for i := range p.stages {
		p.stages[i].timeout = min(p.stages[i].timeout, ring)
		if p.stages[i].timeout <= 0 {
			p.stages[i].timeout = ring
		}
	}
	if h.c.ringAnswered(p) {
		h.handedOver = true
		h.ending = "answered at " + d.Target
		h.c.caller.ep.Record(nil)
		return true, script.ErrHandedOver
	}
	if err := h.gone(); err != nil {
		return false, err
	}
	return false, nil
}

func (h *callHost) Transfer(sd script.Destination) error {
	if err := h.gone(); err != nil {
		return err
	}
	d, ok := h.destination(sd)
	if !ok {
		return fmt.Errorf("there is no %s here", sd)
	}
	if d.Type == "number" {
		p, err := h.planFor(d)
		if err != nil {
			return err
		}
		h.handedOver = true
		h.ending = "handed to " + d.Target
		h.c.caller.ep.Record(nil)
		if !h.c.ringAnswered(p) {
			h.c.hangup("no answer", "system")
		}
		return nil
	}
	if _, err := h.planFor(d); err != nil {
		return err
	}
	h.handedOver = true
	h.ending = "handed to " + d.Type + " " + d.Target
	h.c.caller.ep.Record(nil)
	h.c.goTo(h.cfg, d)
	return nil
}

func (h *callHost) Hangup() error {
	h.ending = "the script hung up"
	h.c.hangup("hung up by script", "system")
	return nil
}
