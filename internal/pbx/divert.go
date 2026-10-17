package pbx

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// Divert is where a person sends their own calls, set from their phone with feature codes, from the
// staff portal or by an administrator: forward everything, forward when busy, forward when not
// answered, and their own switch for follow-me. It is live state, kept across restarts, not
// configuration: dialling *72 does not make a configuration revision.
type Divert struct {
	Ext      string `json:"ext"`
	All      string `json:"all,omitempty"`      // every call goes here
	Busy     string `json:"busy,omitempty"`     // calls while the extension is on a call
	NoAnswer string `json:"noAnswer,omitempty"` // calls nobody answers in the ring time
	// FollowMe is the person's own switch over the configured follow-me: nil follows the configuration.
	FollowMe *bool `json:"followMe,omitempty"`
}

func (d Divert) empty() bool {
	return d.All == "" && d.Busy == "" && d.NoAnswer == "" && d.FollowMe == nil
}

// DivertState is what is saved across restarts: the diverts and the do-not-disturbs set by hand.
type DivertState struct {
	Diverts []Divert `json:"diverts"`
	Away    []Away   `json:"away"`
	// Toggles are the IDs of the day/night switches switched over.
	Toggles []string `json:"toggles,omitempty"`
}

type diverts struct {
	mu     sync.Mutex
	m      map[string]Divert
	saveMu sync.Mutex
}

var divertNumberRe = regexp.MustCompile(`^\+?[0-9]{2,20}$`)

// Errors from setting a divert.
var (
	ErrNoExtension = errors.New("no such extension")
	ErrDivertSelf  = errors.New("calls cannot be forwarded to the extension itself")
	ErrDivertBad   = errors.New("forward to a number: digits, optionally starting with +")
	ErrDivertOut   = errors.New("this extension may not call outside numbers, so it cannot forward to one")
)

// Diverts lists every extension with a divert set, in number order.
func (e *Engine) Diverts() []Divert {
	e.div.mu.Lock()
	defer e.div.mu.Unlock()
	out := make([]Divert, 0, len(e.div.m))
	for _, d := range e.div.m {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Divert) int { return compareNumbers(a.Ext, b.Ext) })
	return out
}

// DivertOf is one extension's divert (empty when none is set).
func (e *Engine) DivertOf(ext string) Divert {
	e.div.mu.Lock()
	defer e.div.mu.Unlock()
	d, ok := e.div.m[ext]
	if !ok {
		d.Ext = ext
	}
	return d
}

// SetDivert replaces an extension's divert, checking every number it forwards to.
func (e *Engine) SetDivert(d Divert) error {
	cfg := e.Config()
	x := cfg.FindExtension(d.Ext)
	if x == nil {
		return ErrNoExtension
	}
	for _, n := range []*string{&d.All, &d.Busy, &d.NoAnswer} {
		*n = cleanNumber(strings.TrimSpace(*n))
		if *n == "" {
			continue
		}
		if err := checkForward(cfg, x, *n); err != nil {
			return err
		}
	}
	e.div.mu.Lock()
	if e.div.m == nil {
		e.div.m = map[string]Divert{}
	}
	if d.empty() {
		delete(e.div.m, d.Ext)
	} else {
		e.div.m[d.Ext] = d
	}
	e.div.mu.Unlock()
	e.saveDiverts()
	e.publish("divert.changed", d)
	e.publish("presence.changed", map[string]string{"extension": d.Ext})
	return nil
}

func checkForward(cfg *config.Config, x *config.Extension, n string) error {
	switch {
	case n == x.Number:
		return ErrDivertSelf
	case !divertNumberRe.MatchString(n):
		return ErrDivertBad
	case !dialsInside(cfg, n) && !x.Outbound:
		return ErrDivertOut
	}
	return nil
}

// dialsInside says whether a number reaches something in this phone system (not a trunk).
func dialsInside(cfg *config.Config, n string) bool {
	return cfg.FindExtension(n) != nil || cfg.FindRingGroup(n) != nil || cfg.FindQueue(n) != nil ||
		cfg.FindMenuNumber(n) != nil || cfg.FindConference(n) != nil || cfg.FindDirectoryNumber(n) != nil || cfg.FindScriptNumber(n) != nil || cfg.FindARIAppNumber(n) != nil
}

// SetDND switches an extension's do not disturb on or off, as its feature codes do.
func (e *Engine) SetDND(ext string, on bool) {
	if on {
		e.SetAway(Away{Ext: ext, Reason: "Do not disturb", Source: "manual"})
	} else {
		e.ClearAway(ext, "manual")
	}
}

