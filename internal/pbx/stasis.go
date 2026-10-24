package pbx

import (
	"errors"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// StasisHub is where calls sent to ARI applications go: the compat package's ARI, which hands them to
// the program connected for the application.
type StasisHub interface {
	// Enter hands a channel to an application; false when no program is connected for it.
	Enter(app string, ch *AppChannel, args []string) bool
}

// AppChannel is a call while an ARI application runs it: the caller's side, answered by BitVoice.
// The application plays to it, records it, hears its keys, bridges it with other channels and hangs
// it up; or lets go of it (continue), and the call goes on to a destination or the application's
// fallback. A program that disconnects lets go of its calls the same way.
type AppChannel struct {
	c    *Call
	app  string
	args []string

	mu     sync.Mutex
	onKey  func(digit byte)
	keys   *media.Capture // the capture seeing the caller's audio now (keys, or a recording)
	leave  chan *config.Destination
	left   bool
	gone   chan struct{}
	bridge *media.Bridge
	vars   map[string]string
	// muteOut plays silence instead of what is played to the caller; muteIn keeps what the caller
	// says out of recordings (as silence) and out of bridges.
	muteIn, muteOut bool
	music           *media.Player // hold music while held (POST .../hold or .../moh)
	app2            string        // the application the channel was moved to, if any
}

// Mute mutes the caller's side: in (what they say), out (what they hear), or both; on false unmutes.
func (a *AppChannel) Mute(in, out, on bool) {
	a.mu.Lock()
	if in {
		a.muteIn = on
	}
	if out {
		a.muteOut = on
	}
	capt := a.keys
	mi := a.muteIn
	a.mu.Unlock()
	if capt != nil {
		capt.Mute(mi)
	}
}

// Muted reports the channel's mutes.
func (a *AppChannel) Muted() (in, out bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.muteIn, a.muteOut
}

// PauseRecording pauses (or resumes) the recording going on; MuteRecording records silence (or sound again).
func (a *AppChannel) PauseRecording(on bool) {
	a.mu.Lock()
	capt := a.keys
	a.mu.Unlock()
	if capt != nil {
		capt.Pause(on)
	}
}

func (a *AppChannel) MuteRecording(on bool) {
	a.mu.Lock()
	capt := a.keys
	a.mu.Unlock()
	if capt != nil {
		capt.Mute(on)
	}
}

// ErrBusy is media asked for on a channel already playing hold music.
var ErrBusy = errors.New("the channel is on hold: take it off hold first")

// Music plays hold music (on true) to the caller, or stops it; a channel in a bridge takes none.
func (a *AppChannel) Music(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bridge != nil {
		return ErrInBridge
	}
	if !on {
		if a.music != nil {
			a.music.Stop()
			a.music = nil
		}
		return nil
	}
	if a.music == nil {
		cfg := a.c.e.Config()
		a.music = a.c.e.startHold(cfg, a.c.caller, holdClass(cfg), media.ToneHold)
	}
	return nil
}

// Silence stops whatever plays to the caller (hold music included): they hear silence.
func (a *AppChannel) Silence() {
	a.mu.Lock()
	if a.music != nil {
		a.music.Stop()
		a.music = nil
	}
	a.mu.Unlock()
	a.c.mu.Lock()
	if a.c.player != nil {
		a.c.player.Stop()
	}
	a.c.mu.Unlock()
}

// RTPStats are the caller's media counters: what BitVoice sent and received.
func (a *AppChannel) RTPStats() (rx media.Stats, sent media.Stats) {
	rx = a.c.caller.ep.Stats()
	return rx, a.c.caller.ep.SentStats()
}

// Tap passes what the caller says (8 kHz) to f until the function returned is called.
func (a *AppChannel) Tap(f func(pcm []int16)) (stop func()) {
	capt := media.NewCapture(3600)
	remove := a.c.caller.ep.Listen(capt.Packet)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if pcm := capt.Drain(); len(pcm) > 0 {
					f(pcm)
				}
			case <-done:
				return
			case <-a.gone:
				return
			}
		}
	}()
	return sync.OnceFunc(func() { remove(); close(done) })
}

// Move records that the channel now belongs to another application (ARI's move).
func (a *AppChannel) Move(app string) {
	a.mu.Lock()
	a.app2 = app
	a.mu.Unlock()
}

// ID is the channel's ARI id: the call's id with -1, as for every caller's channel.
func (a *AppChannel) ID() string { return a.c.ID + "-1" }

// CallID is the BitVoice call the channel is.
func (a *AppChannel) CallID() string { return a.c.ID }

// App is the application the channel is in; Args what the call was sent there with.
func (a *AppChannel) App() string    { return a.app }
func (a *AppChannel) Args() []string { return a.args }

// Info is the call as the engine reports it.
func (a *AppChannel) Info() CallInfo { return a.c.info() }

// Gone is closed when the channel leaves the application: hung up, or let go.
func (a *AppChannel) Gone() <-chan struct{} { return a.gone }

// Ended says whether the call itself is over.
func (a *AppChannel) Ended() bool {
	select {
	case <-a.c.cancelled:
		return true
	default:
		return false
	}
}

