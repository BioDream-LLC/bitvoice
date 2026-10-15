package pbx

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The outbound dialer calls a campaign's contacts for a queue's agents. Preview: the agent asks for
// the next contact and calls it. Progressive: one call per free agent. Power: Ratio calls per free
// agent. Predictive: the ratio is moved up and down to keep the share of answered calls left without
// an agent (abandoned) under the campaign's limit; over it, the campaign falls back to one call per
// agent. Contacts on the do-not-call list are never called; calls are made only within the hours; an
// answering machine is told apart from a person by how it speaks; a person who answers when no agent
// is free hears the campaign's message (who is calling, and 9 to be put on the do-not-call list).

// DialerStore keeps the contacts and the do-not-call list. *store.Store implements it.
type DialerStore interface {
	DueDialContacts(campaign string, now time.Time, n int) ([]store.DialContact, error)
	PutDialContact(c store.DialContact) error
	DialContactByID(campaign, id string) (store.DialContact, error)
	OnDNC(number string) bool
	AddDNC(reason string, numbers ...string) error
	ResetDialling() error
}

type dialerState struct {
	inflight int     // calls ringing, not yet answered or failed
	ratio    float64 // predictive: calls per free agent now
	recent   []bool  // the last answers by a person: true when it was abandoned
	machines int
}

type dialers struct {
	mu       sync.Mutex
	m        map[string]*dialerState
	reserved map[string]time.Time // agents a person was just handed to, until the queue rings them
}

const dialerWindow = 100 // answers the abandon rate is measured over

// DialerStatus is a campaign's live numbers.
type DialerStatus struct {
	Campaign    string  `json:"campaign"`
	Ringing     int     `json:"ringing"`
	Ratio       float64 `json:"ratio"`
	AbandonRate float64 `json:"abandonRate"` // percent of the last answers by a person
	Open        bool    `json:"open"`        // within its hours now
	FreeAgents  int     `json:"freeAgents"`
}

func (e *Engine) dialerOf(id string) *dialerState {
	e.dial.mu.Lock()
	defer e.dial.mu.Unlock()
	if e.dial.m == nil {
		e.dial.m = map[string]*dialerState{}
	}
	d := e.dial.m[id]
	if d == nil {
		d = &dialerState{ratio: 1}
		e.dial.m[id] = d
	}
	return d
}

func (d *dialerState) abandonRate() float64 {
	if len(d.recent) == 0 {
		return 0
	}
	n := 0
	for _, a := range d.recent {
		if a {
			n++
		}
	}
	return 100 * float64(n) / float64(len(d.recent))
}

// DialerStatuses reports every campaign.
func (e *Engine) DialerStatuses() []DialerStatus {
	cfg := e.Config()
	var out []DialerStatus
	for i := range cfg.Campaigns {
		k := &cfg.Campaigns[i]
		d := e.dialerOf(k.ID)
		e.dial.mu.Lock()
		st := DialerStatus{Campaign: k.ID, Ringing: d.inflight, Ratio: math.Round(d.ratio*100) / 100, AbandonRate: math.Round(d.abandonRate()*10) / 10}
		e.dial.mu.Unlock()
		st.Open = cfg.CampaignOpen(k, time.Now())
		st.FreeAgents = len(e.freeAgents(k.Queue))
		out = append(out, st)
	}
	return out
}

// freeAgents are a queue's agents ready for a call.
func (e *Engine) freeAgents(queue string) []string {
	var out []string
	for _, q := range e.Queues() {
		if q.Number != queue {
			continue
		}
		for _, a := range q.Agents {
			if a.State == "available" {
				out = append(out, a.Extension)
			}
		}
	}
	return out
}

// dialerLoop places the calls of every running campaign, once a second.
func (e *Engine) dialerLoop(ctx context.Context) {
	if e.Dialer != nil {
		_ = e.Dialer.ResetDialling()
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if e.Dialer == nil {
			continue
		}
		cfg := e.Config()
		now := time.Now()
		for i := range cfg.Campaigns {
			k := cfg.Campaigns[i]
			if !k.Active || k.Mode == "preview" || !cfg.CampaignOpen(&k, now) {
				continue
			}
			e.paceCampaign(cfg, &k, now)
		}
	}
}

