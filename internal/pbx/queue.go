package pbx

import (
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// queueState is what BitVoice knows about one queue between calls.
type queueState struct {
	mu      sync.Mutex
	waiting []*Call              // oldest first; only the first is offered to agents
	lastEnd map[string]time.Time // when each agent's last queue call ended (longest-idle, wrap-up)
	next    int                  // round-robin position
	stats   QueueTotals
}

// QueueTotals counts a queue's calls since BitVoice started.
type QueueTotals struct {
	Answered  int `json:"answered"`
	Abandoned int `json:"abandoned"`
	TimedOut  int `json:"timedOut"`
	// LongestWait is the longest any answered caller waited, in seconds.
	LongestWait int `json:"longestWait"`
}

// QueueStatus is a queue right now, for the wallboard.
type QueueStatus struct {
	Number  string        `json:"number"`
	Name    string        `json:"name"`
	Waiting []QueueCaller `json:"waiting"`
	Agents  []AgentStatus `json:"agents"`
	Totals  QueueTotals   `json:"totals"`
}

// QueueCaller is someone waiting.
type QueueCaller struct {
	CallID string    `json:"callId"`
	From   string    `json:"from"`
	Name   string    `json:"name"`
	Since  time.Time `json:"since"`
	VIP    bool      `json:"vip,omitempty"`
}

// AgentStatus is an agent's state: available, ringing, busy (on a call), wrap-up or offline (no phone).
type AgentStatus struct {
	Extension string    `json:"extension"`
	Name      string    `json:"name"`
	State     string    `json:"state"`            // available, ringing, busy, wrap-up, paused, logged-out, offline
	Reason    string    `json:"reason,omitempty"` // why paused
	LastCall  time.Time `json:"lastCall,omitzero"`
}

var toneQueue = media.Tone{Freqs: []float64{392, 523}, Level: 0.1, On: 600 * time.Millisecond, Off: 5400 * time.Millisecond}

func (e *Engine) queue(number string) *queueState {
	e.mu.Lock()
	defer e.mu.Unlock()
	q := e.queues[number]
	if q == nil {
		q = &queueState{lastEnd: map[string]time.Time{}}
		e.queues[number] = q
	}
	return q
}

// busyExtensions lists extensions on a call or being rung now.
func (e *Engine) busyExtensions() (busy, ringing map[string]bool) {
	busy, ringing = e.localBusy()
	if e.Cluster != nil {
		cfg := e.Config()
		for _, x := range cfg.Extensions {
			if !busy[x.Number] && e.Cluster.RemoteBusy(x.Number) {
				busy[x.Number] = true // on a call on another server
			} else if !busy[x.Number] && !ringing[x.Number] && e.Cluster.RemoteRinging(x.Number) {
				ringing[x.Number] = true
			}
		}
	}
	return busy, ringing
}

// localBusy is busyExtensions for this server's calls alone.
func (e *Engine) localBusy() (busy, ringing map[string]bool) {
	busy, ringing = map[string]bool{}, map[string]bool{}
	e.mu.Lock()
	calls := make([]*Call, 0, len(e.calls))
	for _, c := range e.calls {
		calls = append(calls, c)
	}
	e.mu.Unlock()
	for _, c := range calls {
		c.mu.Lock()
		if c.caller != nil && c.caller.Ext != "" {
			busy[c.caller.Ext] = true
		}
		if c.callee != nil && c.callee.Ext != "" {
			busy[c.callee.Ext] = true
		}
		for _, f := range c.forks {
			if f.Ext != "" {
				ringing[f.Ext] = true
			}
		}
		c.mu.Unlock()
	}
	return busy, ringing
}

// agentStates works out each agent's state.
func (e *Engine) agentStates(cfg *config.Config, qc *config.Queue, q *queueState) []AgentStatus {
	busy, ringing := e.busyExtensions()
	now := time.Now()
	out := make([]AgentStatus, 0, len(qc.Agents))
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, n := range qc.Agents {
		a := AgentStatus{Extension: n, LastCall: q.lastEnd[n]}
		x := cfg.FindExtension(n)
		if x != nil {
			a.Name = x.Name
		}
		pr := e.presence.get(n)
		a.Reason = pr.Reason
		switch {
		case x == nil || !x.Enabled || len(e.reg.contacts(n)) == 0:
			a.State = "offline"
		case pr.LoggedOut:
			a.State = "logged-out"
		case func() bool { aw, ok := e.awayNow(n); a.Reason = firstNonEmptyStr(a.Reason, aw.Reason); return ok }():
			a.State = "away"
		case busy[n]:
			a.State = "busy"
		case pr.Paused:
			a.State = "paused"
		case busy[n]:
			a.State = "busy"
		case ringing[n]:
			a.State = "ringing"
		case !a.LastCall.IsZero() && now.Sub(a.LastCall) < time.Duration(qc.WrapUpSeconds)*time.Second:
			a.State = "wrap-up"
		default:
			a.State = "available"
		}
		out = append(out, a)
	}
	return out
}

// pickAgents chooses whom to ring next under the queue's strategy.
func (e *Engine) pickAgents(cfg *config.Config, qc *config.Queue, q *queueState) []string {
	var free []AgentStatus
	for _, a := range e.agentStates(cfg, qc, q) {
		if a.State == "available" {
			free = append(free, a)
		}
	}
	if len(free) == 0 {
		return nil
	}
	switch qc.Strategy {
	case "ring-all":
		out := make([]string, len(free))
		for i, a := range free {
			out[i] = a.Extension
		}
		return out
	case "round-robin":
		q.mu.Lock()
		defer q.mu.Unlock()
		for i := range qc.Agents {
			n := qc.Agents[(q.next+i)%len(qc.Agents)]
			if slices.ContainsFunc(free, func(a AgentStatus) bool { return a.Extension == n }) {
				q.next = (q.next + i + 1) % len(qc.Agents)
				return []string{n}
			}
		}
		return nil
	default: // longest-idle: never-used agents first, then the oldest last call
		sort.SliceStable(free, func(i, j int) bool { return free[i].LastCall.Before(free[j].LastCall) })
		return []string{free[0].Extension}
	}
}

// waitInQueue answers the caller, plays the queue tone, and offers the call to agents in turn until one
// answers, the caller hangs up, or the wait runs out.
func (c *Call) waitInQueue(number string) {
	e := c.e
	q := e.queue(number)
	cfg0 := e.Config()
	_, vip := cfg0.IsVIP(c.from)
	c.mu.Lock()
	c.vip = vip
	c.mu.Unlock()
	q.mu.Lock()
	if vip { // to the front, after any VIP already waiting
		at := 0
		for at < len(q.waiting) && q.waiting[at].isVIP() {
			at++
		}
		q.waiting = slices.Insert(q.waiting, at, c)
	} else {
		q.waiting = append(q.waiting, c)
	}
	q.mu.Unlock()
	// A returning caller is offered first to the agent who last answered them.
	sticky := ""
	if qc := cfg0.FindQueue(number); qc != nil && qc.Sticky && e.LastAgent != nil {
		days := qc.StickyDays
		if days == 0 {
			days = 30
		}
		if a := e.LastAgent(c.from, time.Now().AddDate(0, 0, -days)); slices.Contains(qc.Agents, a) {
			sticky = a
		}
	}
	joined := time.Now()
	leave := func() {
		q.mu.Lock()
		q.waiting = slices.DeleteFunc(q.waiting, func(x *Call) bool { return x == c })
		q.mu.Unlock()
		e.publish("queue.changed", map[string]any{"queue": number})
	}
	e.publish("queue.changed", map[string]any{"queue": number})

	c.mu.Lock()
	pt := 0
	if c.caller.codecName() == "PCMA" {
		pt = 8
	}
	c.player = media.NewPlayer(c.caller.ep, pt)
	c.player.Play(toneQueue)
	c.mu.Unlock()

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		cfg := e.Config()
		qc := cfg.FindQueue(number)
		if qc == nil {
			leave()
			c.hangup("queue removed", "system")
			return
		}
		if qc.MaxWaitSeconds > 0 && time.Since(joined) > time.Duration(qc.MaxWaitSeconds)*time.Second {
			leave()
			q.mu.Lock()
			q.stats.TimedOut++
			q.mu.Unlock()
			c.queueTimeout(qc)
			return
		}
		q.mu.Lock()
		first := len(q.waiting) > 0 && q.waiting[0] == c
		q.mu.Unlock()
		var agents []string
		if first && sticky != "" {
			if e.agentFree(cfg, qc, q, sticky) {
				agents = []string{sticky}
			}
			sticky = "" // one try: then as usual
		}
		if first && len(agents) == 0 {
			agents = e.pickAgents(cfg, qc, q)
		}
		if len(agents) == 0 {
			select {
			case <-c.cancelled:
				leave()
				q.mu.Lock()
				q.stats.Abandoned++
				q.mu.Unlock()
				return
			case <-tick.C:
				continue
			}
		}
		ring := time.Duration(qc.RingSeconds) * time.Second
		if qc.MaxWaitSeconds > 0 {
			ring = min(ring, time.Until(joined.Add(time.Duration(qc.MaxWaitSeconds)*time.Second))+50*time.Millisecond)
		}
		if agent := c.offerToAgents(cfg, qc, agents, ring); agent != "" {
			leave()
			waited := int(time.Since(joined).Seconds())
			q.mu.Lock()
			q.stats.Answered++
			q.stats.LongestWait = max(q.stats.LongestWait, waited)
			q.mu.Unlock()
			<-c.cancelled // the call ends
			q.mu.Lock()
			q.lastEnd[agent] = time.Now()
			q.mu.Unlock()
			e.publish("queue.changed", map[string]any{"queue": number})
			return
		}
		select {
		case <-c.cancelled:
			leave()
			q.mu.Lock()
			q.stats.Abandoned++
			q.mu.Unlock()
			return
		default:
		}
	}
}