// DND says whether an extension has do not disturb set by hand.
func (e *Engine) DND(ext string) bool {
	a, ok := e.awayNow(ext)
	return ok && a.Source == "manual"
}

// LoadDiverts restores what saveDiverts kept, dropping extensions that are gone.
func (e *Engine) LoadDiverts(s DivertState) {
	cfg := e.Config()
	e.div.mu.Lock()
	e.div.m = map[string]Divert{}
	for _, d := range s.Diverts {
		if cfg.FindExtension(d.Ext) != nil && !d.empty() {
			e.div.m[d.Ext] = d
		}
	}
	e.div.mu.Unlock()
	e.away.mu.Lock()
	if e.away.m == nil {
		e.away.m = map[string]Away{}
	}
	for _, a := range s.Away {
		if cfg.FindExtension(a.Ext) != nil && a.Source == "manual" && (a.Until.IsZero() || time.Now().Before(a.Until)) {
			e.away.m[a.Ext] = a
		}
	}
	e.away.mu.Unlock()
	e.loadToggles(s.Toggles)
}

// saveDiverts keeps the diverts and the hand-set do not disturbs.
func (e *Engine) saveDiverts() {
	if e.SaveDiverts == nil {
		return
	}
	var s DivertState
	s.Diverts = e.Diverts()
	s.Toggles = e.TogglesOn()
	for _, a := range e.Aways() {
		if a.Source == "manual" {
			s.Away = append(s.Away, a)
		}
	}
	e.div.saveMu.Lock()
	err := e.SaveDiverts(s)
	e.div.saveMu.Unlock()
	if err != nil {
		e.Log.Error("could not save call forwarding", "err", err)
	}
}

// pruneDiverts drops the diverts of extensions that were removed, or that forward where they no longer may.
func (e *Engine) pruneDiverts() {
	cfg := e.Config()
	changed := false
	e.div.mu.Lock()
	for k, d := range e.div.m {
		x := cfg.FindExtension(k)
		if x == nil {
			delete(e.div.m, k)
			changed = true
			continue
		}
		for _, n := range []*string{&d.All, &d.Busy, &d.NoAnswer} {
			if *n != "" && checkForward(cfg, x, *n) != nil {
				*n = ""
				changed = true
			}
		}
		if d.empty() {
			delete(e.div.m, k)
		} else {
			e.div.m[k] = d
		}
	}
	e.div.mu.Unlock()
	if changed {
		e.saveDiverts()
	}
}

// onCall says whether an extension is talking on a call now, either side of it.
func (e *Engine) onCall(ext string) bool {
	for _, c := range e.allCalls() {
		c.mu.Lock()
		busy := !c.ended && ((c.callerExt == ext && (c.callee != nil || c.localAnswer)) || (c.callee != nil && c.callee.Ext == ext))
		c.mu.Unlock()
		if busy {
			return true
		}
	}
	return false
}

// FollowMeOn says whether follow-me rings for an extension now.
func (e *Engine) FollowMeOn(x *config.Extension) bool { return e.followMeOn(x) }

// followMeOn says whether follow-me rings for an extension now: configured on, unless the person
// switched it off (or configured with numbers and switched on by the person).
func (e *Engine) followMeOn(x *config.Extension) bool {
	if len(x.FollowMe.Numbers) == 0 {
		return false
	}
	if d := e.DivertOf(x.Number); d.FollowMe != nil {
		return *d.FollowMe
	}
	return x.FollowMe.Enabled
}

// forwardPlan is where a call to x goes when x forwards it to n: another extension (its own settings
// apply, with the loop limit), anything else inside, or an outside number through the outbound routes
// as x.
func (e *Engine) forwardPlan(cfg *config.Config, x *config.Extension, n string, depth int, sc scriptCall) plan {
	if cfg.FindExtension(n) != nil {
		p := e.resolveFor(cfg, config.Destination{Type: "extension", Target: n}, depth+1, sc)
		return p
	}
	if dialsInside(cfg, n) {
		return e.route(cfg, x, n)
	}
	if !x.Outbound {
		return plan{reject: 403, reason: "Forwarding Outside Not Allowed"}
	}
	p := e.outbound(cfg, x, n)
	p.toName = x.Name + " (forwarded)"
	return p
}

// diverted marks a forward's calls out with the extension they were forwarded from.
func diverted(p plan, x *config.Extension, reason string) plan {
	from := x.CallerID
	if from == "" {
		from = x.Number
	}
	for i := range p.stages {
		for j := range p.stages[i].targets {
			if t := &p.stages[i].targets[j]; t.trunk != nil && t.divertFrom == "" {
				t.divertFrom, t.divertReason = from, reason
			}
		}
	}
	return p
}

