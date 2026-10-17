package pbx

import (
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Mailbox stores voicemail. *store.Store implements it.
type Mailbox interface {
	AddVoicemail(v store.Voicemail, wav []byte) (store.Voicemail, error)
	Voicemails(box string) ([]store.Voicemail, error)
	VoicemailAudio(box, id string) ([]byte, error)
	VoicemailCounts(box string) (fresh, old int)
	SetHeard(box, id string, heard bool) (store.Voicemail, error)
	DeleteVoicemail(box, id string) error
	Greeting(box string) ([]byte, error)
}

// Tones for the voicemail menu. BitVoice ships no recorded speech yet, so the menus speak in tones; a
// mailbox's own recorded greeting is played to callers.
var (
	toneNoMessages = media.Tone{Freqs: []float64{480, 620}, Level: 0.2, On: 250 * time.Millisecond, Off: 250 * time.Millisecond, Repeat: 2}
	toneNext       = media.Tone{Freqs: []float64{1400}, Level: 0.2, On: 120 * time.Millisecond, Off: 380 * time.Millisecond, Repeat: 1}
	toneDeleted    = media.Tone{Freqs: []float64{600}, Level: 0.2, On: 200 * time.Millisecond, Off: 100 * time.Millisecond, Repeat: 2}
)

// play plays audio to the caller and waits until it ends, the caller presses a key (returned), or the
// call ends (returns 0 with ended true).
func (c *Call) play(pcm []int16, keys <-chan byte) (key byte, ended bool) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return 0, true
	}
	a := c.caller
	pt := 0
	if strings.EqualFold(a.codecName(), "PCMA") {
		pt = 8
	}
	if c.player != nil {
		c.player.Stop()
	}
	pl := media.NewPlayer(a.ep, pt)
	c.player = pl
	c.mu.Unlock()
	pl.PlayPCM(pcm)
	defer pl.Stop()
	select {
	case <-pl.Done():
		return 0, false
	case k := <-keys:
		return k, false
	case <-c.cancelled:
		return 0, true
	}
}

// leaveMessage plays the mailbox greeting and a beep, then records until the caller hangs up, presses #,
// or reaches the mailbox's limit.
func (c *Call) leaveMessage(box string) {
	cfg := c.e.Config()
	x := cfg.FindExtension(box)
	if x == nil || c.e.Mail == nil {
		c.hangup("no mailbox", "system")
		return
	}
	capt := media.NewCapture(x.Voicemail.MaxSeconds)
	c.caller.ep.Record(capt.Packet)
	defer c.caller.ep.Record(nil)

	greeting := []int16(nil)
	if b, err := c.e.Mail.Greeting(box); err == nil {
		greeting, _ = media.DecodeWAV(b)
	}
	if len(greeting) == 0 {
		greeting = make([]int16, media.Rate/2) // half a second of quiet before the beep
	}
	if k, ended := c.play(greeting, capt.Keys); ended {
		capt.Stop()
		return
	} else if k == '*' && c.callerExt == box {
		// The owner calling their own mailbox (say from the no-answer path) can go to their messages.
		capt.Stop()
		c.collectMessages(box)
		return
	}
	if _, ended := c.play(media.ToneSamples(media.ToneBeep), nil); ended {
		capt.Stop()
		return
	}
	capt.Stop() // drop what was said over the greeting
	capt = media.NewCapture(x.Voicemail.MaxSeconds)
	c.caller.ep.Record(capt.Packet)
	started := time.Now()

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	hangup := false
wait:
	for {
		select {
		case <-c.cancelled:
			break wait
		case k := <-capt.Keys:
			if k == '#' {
				hangup = true
				break wait
			}
		case <-tick.C:
			if capt.Full() {
				hangup = true
				break wait
			}
		}
	}
	pcm := media.TrimSilence(capt.Stop())
	c.saveMessage(box, pcm, started)
	if hangup {
		c.play(media.ToneSamples(media.ToneConfirm), nil)
		c.hangup("message left", "system")
	}
}