// offerToAgents rings agents for up to ring and returns who answered.
func (c *Call) offerToAgents(cfg *config.Config, qc *config.Queue, agents []string, ring time.Duration) string {
	c.mu.Lock()
	c.stageNo++
	stageNo := c.stageNo
	c.mu.Unlock()
	started := 0
	for _, n := range agents {
		x := cfg.FindExtension(n)
		for _, b := range c.e.reg.contacts(n) {
			if c.startFork(target{ext: n, name: x.Name, binding: b}, stageNo) {
				started++
			}
		}
	}
	if started == 0 {
		return ""
	}
	c.e.publish("queue.changed", map[string]any{"queue": qc.Number})
	timer := time.NewTimer(ring)
	defer timer.Stop()
	for failed := 0; failed < started; {
		select {
		case <-c.cancelled:
			return ""
		case r := <-c.results:
			if r.stage != stageNo {
				continue
			}
			if r.code == 200 {
				return r.leg.Ext
			}
			failed++
		case <-timer.C:
			c.cancelForks(nil)
			return ""
		}
	}
	return ""
}

// queueTimeout sends a caller who waited too long to a mailbox, or says goodbye.
func (c *Call) queueTimeout(qc *config.Queue) {
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
		c.player = nil
	}
	c.mu.Unlock()
	if d := qc.Timeout; d != nil && d.Type == "voicemail" {
		c.leaveMessage(d.Target)
		return
	}
	c.play(media.ToneSamples(media.ToneConfirm), nil)
	c.hangup("queue wait limit", "system")
}

// Queues reports every queue's callers, agents and totals.
func (e *Engine) Queues() []QueueStatus {
	cfg := e.Config()
	out := make([]QueueStatus, 0, len(cfg.Queues))
	for i := range cfg.Queues {
		qc := &cfg.Queues[i]
		q := e.queue(qc.Number)
		s := QueueStatus{Number: qc.Number, Name: qc.Name, Agents: e.agentStates(cfg, qc, q), Waiting: []QueueCaller{}}
		q.mu.Lock()
		s.Totals = q.stats
		waiting := append([]*Call(nil), q.waiting...)
		q.mu.Unlock()
		for _, c := range waiting {
			c.mu.Lock()
			s.Waiting = append(s.Waiting, QueueCaller{CallID: c.ID, From: c.from, Name: c.fromName, Since: c.answer, VIP: c.vip})
			c.mu.Unlock()
		}
		out = append(out, s)
	}
	return out
}

// agentFree says whether one agent of the queue is available now.
func (e *Engine) agentFree(cfg *config.Config, qc *config.Queue, q *queueState, ext string) bool {
	for _, a := range e.agentStates(cfg, qc, q) {
		if a.Extension == ext {
			return a.State == "available"
		}
	}
	return false
}

func (c *Call) isVIP() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vip
}