// followMeStages are the stages follow-me adds after the extension's own phones.
func (e *Engine) followMeStages(cfg *config.Config, x *config.Extension, depth int) []stage {
	fm := x.FollowMe
	ring := time.Duration(fm.RingSeconds) * time.Second
	var out []stage
	var together stage
	together.timeout = ring
	for _, n := range fm.Numbers {
		var st stage
		if y := cfg.FindExtension(n); y != nil {
			if !y.Enabled {
				continue
			}
			for _, b := range e.reg.contacts(n) {
				st.targets = append(st.targets, target{ext: n, name: y.Name, binding: b})
			}
			if e.hasApps(n) {
				st.wake = []waking{{n, y.Name}}
			}
		} else {
			if !x.Outbound {
				continue
			}
			op := e.outbound(cfg, x, n)
			if op.reject != 0 || len(op.stages) == 0 {
				continue
			}
			// The first trunk that can take it: follow-me rings a number, it does not fail over trunks.
			for _, t := range op.stages[0].targets {
				t.confirm = fm.Confirm
				t.name = x.Name + " (follow-me)"
				st.targets = append(st.targets, t)
				break
			}
		}
		if len(st.targets) == 0 && len(st.wake) == 0 {
			continue
		}
		if fm.Strategy == "together" {
			together.targets = append(together.targets, st.targets...)
			together.wake = append(together.wake, st.wake...)
			continue
		}
		st.timeout = ring
		out = append(out, st)
	}
	if fm.Strategy == "together" && (len(together.targets) > 0 || len(together.wake) > 0) {
		out = append(out, together)
	}
	return out
}

// featureCode turns a dialled feature code into a plan; ok is false when dialled is not one.
func (e *Engine) featureCode(cfg *config.Config, caller *config.Extension, dialled string) (plan, bool) {
	fc := cfg.System.Codes
	is := func(code string) bool { return code != "" && code != "off" && dialled == code }
	// prefixed returns what follows a code, when dialled starts with it and digits follow.
	prefixed := func(code string) (string, bool) {
		if code == "" || code == "off" || !strings.HasPrefix(dialled, code) {
			return "", false
		}
		rest := dialled[len(code):]
		if rest == "" || strings.Trim(rest, "0123456789") != "" {
			return "", false
		}
		return rest, true
	}
	local := func(kind, box, name string) (plan, bool) {
		return plan{local: kind, box: box, toName: name, direction: "internal"}, true
	}
	switch {
	case is(fc.ForwardAll), is(fc.ForwardBusy), is(fc.ForwardNoAnswer):
		return local("divert", dialled, "Call forwarding")
	case is(fc.ForwardAllOff), is(fc.ForwardBusyOff), is(fc.ForwardNoAnswerOff),
		is(fc.DNDOn), is(fc.DNDOff), is(fc.DNDToggle), is(fc.FollowMe):
		return local("divert", dialled, "Call forwarding")
	case is(fc.Pickup):
		return local("grouppickup", "", "Call pickup")
	case is(fc.WakeUp):
		return local("wakeup", "", "Wake-up call")
	}
	for _, code := range []string{fc.ForwardAll, fc.ForwardBusy, fc.ForwardNoAnswer} {
		if n, ok := prefixed(code); ok && len(n) >= 2 {
			return local("divert", code+"="+n, "Call forwarding")
		}
	}
	// The longest prefix wins, so *80 (intercom) is not taken for *8 then 0.
	type pre struct {
		code string
		kind string
	}
	pres := []pre{{fc.DirectedPickup, "pickup"}, {fc.Intercom, "intercom"}, {fc.SpeedDial, "speed"}, {fc.RoomStatus, "room"}, {fc.Toggle, "toggle"}}
	slices.SortFunc(pres, func(a, b pre) int { return len(b.code) - len(a.code) })
	for _, pr := range pres {
		rest, ok := prefixed(pr.code)
		if !ok {
			continue
		}
		switch pr.kind {
		case "pickup":
			return local("directedpickup", rest, "Call pickup")
		case "intercom":
			return e.intercomPlan(cfg, caller, rest), true
		case "room":
			return local("roomstatus", rest, "Room status")
		case "toggle":
			t := cfg.FindToggleCode(rest)
			if t == nil {
				return plan{reject: 404, reason: "No Such Toggle", direction: "internal"}, true
			}
			if caller == nil || !t.ToggleAllowed(caller.Number) {
				return plan{reject: 403, reason: "Not Allowed To Switch This Toggle", direction: "internal"}, true
			}
			return local("toggle", t.ID, t.Name)
		case "speed":
			n := speedDial(cfg, caller, rest)
			if n == "" {
				return plan{reject: 404, reason: "No Such Speed Dial", direction: "internal"}, true
			}
			return e.route(cfg, caller, n), true
		}
	}
	return plan{}, false
}