// OnKey sets what hears the caller's keys (ChannelDtmfReceived).
func (a *AppChannel) OnKey(f func(digit byte)) {
	a.mu.Lock()
	a.onKey = f
	a.mu.Unlock()
}

// listen puts a capture on the caller's audio and passes its keys on; the capture before it stops.
func (a *AppChannel) listen(capt *media.Capture) {
	a.mu.Lock()
	a.keys = capt
	a.mu.Unlock()
	a.c.caller.ep.Record(capt.Packet)
	go func() {
		for {
			select {
			case k := <-capt.Keys:
				a.mu.Lock()
				f, cur := a.onKey, a.keys
				a.mu.Unlock()
				if f != nil {
					f(k)
				}
				if cur != capt {
					return
				}
			case <-a.gone:
				return
			}
		}
	}()
}

// Var and SetVar are the channel's variables (ARI's GET and POST .../variable).
func (a *AppChannel) Var(name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.vars[name]
	return v, ok
}

func (a *AppChannel) SetVar(name, value string) {
	a.mu.Lock()
	if a.vars == nil {
		a.vars = map[string]string{}
	}
	a.vars[name] = value
	a.mu.Unlock()
}

// Sound is what a sound name plays (see soundFor).
func (a *AppChannel) Sound(name string) ([]int16, error) { return a.c.e.soundFor(a.c.e.Config(), name) }

// ErrInBridge is returned for media on a channel in a bridge: the bridge's media goes to the bridge.
var ErrInBridge = errors.New("the channel is in a bridge: play to the bridge, or remove it first")

// Play plays audio to the caller until it ends, stop is closed, or the call ends. It reports whether
// it was stopped, and whether the call ended.
func (a *AppChannel) Play(pcm []int16, stop <-chan struct{}) (stopped, ended bool, err error) {
	a.mu.Lock()
	inBridge := a.bridge != nil
	a.mu.Unlock()
	if inBridge {
		return false, false, ErrInBridge
	}
	interrupt := make(chan byte, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			interrupt <- 1
		case <-done:
		}
	}()
	a.mu.Lock()
	if a.muteOut {
		pcm = make([]int16, len(pcm))
	}
	if a.music != nil {
		a.music.Stop()
		a.music = nil
	}
	a.mu.Unlock()
	k, ended := a.c.play(pcm, interrupt)
	return k != 0, ended, nil
}

// RecordResult is how a recording ended.
type RecordResult struct {
	PCM    []int16
	Reason string // "time", "silence", "key", "stopped", "hangup"
}

// Record records the caller until maxSeconds, maxSilence seconds of quiet, a key in terminateOn ("#",
// "*", "any"; "" for none), stop being closed, or the call ending. Keys still reach OnKey.
func (a *AppChannel) Record(maxSeconds, maxSilence int, beep bool, terminateOn string, stop <-chan struct{}) (RecordResult, error) {
	a.mu.Lock()
	inBridge := a.bridge != nil
	a.mu.Unlock()
	if inBridge {
		return RecordResult{}, ErrInBridge
	}
	if maxSeconds <= 0 || maxSeconds > 3600 {
		maxSeconds = 3600
	}
	if beep {
		if _, ended := a.c.play(media.ToneSamples(media.ToneBeep), nil); ended {
			return RecordResult{Reason: "hangup"}, nil
		}
	}
	capt := media.NewCapture(maxSeconds)
	a.mu.Lock()
	capt.Mute(a.muteIn)
	a.mu.Unlock()
	terminated := make(chan struct{}, 1)
	a.mu.Lock()
	prev := a.onKey
	a.onKey = func(k byte) {
		if prev != nil {
			prev(k)
		}
		if terminateOn == "any" || terminateOn != "" && terminateOn == string(k) {
			select {
			case terminated <- struct{}{}:
			default:
			}
		}
	}
	a.mu.Unlock()
	a.listen(capt)
	defer func() {
		a.mu.Lock()
		a.onKey = prev
		a.mu.Unlock()
		a.listen(media.NewCapture(1))
	}()
	limit := time.NewTimer(time.Duration(maxSeconds) * time.Second)
	defer limit.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	reason := ""
	started := time.Now()
	for reason == "" {
		select {
		case <-a.c.cancelled:
			reason = "hangup"
		case <-stop:
			reason = "stopped"
		case <-terminated:
			reason = "key"
		case <-limit.C:
			reason = "time"
		case <-tick.C:
			if capt.Full() {
				reason = "time"
			} else if maxSilence > 0 && time.Since(started) > time.Duration(maxSilence)*time.Second && quietFor(capt.Samples(), maxSilence) {
				reason = "silence"
			}
		}
	}
	return RecordResult{PCM: capt.Stop(), Reason: reason}, nil
}

// quietFor says whether the last seconds of audio were all quiet.
func quietFor(pcm []int16, seconds int) bool {
	n := seconds * media.Rate
	if len(pcm) < n {
		return false
	}
	for _, s := range pcm[len(pcm)-n:] {
		if s > 600 || s < -600 {
			return false
		}
	}
	return true
}