func (c *Call) saveMessage(box string, pcm []int16, started time.Time) {
	if len(pcm) < media.Rate { // under a second: a hang-up at the beep, not a message
		return
	}
	c.mu.Lock()
	v := store.Voicemail{Box: box, From: c.from, FromName: c.fromName, At: started.UTC(),
		Seconds: (len(pcm) + media.Rate/2) / media.Rate, CallID: c.ID}
	c.mu.Unlock()
	if c.e.Config().System.Audio.Voicemail {
		pcm = media.Clean(pcm, media.Rate)
	}
	v, err := c.e.Mail.AddVoicemail(v, media.EncodeWAV(pcm))
	if err != nil {
		c.e.Log.Error("could not save voicemail", "box", box, "err", err)
		return
	}
	c.e.Log.Info("voicemail left", "box", box, "from", v.From, "seconds", v.Seconds)
	c.e.publish("voicemail.new", v)
	c.e.MailboxChanged(box)
}

// collectMessages plays a mailbox's messages, new ones first, oldest first. While a message plays: 7
// deletes it, # or 6 skips to the next, 4 plays it again, * hangs up. Each message starts with a short
// tone; a message heard to the end is marked heard.
func (c *Call) collectMessages(box string) {
	if c.e.Mail == nil {
		c.hangup("no mailbox", "system")
		return
	}
	keys := media.NewCapture(1)
	c.caller.ep.Record(keys.Packet)
	defer c.caller.ep.Record(nil)

	all, _ := c.e.Mail.Voicemails(box)
	var fresh, old []store.Voicemail
	for i := len(all) - 1; i >= 0; i-- { // the list is newest first; play oldest first
		if all[i].Heard {
			old = append(old, all[i])
		} else {
			fresh = append(fresh, all[i])
		}
	}
	queue := append(fresh, old...)
	if len(queue) == 0 {
		c.play(make([]int16, media.Rate/2), nil)
		c.play(media.ToneSamples(toneNoMessages), nil)
		c.hangup("no messages", "system")
		return
	}
	changed := false
	defer func() {
		if changed {
			c.e.MailboxChanged(box)
		}
	}()
	c.play(make([]int16, media.Rate/2), nil)
	for i := 0; i < len(queue); i++ {
		v := queue[i]
		wav, err := c.e.Mail.VoicemailAudio(box, v.ID)
		if err != nil {
			continue
		}
		pcm, _ := media.DecodeWAV(wav)
		audio := append(media.ToneSamples(toneNext), pcm...)
		k, ended := c.play(audio, keys.Keys)
		if ended {
			return
		}
		switch k {
		case 0: // heard to the end
			if !v.Heard {
				c.e.Mail.SetHeard(box, v.ID, true)
				changed = true
			}
		case '7':
			c.e.Mail.DeleteVoicemail(box, v.ID)
			changed = true
			c.e.publish("voicemail.deleted", v)
			if _, ended := c.play(media.ToneSamples(toneDeleted), nil); ended {
				return
			}
		case '4':
			i-- // again
		case '*':
			c.hangup("caller left voicemail", "caller")
			return
		}
	}
	c.play(media.ToneSamples(media.ToneConfirm), nil)
	c.hangup("all messages played", "system")
}

// MailboxChanged tells the mailbox's phones (message-waiting light) and screens that its messages changed.
// MailboxCounts is a mailbox's new and old messages (AMI's MailboxCount); ok is false when there is
// no such mailbox.
func (e *Engine) MailboxCounts(box string) (fresh, old int, ok bool) {
	x := e.Config().FindExtension(box)
	if x == nil || !x.Voicemail.Enabled || e.Mail == nil {
		return 0, 0, false
	}
	fresh, old = e.Mail.VoicemailCounts(box)
	return fresh, old, true
}

func (e *Engine) MailboxChanged(box string) {
	if e.Mail == nil {
		return
	}
	fresh, old := e.Mail.VoicemailCounts(box)
	e.publish("voicemail.changed", map[string]any{"box": box, "new": fresh, "old": old})
	e.notifyMWI(box, fresh, old)
}
