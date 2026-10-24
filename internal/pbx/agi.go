package pbx

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/agi"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// AGIHost is what AGI scripts use on this server: their sounds and recordings, Asterisk's database,
// and the folder of AGI programs.
type AGIHost struct {
	Library  *agi.Library
	DB       *agi.DB
	Programs string // DATA/agi-bin
	Version  string
}

// AGIRun is the outcome of one AGI script, published as agi.ran for the screen's live log.
type AGIRun struct {
	Script   string    `json:"script"`
	At       time.Time `json:"at"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Commands int       `json:"commands"`
	Decision string    `json:"decision"`
	Error    string    `json:"error,omitempty"`
	Log      []string  `json:"log,omitempty"`
	Millis   float64   `json:"millis"`
}

// agiConnectTimeout bounds connecting to a FastAGI server.
var agiConnectTimeout = 5 * time.Second

// runAGI runs an AGI script on an answered call. Whatever goes wrong, the caller ends up at the
// number the script chose or at the script's fallback (unless they hung up, or the script handed
// the call on).
func (c *Call) runAGI(name string) {
	cfg := c.e.Config()
	s := cfg.FindAGI(name)
	if s == nil || c.e.AGI == nil {
		c.hangup("AGI script removed", "system")
		return
	}
	start := time.Now()
	c.mu.Lock()
	run := AGIRun{Script: s.Name, At: start.UTC(), From: c.from, To: c.to}
	env := agi.Env{Request: s.Address, Language: promptLang(cfg, ""), Type: "PJSIP", UniqueID: c.ID,
		Version: c.e.AGI.Version, CallerID: c.from, CallerIDName: c.fromName, DNID: c.to, Context: "from-bitvoice",
		Extension: c.to, AccountCode: c.account, Args: s.Args}
	c.mu.Unlock()
	ch := newAGIChannel(c, cfg)
	defer ch.stop()
	finish := func(err error, next string, commands int) {
		run.Millis = float64(time.Since(start).Microseconds()) / 1000
		run.Commands = commands
		run.Log = ch.logLines()
		if err != nil {
			run.Error = err.Error()
		}
		ch.release()
		switch {
		case ch.handedOver():
			run.Decision = "handed on: " + ch.ending
		case c.isEnded():
			run.Decision = "ended: the call ended"
		case next != "":
			run.Decision = "to " + next
			c.note("the AGI script " + s.Name + " sent the call to " + next)
			c.goToNumber(cfg, next, s.Fallback)
		default:
			run.Decision = "fallback: " + s.Fallback.Type + " " + s.Fallback.Target
			if err != nil {
				c.e.Log.Warn("AGI script failed; the call goes to its fallback", "script", s.Name, "err", err)
				c.note("the AGI script " + s.Name + " failed (" + err.Error() + "); the call went to its fallback")
			}
			c.goTo(cfg, s.Fallback)
		}
		c.e.publish("agi.ran", run)
	}
	tg, err := agi.ParseTarget(s.Address)
	if err != nil {
		finish(err, "", 0)
		return
	}
	var rw io.ReadWriter
	var onHangup func()
	if tg.Program != "" {
		p, err := agi.StartProgram(c.e.AGI.Programs, tg.Program, s.Args, s.EAGI, func(line string) {
			c.e.Log.Info("AGI program wrote", "script", s.Name, "line", line)
		})
		if err != nil {
			finish(err, "", 0)
			return
		}
		defer p.Stop()
		rw, onHangup = p, p.Hangup
		env.Enhanced = p.EAGI()
		if p.EAGI() {
			ch.feedAudio(p.Audio)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*agiConnectTimeout)
		conn, err := agi.Dial(ctx, tg, agiConnectTimeout)
		cancel()
		if err != nil {
			finish(errors.New("could not reach "+s.Address+": "+err.Error()), "", 0)
			return
		}
		defer conn.Close()
		rw = conn
		env.Network, env.Script = true, tg.Script
	}
	limit := time.Duration(s.MaxMinutes) * time.Minute
	res := agi.Run(rw, ch, env, agi.Config{Library: c.e.AGI.Library, DB: c.e.AGI.DB, MaxTime: limit, OnHangup: onHangup})
	finish(res.Err, res.Next, res.Commands)
}

// goToNumber sends the call on to a number the way an extension's dialling would (an extension, a
// ring group, a menu, an outside number), or to fallback when nothing here takes that number.
func (c *Call) goToNumber(cfg *config.Config, number string, fallback config.Destination) {
	c.mu.Lock()
	caller := cfg.FindExtension(c.callerExt)
	outside := c.direction == "inbound"
	c.mu.Unlock()
	n := cleanNumber(number)
	if slices.Contains(cfg.System.EmergencyNumbers, n) {
		c.goTo(cfg, fallback) // a script never sends a call to an emergency number
		return
	}
	p := c.e.routeCall(cfg, caller, n, outside)
	c.note(p.why...)
	switch {
	case p.reject != 0:
		c.goTo(cfg, fallback)
	case p.local != "":
		c.mu.Lock()
		c.toName = p.toName
		c.mu.Unlock()
		c.answerLocal(p)
	default:
		if !c.ringAnswered(p) && !c.isEnded() {
			c.hangup("no answer", "system")
		}
	}
}

// agiChannel is a call as an AGI script runs it.
type agiChannel struct {
	c    *Call
	cfg  *config.Config
	name string
	keys *media.Capture
	gone chan struct{}
	once sync.Once

	mu       sync.Mutex
	handed   bool
	ending   string
	log      []string
	stopFeed func()
	music    *media.Player
	quit     chan struct{}
}

func newAGIChannel(c *Call, cfg *config.Config) *agiChannel {
	c.mu.Lock()
	name := "PJSIP/" + c.from + "-" + c.ID
	c.mu.Unlock()
	ch := &agiChannel{c: c, cfg: cfg, name: name, keys: media.NewCapture(1), gone: make(chan struct{}), quit: make(chan struct{})}
	c.caller.ep.Record(ch.keys.Packet)
	go func() {
		select {
		case <-c.cancelled:
			ch.markGone()
		case <-ch.quit:
		}
	}()
	return ch
}

func (h *agiChannel) markGone() { h.once.Do(func() { close(h.gone) }) }

// stop ends the channel's watcher (the session is over).
func (h *agiChannel) stop() {
	select {
	case <-h.quit:
	default:
		close(h.quit)
	}
}

// release lets go of the caller's audio once the script is done (unless the call was handed on).
func (h *agiChannel) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopFeed != nil {
		h.stopFeed()
		h.stopFeed = nil
	}
	if h.music != nil {
		h.music.Stop()
		h.music = nil
	}
	if !h.handed {
		h.c.caller.ep.Record(nil)
	}
}

func (h *agiChannel) handedOver() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handed
}

func (h *agiChannel) logLines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.log...)
}

// feedAudio passes the caller's audio to an EAGI program every 20 ms.
func (h *agiChannel) feedAudio(to func([]int16)) {
	capt := media.NewCapture(60)
	remove := h.c.caller.ep.Listen(capt.Packet)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				to(capt.Drain())
			case <-done:
				return
			}
		}
	}()
	h.mu.Lock()
	h.stopFeed = func() { remove(); close(done) }
	h.mu.Unlock()
}

func (h *agiChannel) Name() string            { return h.name }
func (h *agiChannel) Hungup() <-chan struct{} { return h.gone }
func (h *agiChannel) Answer() error           { return nil } // BitVoice answered before the script started
func (h *agiChannel) Up() bool                { return true }

func (h *agiChannel) isGone() bool {
	select {
	case <-h.gone:
		return true
	default:
		return false
	}
}

func (h *agiChannel) Play(pcm []int16, escape string) (byte, int, bool) {
	if h.isGone() {
		return 0, 0, true
	}
	h.stopMusic()
	if len(pcm) == 0 {
		return 0, 0, false
	}
	filtered := make(chan byte, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case k := <-h.keys.Keys:
				if strings.IndexByte(escape, k) >= 0 {
					filtered <- k
					return
				}
			case <-done:
				return
			}
		}
	}()
	started := time.Now()
	k, ended := h.c.play(pcm, filtered)
	close(done)
	played := min(len(pcm), int(time.Since(started).Seconds()*media.Rate))
	if k == 0 && !ended {
		played = len(pcm)
	}
	return k, played, ended
}

func (h *agiChannel) WaitKey(d time.Duration) (byte, bool) {
	var limit <-chan time.Time
	if d >= 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		limit = t.C
	}
	select {
	case k := <-h.keys.Keys:
		return k, false
	case <-limit:
		return 0, false
	case <-h.gone:
		return 0, true
	}
}

func (h *agiChannel) Record(max, silence time.Duration, escape string) ([]int16, byte, string) {
	h.stopMusic()
	secs := int(max/time.Second) + 1
	capt := media.NewCapture(secs)
	h.c.caller.ep.Record(capt.Packet)
	defer h.c.caller.ep.Record(h.keys.Packet)
	limit := time.NewTimer(max)
	defer limit.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	started := time.Now()
	for {
		select {
		case <-h.gone:
			return capt.Stop(), 0, "hangup"
		case k := <-capt.Keys:
			if strings.IndexByte(escape, k) >= 0 {
				return capt.Stop(), k, "dtmf"
			}
		case <-limit.C:
			return capt.Stop(), 0, "timeout"
		case <-tick.C:
			if capt.Full() {
				return capt.Stop(), 0, "timeout"
			}
			if silence > 0 && time.Since(started) > silence {
				secs := int(silence / time.Second)
				if secs < 1 {
					secs = 1
				}
				if quietFor(capt.Samples(), secs) {
					return capt.Stop(), 0, "timeout"
				}
			}
		}
	}
}

func (h *agiChannel) Sound(name string) ([]int16, bool) {
	pcm, err := h.c.e.soundFor(h.cfg, name)
	return pcm, err == nil
}

func (h *agiChannel) Speak(text string) ([]int16, bool) {
	a := h.cfg.System.AI
	if !a.Enabled || a.SpeechModel == "" || strings.TrimSpace(text) == "" || h.c.e.Speech == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wav, err := h.c.e.Speech.Client(a, h.c.e.AIAllow).Speak(ctx, a.SpeechModel, a.Voice, text)
	if err != nil {
		h.Log("speech: " + err.Error())
		return nil, false
	}
	pcm, err := media.DecodeWAV(wav)
	return pcm, err == nil
}

// canTranscribe says whether Settings, AI has a speech to text to use.
func canTranscribe(a config.AI) bool {
	return a.Enabled && (a.LocalSpeech || a.TranscribeURL != "" || a.AudioModel != "")
}

func (h *agiChannel) Transcribe(pcm []int16) (string, bool) {
	a := h.cfg.System.AI
	if !canTranscribe(a) || h.c.e.Speech == nil {
		return "", false
	}
	if pcm == nil {
		return "", true // only asking whether there is one
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	text, err := h.c.e.Speech.Client(a, h.c.e.AIAllow).Transcribe(ctx, media.EncodeWAV(pcm))
	if err != nil {
		h.Log("speech to text: " + err.Error())
		return "", true
	}
	return text, true
}

func (h *agiChannel) stopMusic() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.music != nil {
		h.music.Stop()
		h.music = nil
	}
}

func (h *agiChannel) Music() func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.music == nil {
		h.music = h.c.e.startHold(h.cfg, h.c.caller, holdClass(h.cfg), media.ToneHold)
	}
	return h.stopMusic
}

func (h *agiChannel) SetCallerID(number, name string) {
	h.c.mu.Lock()
	if number != "" {
		h.c.from = number
	}
	if name != "" {
		h.c.fromName = name
	}
	h.c.mu.Unlock()
	h.c.e.publish("call.updated", h.c.info())
}

// handOver marks the call as no longer the script's.
func (h *agiChannel) handOver(ending string) {
	h.mu.Lock()
	h.handed, h.ending = true, ending
	h.mu.Unlock()
	h.stopMusic()
	h.c.caller.ep.Record(nil)
	h.markGone()
}

func (h *agiChannel) Dial(number string, ring time.Duration) (string, bool) {
	if h.isGone() {
		return "CANCEL", false
	}
	host := &callHost{c: h.c, cfg: h.cfg}
	n := cleanNumber(number)
	d := config.Destination{Type: "number", Target: n}
	switch {
	case h.cfg.FindExtension(n) != nil:
		d.Type = "extension"
	case h.cfg.FindRingGroup(n) != nil:
		d.Type = "ringgroup"
	case h.cfg.FindQueue(n) != nil:
		d.Type = "queue"
	}
	p, err := host.planFor(d)
	if err != nil {
		h.Log("Dial " + number + ": " + err.Error())
		return "CHANUNAVAIL", false
	}
	if p.reject != 0 {
		return "CHANUNAVAIL", false
	}
	if p.local != "" { // voicemail, a queue, a menu: BitVoice takes the call itself
		h.handOver("dialled " + number)
		h.c.goTo(h.cfg, d)
		return "ANSWER", true
	}
	for i := range p.stages {
		if p.stages[i].timeout <= 0 || p.stages[i].timeout > ring {
			p.stages[i].timeout = ring
		}
	}
	h.stopMusic()
	h.c.caller.ep.Record(nil)
	if h.c.ringAnswered(p) {
		h.handOver("answered at " + number)
		return "ANSWER", true
	}
	h.c.caller.ep.Record(h.keys.Packet)
	if h.isGone() {
		return "CANCEL", false
	}
	return "NOANSWER", false
}

func (h *agiChannel) HandOver(kind, target string) error {
	var d config.Destination
	switch kind {
	case "voicemail":
		x := h.cfg.FindExtension(target)
		if x == nil || !x.Voicemail.Enabled {
			return errors.New("there is no mailbox " + target)
		}
		d = config.Destination{Type: "voicemail", Target: target}
	case "queue":
		if h.cfg.FindQueue(target) == nil {
			return errors.New("there is no queue " + target)
		}
		d = config.Destination{Type: "queue", Target: target}
	default:
		return errors.New("cannot hand a call to " + kind)
	}
	h.handOver("handed to " + kind + " " + target)
	h.c.goTo(h.cfg, d)
	return nil
}

func (h *agiChannel) Hangup() {
	h.mu.Lock()
	h.ending = "the script hung up"
	h.mu.Unlock()
	h.c.hangup("hung up by AGI script", "system")
}

func (h *agiChannel) Log(msg string) {
	h.mu.Lock()
	if len(h.log) < 200 {
		h.log = append(h.log, msg)
	}
	h.mu.Unlock()
}

// agiPruneEvery is how often old recordings of AGI scripts are removed.
var agiPruneEvery = 6 * time.Hour

// PruneAGI removes the AGI scripts' recordings older than the days kept, now and then, until stop
// is closed.
func (e *Engine) PruneAGI(stop <-chan struct{}) {
	if e.AGI == nil || e.AGI.Library == nil {
		return
	}
	t := time.NewTicker(agiPruneEvery)
	defer t.Stop()
	for {
		e.AGI.Library.KeepDays = e.Config().System.Compat.AGIKeepDays
		if n := e.AGI.Library.Prune(time.Now()); n > 0 {
			e.Log.Info("removed old recordings of AGI scripts", "count", strconv.Itoa(n))
		}
		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
}