// JoinBridge puts the channel's audio in a mixing bridge; LeaveBridge takes it out.
func (a *AppChannel) JoinBridge(b *media.Bridge) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.left || a.Ended() {
		return errors.New("the channel is gone")
	}
	if a.bridge != nil {
		return errors.New("the channel is already in a bridge")
	}
	a.c.mu.Lock()
	if a.c.player != nil {
		a.c.player.Stop()
	}
	a.c.mu.Unlock()
	if a.music != nil {
		a.music.Stop()
		a.music = nil
	}
	a.bridge = b
	b.Add(a.ID(), a.c.caller.ep)
	return nil
}

func (a *AppChannel) LeaveBridge() {
	a.mu.Lock()
	b := a.bridge
	a.bridge = nil
	a.mu.Unlock()
	if b != nil {
		b.Remove(a.ID())
	}
}

// Hangup ends the call.
func (a *AppChannel) Hangup(why string) { a.c.hangup(why, "system") }

// Continue lets go of the channel: the call goes to d, or to the application's fallback when d is nil.
func (a *AppChannel) Continue(d *config.Destination) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.left {
		return errors.New("the channel has already left the application")
	}
	a.left = true
	a.leave <- d
	return nil
}

// runStasis hands an answered call to an ARI application, and waits until it hangs up or lets go.
func (c *Call) runStasis(name string, args []string) {
	cfg := c.e.Config()
	fallback := config.Destination{Type: "hangup"}
	if app := cfg.FindARIApp(name); app != nil {
		fallback = app.Fallback
	}
	a := &AppChannel{c: c, app: name, args: args, leave: make(chan *config.Destination, 1), gone: make(chan struct{})}
	a.listen(media.NewCapture(1))
	c.mu.Lock()
	c.answeredBy = "ARI " + name
	c.mu.Unlock()
	c.e.publish("call.updated", c.info())
	if c.e.Stasis == nil || !c.e.Stasis.Enter(name, a, args) {
		close(a.gone)
		c.caller.ep.Record(nil)
		c.e.Log.Info("no program is connected for the ARI application; the call goes to its fallback", "app", name, "call", c.ID)
		c.goTo(cfg, fallback)
		return
	}
	var next *config.Destination
	select {
	case <-c.cancelled:
	case d := <-a.leave:
		next = d
		if next == nil {
			next = &fallback
		}
	}
	a.LeaveBridge()
	_ = a.Music(false)
	c.caller.ep.Record(nil)
	close(a.gone)
	if next != nil {
		c.goTo(c.e.Config(), *next)
	}
}

// CallIntoApp rings an extension's phones and, when one answers, hands that call to an ARI
// application (ARI's POST /channels with app). It returns the channel's id at once; the application
// hears StasisStart when it is answered. ring bounds the ringing.
func (e *Engine) CallIntoApp(ext, app string, args []string, callerName string, ring time.Duration) (string, error) {
	return e.CallIntoAppAs("", ext, app, args, callerName, ring, nil)
}

// CallIntoAppAs is CallIntoApp with the call's id chosen by the caller ("" for a new one; the channel
// is the id with -1), and onStatus told how the ringing went: RINGING, then ANSWER or NOANSWER.
func (e *Engine) CallIntoAppAs(id, ext, app string, args []string, callerName string, ring time.Duration, onStatus func(string)) (string, error) {
	if onStatus == nil {
		onStatus = func(string) {}
	}
	cfg := e.Config()
	x := cfg.FindExtension(ext)
	if x == nil {
		return "", errors.New("no extension " + ext)
	}
	if e.Stasis == nil {
		return "", errors.New("ARI applications are not available")
	}
	targets := e.phoneTargets(cfg, x)
	if len(targets) == 0 {
		return "", errors.New("extension " + ext + " has no phone signed in")
	}
	if callerName == "" {
		callerName = app
	}
	if id == "" {
		id = config.NewID()
	}
	go func() {
		h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: cfg.System.Name, fromName: callerName,
			answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
			xfer: make(chan *Leg, 1)}
		onStatus("RINGING")
		go h.run(plan{stages: []stage{{targets: targets, timeout: ring}}, direction: "internal"})
		leg := <-h.xfer
		if leg == nil {
			onStatus("NOANSWER")
			return
		}
		onStatus("ANSWER")
		c := &Call{e: e, ID: id, start: time.Now(), answered: make(chan struct{}),
			cancelled: make(chan struct{}), results: make(chan forkResult, 64),
			from: cfg.System.Name, fromName: callerName, callerExt: x.Number, to: x.Number, toName: x.Name, direction: "internal"}
		leg.call = c
		leg.Number, leg.Name, leg.Ext = x.Number, x.Name, x.Number
		c.caller = leg
		c.localAnswer = true
		c.answer = time.Now()
		e.mu.Lock()
		e.calls[c.ID] = c
		e.mu.Unlock()
		e.publish("call.started", c.info())
		c.runStasis(app, args)
	}()
	return id + "-1", nil
}