// speedDial finds the number behind a speed dial code: the person's own first, then the company's.
func speedDial(cfg *config.Config, caller *config.Extension, code string) string {
	if caller != nil {
		for _, s := range caller.SpeedDials {
			if s.Code == code {
				return s.Number
			}
		}
	}
	for _, s := range cfg.System.SpeedDials {
		if s.Code == code {
			return s.Number
		}
	}
	return ""
}

// intercomPlan rings an extension's phones with auto-answer, two ways, unless it refuses intercom.
func (e *Engine) intercomPlan(cfg *config.Config, caller *config.Extension, ext string) plan {
	x := cfg.FindExtension(ext)
	if x == nil || !x.Enabled {
		return plan{reject: 404, direction: "internal"}
	}
	if caller != nil && x.Number == caller.Number {
		return plan{reject: 486, reason: "Busy Here", direction: "internal"}
	}
	if x.NoIntercom || x.Kind == "door" {
		p := e.resolve(cfg, config.Destination{Type: "extension", Target: ext}, 0)
		p.direction = "internal"
		return p
	}
	var ts []target
	for _, b := range e.reg.contacts(ext) {
		ts = append(ts, target{ext: ext, name: x.Name, binding: b})
	}
	if len(ts) == 0 {
		return plan{reject: 480, reason: "Not Registered", toName: x.Name, direction: "internal"}
	}
	return plan{toName: "Intercom " + x.Name, direction: "internal", intercom: true,
		stages: []stage{{targets: ts, timeout: 20 * time.Second}}}
}

// divertCode runs a forwarding, do not disturb or follow-me code dialled from a phone. A forwarding
// code alone asks for the number (a tone, the number and #); one with the number after it sets it at
// once. Confirmed with the rising tone; refused with the falling one.
func (c *Call) divertCode(code string) {
	cfg := c.e.Config()
	fc := cfg.System.Codes
	ext := c.callerExt
	defer func() {
		time.Sleep(300 * time.Millisecond)
		c.hangup("feature code", "system")
	}()
	ok := func(good bool) {
		if good {
			c.play(media.ToneSamples(media.ToneConfirm), nil)
		} else {
			c.play(media.ToneSamples(media.ToneOff), nil)
		}
	}
	c.play(make([]int16, media.Rate/4), nil)
	d := c.e.DivertOf(ext)
	number := ""
	if i := strings.IndexByte(code, '='); i > 0 {
		code, number = code[:i], code[i+1:]
	}
	set := func(field *string) {
		if number == "" {
			keys := media.NewCapture(1)
			c.caller.ep.Record(keys.Packet)
			if _, ended := c.play(media.ToneSamples(toneNext), nil); ended {
				c.caller.ep.Record(nil)
				return
			}
			number = c.collectEntry(keys.Keys)
			c.caller.ep.Record(nil)
		}
		if number == "" {
			ok(false)
			return
		}
		*field = number
		if err := c.e.SetDivert(d); err != nil {
			c.e.Log.Info("call forwarding refused", "extension", ext, "to", number, "why", err.Error())
			ok(false)
			return
		}
		ok(true)
	}
	clear := func(field *string) {
		*field = ""
		ok(c.e.SetDivert(d) == nil)
	}
	switch code {
	case fc.ForwardAll:
		set(&d.All)
	case fc.ForwardBusy:
		set(&d.Busy)
	case fc.ForwardNoAnswer:
		set(&d.NoAnswer)
	case fc.ForwardAllOff:
		clear(&d.All)
	case fc.ForwardBusyOff:
		clear(&d.Busy)
	case fc.ForwardNoAnswerOff:
		clear(&d.NoAnswer)
	case fc.DNDOn:
		c.e.SetDND(ext, true)
		ok(true)
	case fc.DNDOff:
		c.e.SetDND(ext, false)
		ok(true)
	case fc.DNDToggle:
		on := !c.e.DND(ext)
		c.e.SetDND(ext, on)
		ok(on) // rising when it is now on, falling when now off
	case fc.FollowMe:
		x := cfg.FindExtension(ext)
		if x == nil || len(x.FollowMe.Numbers) == 0 {
			ok(false) // nothing to follow: an administrator or the portal sets the numbers first
			return
		}
		on := !c.e.followMeOn(x)
		d.FollowMe = &on
		if err := c.e.SetDivert(d); err != nil {
			ok(false)
			return
		}
		ok(on)
	default:
		ok(false)
	}
}