// paceCampaign works out how many calls to start for a campaign now, and starts them.
func (e *Engine) paceCampaign(cfg *config.Config, k *config.Campaign, now time.Time) {
	agents := e.freeAgents(k.Queue)
	d := e.dialerOf(k.ID)
	e.dial.mu.Lock()
	free := 0
	for _, a := range agents {
		if time.Now().After(e.dial.reserved[a]) {
			free++
		}
	}
	ratio := 1.0
	switch k.Mode {
	case "power":
		ratio = k.Ratio
	case "predictive":
		ratio = d.ratio
	}
	if (k.Mode == "power" || k.Mode == "predictive") && d.abandonRate() > k.MaxAbandon {
		ratio = 1 // over the limit: one call per agent until it is back under
	}
	want := int(math.Ceil(float64(free)*ratio)) - d.inflight
	e.dial.mu.Unlock()
	if want <= 0 {
		return
	}
	due, err := e.Dialer.DueDialContacts(k.ID, now, want)
	if err != nil {
		return
	}
	for _, ct := range due {
		if e.Dialer.OnDNC(ct.Number) {
			ct.State, ct.Result = "dnc", "on the do-not-call list"
			_ = e.Dialer.PutDialContact(ct)
			continue
		}
		ct.State, ct.Attempts, ct.LastCall = "dialling", ct.Attempts+1, now
		if err := e.Dialer.PutDialContact(ct); err != nil {
			continue
		}
		e.dial.mu.Lock()
		d.inflight++
		e.dial.mu.Unlock()
		go e.dialContact(*k, ct)
	}
}

// dialAs is the extension a campaign's calls are made as: its own, else the first agent of its queue.
func dialAs(cfg *config.Config, k *config.Campaign) *config.Extension {
	if x := cfg.FindExtension(k.As); x != nil {
		return x
	}
	if q := cfg.FindQueue(k.Queue); q != nil {
		for _, a := range q.Agents {
			if x := cfg.FindExtension(a); x != nil && x.Outbound {
				return x
			}
		}
	}
	return nil
}

// settle records how a try ended: done, or another try later, or out of tries.
func (e *Engine) settle(k config.Campaign, ct store.DialContact, result string, again bool) {
	ct.Result = result
	switch {
	case !again:
		ct.State = "done"
	case ct.Attempts >= k.Attempts:
		ct.State = "failed"
	default:
		ct.State, ct.Next = "retry", time.Now().Add(time.Duration(k.RetryMinutes)*time.Minute)
	}
	_ = e.Dialer.PutDialContact(ct)
	e.publish("dialer.contact", ct)
}

