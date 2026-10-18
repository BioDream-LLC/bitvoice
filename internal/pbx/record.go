package pbx

import (
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Recordings stores call recordings. *store.Store implements it.
type Recordings interface {
	AddRecording(callID string, at time.Time, wav []byte) (string, error)
}

// maxRecordSeconds bounds one recording (four hours) so a stuck call cannot fill the disk.
const maxRecordSeconds = 4 * 3600

type recording struct {
	caller, callee *media.Track
	start          time.Time
	pausedAt       time.Time    // zero while recording
	spans          []store.Span // paused stretches, for the call record
}

// shouldRecord applies the recording setting to a call between two parties (extension numbers or "").
func shouldRecord(cfg *config.Config, exts ...string) bool {
	switch cfg.System.Recording.Mode {
	case "all":
		return true
	case "selected":
		for _, n := range exts {
			if x := cfg.FindExtension(n); x != nil && x.Record {
				return true
			}
		}
	}
	return false
}

// startRecording records both parties from the moment of answer, caller on the left channel. Called with
// c.mu held.
func (c *Call) startRecording(callee *Leg) {
	cfg := c.e.Config()
	if c.e.Recordings == nil || c.rec != nil || c.direction == "cluster" || !shouldRecord(cfg, c.callerExt, callee.Ext) {
		return // a call another server sent is recorded there
	}
	// Every party's rule counts, ours too: an office under an all-party law announces whoever it calls.
	ours := c.presented
	if c.direction == "inbound" {
		ours = c.to
	}
	action, applied := cfg.System.Recording.ConsentForParties(c.outsideNumber(callee), ours)
	if len(applied) > 1 && len(c.why) < whyMax { // c.mu is held
		c.why = append(c.why, "Recorded under the most protective rule ("+action+") of "+strings.Join(applied, " and ")+".")
	}
	switch action {
	case "never":
		return
	case "announce":
		c.notice, c.noticeLeg = "announce", callee // bridged plays it, then starts recording
		return
	case "beep":
		c.notice = "beep"
	}
	c.beginRecording(callee)
}

// outsideNumber is the number on the far side of a call with the outside world, or "".
func (c *Call) outsideNumber(callee *Leg) string {
	if c.direction == "inbound" {
		return c.from
	}
	if callee != nil && callee.TrunkID != "" {
		return callee.Number
	}
	return ""
}

// noticeBox is where the recorded "this call is recorded" notice is kept.
const noticeBox = "recording-notice"

// consentNotice runs after a recorded call is bridged: the notice before recording starts, or the
// beep while it records.
func (c *Call) consentNotice() {
	c.mu.Lock()
	n, l := c.notice, c.noticeLeg
	c.notice, c.noticeLeg = "", nil
	c.mu.Unlock()
	switch n {
	case "announce":
		go c.announceThenRecord(l)
	case "beep":
		go c.beepWhileRecording()
	}
}

// noticeAudio is the recorded notice, or three beeps when none was recorded.
func (c *Call) noticeAudio() []int16 {
	if c.e.Mail != nil {
		if b, err := c.e.Mail.Greeting(noticeBox); err == nil {
			if pcm, err := media.DecodeWAV(b); err == nil && len(pcm) > 0 {
				return pcm
			}
		}
	}
	return media.ToneSamples(media.Tone{Freqs: []float64{1400}, Level: 0.25, On: 200 * time.Millisecond, Off: 300 * time.Millisecond, Repeat: 3})
}

// toBoth plays audio to both parties at once, with their audio to each other cut meanwhile. It
// reports false when the call ended or changed (a transfer) while it played.
func (c *Call) toBoth(l *Leg, pcm []int16) bool {
	c.mu.Lock()
	a := c.caller
	if c.ended || c.callee != l || a == nil || l == nil {
		c.mu.Unlock()
		return false
	}
	c.mu.Unlock()
	if a.held() || l.held() {
		return false
	}
	connect(a, nil)
	connect(l, nil)
	pt := func(x *Leg) int {
		if strings.EqualFold(x.codecName(), "PCMA") {
			return 8
		}
		return 0
	}
	pa, pl := media.NewPlayer(a.ep, pt(a)), media.NewPlayer(l.ep, pt(l))
	pa.PlayPCM(pcm)
	pl.PlayPCM(pcm)
	select {
	case <-pa.Done():
	case <-c.cancelled:
	}
	<-pl.Done()
	pa.Stop()
	pl.Stop()
	c.mu.Lock()
	ok := !c.ended && c.callee == l
	c.mu.Unlock()
	if ok {
		connect(a, l)
	}
	return ok
}

func (c *Call) announceThenRecord(l *Leg) {
	if !c.toBoth(l, c.noticeAudio()) {
		return
	}
	c.mu.Lock()
	if !c.ended && c.callee == l && c.rec == nil {
		c.beginRecording(l)
	}
	c.mu.Unlock()
	c.e.publish("call.updated", c.info())
}

// beepWhileRecording sounds a short tone to both sides every 15 seconds while the call records.
func (c *Call) beepWhileRecording() {
	beep := media.ToneSamples(media.Tone{Freqs: []float64{1400}, Level: 0.2, On: 200 * time.Millisecond, Off: 0, Repeat: 1})
	for first := true; ; first = false {
		if !first {
			select {
			case <-time.After(15 * time.Second):
			case <-c.cancelled:
				return
			}
		}
		c.mu.Lock()
		l, recording := c.callee, c.rec != nil
		c.mu.Unlock()
		if !recording {
			return
		}
		c.toBoth(l, beep)
	}
}

// beginRecording records both parties from now, caller on the left channel. Called with c.mu held.
func (c *Call) beginRecording(callee *Leg) {
	now := time.Now()
	r := &recording{start: now, caller: media.NewTrack(now, maxRecordSeconds), callee: media.NewTrack(now, maxRecordSeconds)}
	c.rec = r
	c.caller.ep.Record(r.caller.Packet)
	callee.ep.Record(r.callee.Packet)
}

// saveRecording writes the recording, if any, and returns its name for the call record.
func (c *Call) saveRecording() string {
	c.mu.Lock()
	r := c.rec
	c.rec = nil
	if r != nil {
		c.recPaused = r.pausedSpans(time.Now())
	}
	c.mu.Unlock()
	if r == nil {
		return ""
	}
	left, right := r.caller.Samples(), r.callee.Samples()
	if len(left) == 0 && len(right) == 0 {
		return ""
	}
	name, err := c.e.Recordings.AddRecording(c.ID, r.start, media.EncodeStereoWAV(left, right))
	if err != nil {
		c.e.Log.Error("could not save call recording", "call", c.ID, "err", err)
		return ""
	}
	return name
}

// PauseRecording pauses or resumes a call's recording (while a card number is given, for PCI DSS).
// It reports false when the call is not being recorded.
func (e *Engine) PauseRecording(id string, pause bool, by string) bool {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	return c != nil && c.pauseRecording(pause, by)
}

func (c *Call) pauseRecording(pause bool, by string) bool {
	c.mu.Lock()
	r := c.rec
	if r == nil {
		c.mu.Unlock()
		return false
	}
	now := time.Now()
	changed := pause != !r.pausedAt.IsZero()
	if changed {
		if pause {
			r.pausedAt = now
		} else {
			r.spans = append(r.spans, store.Span{From: int(r.pausedAt.Sub(r.start).Seconds()), To: int(now.Sub(r.start).Seconds())})
			r.pausedAt = time.Time{}
		}
		r.caller.Pause(pause)
		r.callee.Pause(pause)
	}
	c.mu.Unlock()
	if changed {
		c.e.Log.Info("recording "+map[bool]string{true: "paused", false: "resumed"}[pause], "call", c.ID, "by", by)
		c.e.publish("call.updated", c.info())
	}
	return true
}

// recordingState is "", recording or paused, for the screen.
func (c *Call) recordingState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.rec == nil:
		return ""
	case !c.rec.pausedAt.IsZero():
		return "paused"
	}
	return "recording"
}

// pausedSpans closes an open pause and returns the paused stretches.
func (r *recording) pausedSpans(end time.Time) []store.Span {
	if r == nil {
		return nil
	}
	out := r.spans
	if !r.pausedAt.IsZero() {
		out = append(out, store.Span{From: int(r.pausedAt.Sub(r.start).Seconds()), To: int(end.Sub(r.start).Seconds())})
	}
	return out
}

func (l *Leg) held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.onHold
}