// ringingFor finds the oldest call ringing one of the extensions want accepts, not yet answered.
func (e *Engine) ringingFor(want func(ext string) bool, not *Call) *Call {
	var best *Call
	for _, c := range e.allCalls() {
		if c == not {
			continue
		}
		c.mu.Lock()
		ok := !c.ended && c.callee == nil && !c.localAnswer && c.paging == nil && c.xfer == nil
		match := false
		if ok {
			for _, f := range c.forks {
				if f.Ext != "" && want(f.Ext) {
					match = true
					break
				}
			}
		}
		start := c.start
		c.mu.Unlock()
		if match && (best == nil || start.Before(best.start)) {
			best = c
		}
	}
	return best
}

// pickupRinging answers a call that is ringing someone else's phone: the picker's call is answered,
// the ringing phones stop, and the picker takes the caller.
func (c *Call) pickupRinging(r *Call) {
	if r == nil {
		c.reject(404, "Nothing To Pick Up")
		return
	}
	picker := c.caller
	r.mu.Lock()
	if r.ended || r.callee != nil || r.localAnswer {
		r.mu.Unlock()
		c.reject(404, "Nothing To Pick Up")
		return
	}
	r.callee = picker // claimed: a phone answering now is told no
	r.mu.Unlock()
	if !c.answerHere("Call pickup") {
		r.mu.Lock()
		r.callee = nil
		r.mu.Unlock()
		return
	}
	if !r.answerHere(picker.party()) {
		c.hangup("the picked-up call could not be answered", "system")
		return
	}
	r.cancelForks(nil)
	c.mu.Lock()
	c.ended = true
	c.disposition, c.cause, c.hangupBy = "answered", "picked up a ringing call", "pickup"
	c.mu.Unlock()
	picker.call = r
	r.mu.Lock()
	r.answeredBy = picker.party()
	r.mu.Unlock()
	connect(r.caller, picker)
	r.bridged()
	c.e.mu.Lock()
	delete(c.e.calls, c.ID)
	c.e.mu.Unlock()
	c.writeRecord(c.info(), "")
	c.e.publish("call.ended", c.info())
	c.e.publish("call.updated", r.info())
}

// groupPickup answers the oldest call ringing anyone in the picker's pickup group.
func (c *Call) groupPickup() {
	cfg := c.e.Config()
	me := cfg.FindExtension(c.callerExt)
	if me == nil || me.PickupGroup == "" {
		c.reject(404, "No Pickup Group")
		return
	}
	r := c.e.ringingFor(func(ext string) bool {
		x := cfg.FindExtension(ext)
		return x != nil && ext != me.Number && strings.EqualFold(x.PickupGroup, me.PickupGroup)
	}, c)
	c.pickupRinging(r)
}

// directedPickup answers the oldest call ringing one extension.
func (c *Call) directedPickup(ext string) {
	if ext == c.callerExt {
		c.reject(404, "Nothing To Pick Up")
		return
	}
	c.pickupRinging(c.e.ringingFor(func(x string) bool { return x == ext }, c))
}

// confirmFork asks whoever answered a follow-me call on an outside number to press 1 to take it; a
// voicemail or a silence never does, so the call keeps ringing elsewhere.
func (c *Call) confirmFork(l *Leg, answer func()) {
	keys := make(chan byte, 8)
	l.ep.OnDTMF(func(k byte) {
		select {
		case keys <- k:
		default:
		}
	})
	defer l.ep.OnDTMF(nil)
	pt := 0
	if strings.EqualFold(l.codecName(), "PCMA") {
		pt = 8
	}
	pl := media.NewPlayer(l.ep, pt)
	defer pl.Stop()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	pl.Play(toneNext)
	for {
		select {
		case k := <-keys:
			if k == '1' {
				pl.Stop()
				answer()
				return
			}
		case <-tick.C:
			pl.Play(toneNext)
		case <-deadline:
			l.bye()
			c.forkFailed(l, 480)
			return
		case <-c.cancelled:
			l.bye()
			l.close()
			return
		case <-c.answered:
			l.bye()
			l.close()
			return
		}
	}
}