// dialContact calls one contact and, when a person answers, hands them to the queue's agents.
func (e *Engine) dialContact(k config.Campaign, ct store.DialContact) {
	d := e.dialerOf(k.ID)
	done := func() {
		e.dial.mu.Lock()
		d.inflight--
		e.dial.mu.Unlock()
	}
	cfg := e.Config()
	x := dialAs(cfg, &k)
	if x == nil {
		done()
		e.settle(k, ct, "failed: no extension to call as", false)
		return
	}
	onward := e.route(cfg, x, ct.Number)
	if onward.reject != 0 || len(onward.stages) == 0 || onward.local != "" {
		done()
		e.settle(k, ct, "failed: "+plainRefusal(ct.Number, onward.reason), false)
		return
	}
	for i := range onward.stages {
		onward.stages[i].timeout = time.Duration(k.RingSeconds) * time.Second
	}
	name := ct.Name
	if name == "" {
		name = ct.Number
	}
	h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: x.Number, fromName: k.Name, callerExt: x.Number,
		cidOverride: k.CallerID, answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		xfer: make(chan *Leg, 1)}
	go h.run(onward)
	leg := <-h.xfer
	done()
	if leg == nil {
		h.mu.Lock()
		code, busy := h.endCode, h.busy
		h.mu.Unlock()
		res := "no-answer"
		switch {
		case busy:
			res = "busy"
		case code == 404 || code == 484 || code == 604:
			e.settle(k, ct, "failed: not a working number", false)
			return
		case code >= 500 && code < 600 && code != 503:
			res = "failed"
		}
		e.settle(k, ct, res, true)
		return
	}
	c := &Call{e: e, ID: config.NewID(), start: time.Now(), answered: make(chan struct{}), cancelled: make(chan struct{}),
		results: make(chan forkResult, 64), from: ct.Number, fromName: name, callerExt: x.Number, to: ct.Number,
		toName: k.Name, direction: "outbound", trunk: onward.trunk, localAnswer: true, answer: time.Now()}
	leg.call = c
	leg.Number, leg.Name = ct.Number, name
	c.caller = leg
	c.ansOnce.Do(func() { close(c.answered) })
	e.mu.Lock()
	e.calls[c.ID] = c
	e.mu.Unlock()
	ct.CallID = c.ID
	e.publish("call.started", c.info())

	machine := false
	if k.Machine != "agent" {
		machine = c.detectMachine()
	}
	select {
	case <-c.cancelled:
		e.settle(k, ct, "hung up", true)
		return
	default:
	}
	if machine {
		e.dial.mu.Lock()
		d.machines++
		e.dial.mu.Unlock()
		if k.Machine == "message" {
			c.waitForQuiet(1200*time.Millisecond, 25*time.Second)
			if c.playCampaignMessage(k, &ct) {
				return
			}
			e.settle(k, ct, "machine: message left", false)
		} else {
			e.settle(k, ct, "machine", true)
		}
		c.hangup("an answering machine", "system")
		return
	}
	// A person: an agent, or (none free in time) the message, counted as abandoned. An agent handed a
	// person is held for them until the queue rings, so two answers at once never share one agent.
	agent := ""
	deadline := time.Now().Add(2 * time.Second)
	for {
		free := e.freeAgents(k.Queue)
		e.dial.mu.Lock()
		if e.dial.reserved == nil {
			e.dial.reserved = map[string]time.Time{}
		}
		for _, a := range free {
			if time.Now().After(e.dial.reserved[a]) {
				agent = a
				e.dial.reserved[a] = time.Now().Add(6 * time.Second)
				break
			}
		}
		e.dial.mu.Unlock()
		if agent != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	abandoned := agent == ""
	e.dial.mu.Lock()
	d.recent = append(d.recent, abandoned)
	if len(d.recent) > dialerWindow {
		d.recent = d.recent[len(d.recent)-dialerWindow:]
	}
	if k.Mode == "predictive" && len(d.recent) >= 10 {
		// Keep the abandon rate under the limit: lean in when well under it, back off when near it.
		switch r := d.abandonRate(); {
		case r > k.MaxAbandon*0.8:
			d.ratio = math.Max(1, d.ratio-0.2)
		case r < k.MaxAbandon*0.4:
			d.ratio = math.Min(3, d.ratio+0.1)
		}
	}
	e.dial.mu.Unlock()
	if abandoned {
		if c.playCampaignMessage(k, &ct) {
			return
		}
		e.settle(k, ct, "abandoned", true)
		c.hangup("no agent free", "system")
		return
	}
	ct.Agent = agent
	e.settle(k, ct, "answered", false)
	go func() {
		// Once the queue rings the agent (or after a moment), the hold is no longer needed.
		time.Sleep(1500 * time.Millisecond)
		for range 30 {
			ringing := true
			for _, a := range e.freeAgents(k.Queue) {
				ringing = ringing && a != agent
			}
			if ringing {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		e.dial.mu.Lock()
		delete(e.dial.reserved, agent)
		e.dial.mu.Unlock()
	}()
	c.goTo(e.Config(), config.Destination{Type: "queue", Target: k.Queue})
}

// playCampaignMessage plays the campaign's recorded message (or a tone without one) and listens for
// 9: put on the do-not-call list. It reports whether the person asked not to be called (and the
// contact is settled).
func (c *Call) playCampaignMessage(k config.Campaign, ct *store.DialContact) bool {
	var pcm []int16
	if c.e.Mail != nil {
		if b, err := c.e.Mail.Greeting("campaign-" + k.ID); err == nil {
			pcm, _ = media.DecodeWAV(b)
		}
	}
	if len(pcm) == 0 {
		pcm = media.ToneSamples(media.ToneConfirm)
	}
	keys := media.NewCapture(1)
	c.caller.ep.Record(keys.Packet)
	defer c.caller.ep.Record(nil)
	k2, ended := c.play(pcm, keys.Keys)
	if !ended && k2 == 0 {
		select {
		case k2 = <-keys.Keys:
		case <-time.After(3 * time.Second):
		case <-c.cancelled:
		}
	}
	if k2 == '9' {
		_ = c.e.Dialer.AddDNC("asked on a call", ct.Number)
		ct.State, ct.Result = "dnc", "opted out"
		_ = c.e.Dialer.PutDialContact(*ct)
		c.play(media.ToneSamples(media.ToneConfirm), nil)
		c.hangup("asked not to be called", "system")
		return true
	}
	return false
}

// AMD settings: speech louder than this RMS; a greeting longer than this is a machine's; this much
// quiet after a short greeting is a person waiting for an answer.
const (
	amdSpeechRMS   = 500
	amdGreeting    = 1600 * time.Millisecond
	amdAfterWords  = 800 * time.Millisecond
	amdMaxAnalysis = 4 * time.Second
	amdGap         = 300 * time.Millisecond
)

// detectMachine listens to the first seconds after an answer: a long, unbroken greeting is a machine;
// a short "hello?" followed by quiet is a person. When in doubt, a person: a machine passed to an
// agent costs a moment, a person hung up on costs much more.
func (c *Call) detectMachine() bool {
	frames := make(chan float64, 256)
	var dec media.Decoders
	c.caller.ep.Record(func(pkt []byte) {
		if len(pkt) < 12 {
			return
		}
		pcm := dec.Narrowband(int(pkt[1]&0x7F), pkt[12:])
		if len(pcm) == 0 {
			return
		}
		var s float64
		for _, v := range pcm {
			s += float64(v) * float64(v)
		}
		select {
		case frames <- math.Sqrt(s / float64(len(pcm))):
		default:
		}
	})
	defer c.caller.ep.Record(nil)
	var speech, quiet, sinceVoice time.Duration
	heard := false
	const frame = 20 * time.Millisecond
	deadline := time.After(amdMaxAnalysis)
	for {
		select {
		case <-c.cancelled:
			return false
		case <-deadline:
			return false
		case rms := <-frames:
			if rms > amdSpeechRMS {
				heard = true
				speech += frame
				quiet, sinceVoice = 0, 0
				if speech >= amdGreeting {
					return true
				}
				continue
			}
			quiet += frame
			sinceVoice += frame
			if heard && quiet >= amdAfterWords {
				return false
			}
			if heard && sinceVoice > amdGap && speech < 200*time.Millisecond {
				// A click or a breath, not words: start over.
				heard, speech = false, 0
			}
		}
	}
}

// waitForQuiet waits for the far end to stop talking (a machine's greeting ending) for quiet, at most max.
func (c *Call) waitForQuiet(quiet, max time.Duration) {
	got := make(chan bool, 256)
	var dec media.Decoders
	c.caller.ep.Record(func(pkt []byte) {
		if len(pkt) < 12 {
			return
		}
		pcm := dec.Narrowband(int(pkt[1]&0x7F), pkt[12:])
		var s float64
		for _, v := range pcm {
			s += float64(v) * float64(v)
		}
		loud := len(pcm) > 0 && math.Sqrt(s/float64(len(pcm))) > amdSpeechRMS
		select {
		case got <- loud:
		default:
		}
	})
	defer c.caller.ep.Record(nil)
	var still time.Duration
	end := time.After(max)
	for still < quiet {
		select {
		case <-c.cancelled:
			return
		case <-end:
			return
		case loud := <-got:
			if loud {
				still = 0
			} else {
				still += 20 * time.Millisecond
			}
		case <-time.After(100 * time.Millisecond):
			still += 100 * time.Millisecond // nothing arriving is quiet too
		}
	}
}

// PreviewNext gives an agent the next contact of a preview campaign, held for them.
func (e *Engine) PreviewNext(id, agent string) (store.DialContact, bool) {
	cfg := e.Config()
	k := cfg.FindCampaign(id)
	if k == nil || e.Dialer == nil {
		return store.DialContact{}, false
	}
	for range 20 {
		due, err := e.Dialer.DueDialContacts(id, time.Now(), 1)
		if err != nil || len(due) == 0 {
			return store.DialContact{}, false
		}
		ct := due[0]
		if e.Dialer.OnDNC(ct.Number) {
			ct.State, ct.Result = "dnc", "on the do-not-call list"
			_ = e.Dialer.PutDialContact(ct)
			continue
		}
		ct.State, ct.Agent = "dialling", agent
		_ = e.Dialer.PutDialContact(ct)
		return ct, true
	}
	return store.DialContact{}, false
}

// PreviewCall calls a held contact for the agent: the agent's phone rings first, then the contact,
// with the campaign's caller ID. skip leaves the contact for another try instead.
func (e *Engine) PreviewCall(id, contact, agent string, skip bool) (string, error) {
	cfg := e.Config()
	k := cfg.FindCampaign(id)
	if k == nil || e.Dialer == nil {
		return "", errNoCampaign
	}
	ct, err := e.Dialer.DialContactByID(id, contact)
	if err != nil {
		return "", err
	}
	if skip {
		ct.State, ct.Result = "pending", "skipped"
		_ = e.Dialer.PutDialContact(ct)
		return "", nil
	}
	if e.Dialer.OnDNC(ct.Number) {
		e.settle(*k, ct, "on the do-not-call list", false)
		return "", errDNC
	}
	ct.Attempts++
	ct.LastCall = time.Now()
	callID, err := e.originate(agent, ct.Number, 30*time.Second, k.CallerID)
	if err != nil {
		e.settle(*k, ct, "failed: "+err.Error(), true)
		return "", err
	}
	ct.CallID, ct.Agent = callID, agent
	e.settle(*k, ct, "answered", false)
	return callID, nil
}

type dialerError string

func (e dialerError) Error() string { return string(e) }

const (
	errNoCampaign = dialerError("no such campaign")
	errDNC        = dialerError("that number is on the do-not-call list")
)
