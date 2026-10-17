package pbx

import (
	"fmt"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/script"
)

// SimInput is a call to try without making it: who calls, the number they dial and when.
type SimInput struct {
	From  string    `json:"from"`  // an extension, or an outside number (a call coming in)
	To    string    `json:"to"`    // the number dialled: an extension, a feature code, a phone number
	Trunk string    `json:"trunk"` // incoming calls: the trunk it arrives on (an ID); empty for any
	At    time.Time `json:"at"`    // when; zero is now
	// AssumePhones treats every extension as having a phone connected, to see the plan as designed
	// rather than as it is right now.
	AssumePhones bool `json:"assumePhones"`
}

// SimStep is one place a call goes, with where it can go next.
type SimStep struct {
	Kind     string      `json:"kind"` // route, extension, ringgroup, queue, menu, schedule, script, voicemail, receptionist, echo, hangup, outbound, feature, refused, loop
	Target   string      `json:"target,omitempty"`
	Name     string      `json:"name"`
	Says     string      `json:"says"`   // what happens here, in plain words
	Status   string      `json:"status"` // ok, warn (works, but look at it), fail (the call goes nowhere)
	Branches []SimBranch `json:"branches,omitempty"`
}

// SimBranch is one way on from a step; Taken marks the way this call goes (menus leave it to the caller).
type SimBranch struct {
	When  string   `json:"when"`
	Taken bool     `json:"taken"`
	Step  *SimStep `json:"step"`
}

// SimResult is the whole path.
type SimResult struct {
	Direction string   `json:"direction"` // inbound, internal, outbound, emergency
	At        string   `json:"at"`        // the time used, in the system's time zone
	Start     *SimStep `json:"start"`
	// Ends lists, in plain words, where this call can finish (the taken path's last steps).
	Outcome string `json:"outcome"`
}

type sim struct {
	e     *Engine
	cfg   *config.Config
	in    SimInput
	at    time.Time
	steps int
}

const simMaxSteps = 120

// Simulate traces where a call would go through routes, schedules, scripts, menus and the rest, at a
// given time, without ringing anything. Scripts run (sandboxed, as for a real call) but nothing is logged.
func (e *Engine) Simulate(in SimInput) SimResult {
	cfg := e.Config()
	s := &sim{e: e, cfg: cfg, in: in, at: in.At}
	if s.at.IsZero() {
		s.at = time.Now()
	}
	s.at = s.at.In(cfg.System.Location())
	res := SimResult{At: s.at.Format("Mon 2 Jan 2006 15:04 MST")}
	from, to := cleanNumber(in.From), cleanNumber(in.To)
	if x := cfg.FindExtension(from); x != nil {
		res.Start = s.fromExtension(x, to, &res)
	} else {
		res.Direction = "inbound"
		res.Start = s.inbound(from, to)
	}
	res.Outcome = outcome(res.Start)
	return res
}

func (s *sim) inbound(from, did string) *SimStep {
	cfg := s.cfg
	if em := cfg.System.EmergencyMode; em.On {
		st := &SimStep{Kind: "route", Name: "Emergency mode", Status: "warn",
			Says: "Emergency mode is on: every incoming call goes to one place, whatever the routes say."}
		st.Branches = []SimBranch{{When: "Every call", Taken: true, Step: s.walk(em.Destination, 0, nil, "inbound", from, did)}}
		return st
	}
	r := inboundRoute(cfg, s.in.Trunk, did)
	on := ""
	if s.in.Trunk == "" && (r == nil || r.DID == "") {
		for _, tr := range cfg.Trunks {
			if x := inboundRoute(cfg, tr.ID, did); x != nil && x.Trunk != "" && (r == nil || x.DID != "") {
				r, on = x, tr.Name
				break
			}
		}
	}
	if r == nil {
		return &SimStep{Kind: "refused", Name: "No incoming route", Status: "fail",
			Says: "No incoming route matches " + did + ", so the provider is told the number does not exist. Add one in Call routing."}
	}
	match := "matches " + did
	if r.DID == "" {
		match = "takes every number not matched by another route"
	}
	if on != "" {
		match += " on " + on + " (choose another trunk under Arrives on to see calls arriving elsewhere)"
	}
	st := &SimStep{Kind: "route", Target: r.ID, Name: r.Name, Status: "ok", Says: "The incoming route " + r.Name + " " + match + "."}
	st.Branches = []SimBranch{{When: "Then", Taken: true, Step: s.walk(r.Destination, 0, nil, "inbound", from, did)}}
	return st
}

func (s *sim) fromExtension(x *config.Extension, to string, res *SimResult) *SimStep {
	cfg := s.cfg
	res.Direction = "internal"
	var d *config.Destination
	switch {
	case cfg.FindExtension(to) != nil:
		d = &config.Destination{Type: "extension", Target: to}
	case cfg.FindMenuNumber(to) != nil:
		d = &config.Destination{Type: "menu", Target: cfg.FindMenuNumber(to).ID}
	case cfg.FindScriptNumber(to) != nil:
		d = &config.Destination{Type: "script", Target: cfg.FindScriptNumber(to).ID}
	case cfg.FindQueue(to) != nil:
		d = &config.Destination{Type: "queue", Target: to}
	case cfg.FindRingGroup(to) != nil:
		d = &config.Destination{Type: "ringgroup", Target: to}
	}
	if d != nil {
		return s.walk(*d, 0, nil, "internal", x.Number, to)
	}
	p := s.e.route(cfg, x, to)
	res.Direction = p.direction
	switch {
	case p.reject != 0:
		return &SimStep{Kind: "refused", Name: "Not allowed", Status: "fail", Says: plainRefusal(to, p.reason) + "."}
	case p.local == "pinout" && p.pinThen != nil:
		ps := cfg.FindPINSet(p.box)
		name := p.box
		if ps != nil {
			name = ps.Name
		}
		st := s.outboundStep(to, *p.pinThen)
		st.Says = "The caller is asked for a PIN from " + name + " first. " + st.Says
		return st
	case p.local != "":
		return &SimStep{Kind: "feature", Name: p.toName, Status: "ok", Says: to + " is a feature code: " + p.toName + "."}
	}
	return s.outboundStep(to, p)
}

// outboundStep says which trunks a call out takes.
func (s *sim) outboundStep(to string, p plan) *SimStep {
	var trunks []string
	for _, st := range p.stages {
		for _, t := range st.targets {
			if t.trunk != nil {
				trunks = append(trunks, t.trunk.Name)
			}
		}
	}
	says := "Goes out on " + strings.Join(trunks, ", then ") + "."
	status := "ok"
	if p.direction == "emergency" {
		says = "An emergency call: " + says + " It is never refused for want of a route, and the caller's location goes with it."
	}
	if len(trunks) == 0 {
		says, status = "No trunk can take it.", "fail"
	}
	return &SimStep{Kind: "outbound", Name: "Outside call to " + to, Status: status, Says: says}
}

// forwardStep is where an extension's own forwarding sends a call: inside, or out on a trunk.
func (s *sim) forwardStep(x *config.Extension, n string, depth int, path []string, direction, from, to string) *SimStep {
	cfg := s.cfg
	switch {
	case cfg.FindExtension(n) != nil:
		return s.walk(config.Destination{Type: "extension", Target: n}, depth+1, path, direction, from, to)
	case cfg.FindRingGroup(n) != nil:
		return s.walk(config.Destination{Type: "ringgroup", Target: n}, depth+1, path, direction, from, to)
	case cfg.FindQueue(n) != nil:
		return s.walk(config.Destination{Type: "queue", Target: n}, depth+1, path, direction, from, to)
	case cfg.FindConference(n) != nil:
		return s.walk(config.Destination{Type: "conference", Target: n}, depth+1, path, direction, from, to)
	case cfg.FindDirectoryNumber(n) != nil:
		return s.walk(config.Destination{Type: "directory", Target: cfg.FindDirectoryNumber(n).ID}, depth+1, path, direction, from, to)
	case cfg.FindDialInNumber(n) != nil:
		return s.walk(config.Destination{Type: "dialin", Target: cfg.FindDialInNumber(n).ID}, depth+1, path, direction, from, to)
	}
	if !x.Outbound {
		return &SimStep{Kind: "refused", Name: "Forwarding refused", Status: "fail", Says: x.Name + " may not call outside numbers, so the call is not forwarded to " + n + "."}
	}
	p := s.e.outbound(cfg, x, n)
	if p.reject != 0 || len(p.stages) == 0 {
		return &SimStep{Kind: "refused", Name: "Forwarded to " + n, Status: "fail", Says: "No outgoing route takes " + n + "."}
	}
	var trunks []string
	for _, st := range p.stages {
		for _, t := range st.targets {
			if t.trunk != nil {
				trunks = append(trunks, t.trunk.Name)
			}
		}
	}
	return &SimStep{Kind: "outbound", Name: "Forwarded to " + n, Status: "ok", Says: "Goes out on " + strings.Join(trunks, ", then ") + "."}
}

// walk follows a destination. path holds the places already passed, to spot a loop.
func (s *sim) walk(d config.Destination, depth int, path []string, direction, from, to string) *SimStep {
	s.steps++
	if s.steps > simMaxSteps || depth > 8 {
		return &SimStep{Kind: "loop", Name: "…", Status: "warn", Says: "The plan goes on further than shown here."}
	}
	key := d.Type + ":" + d.Target
	for _, p := range path {
		if p == key {
			return &SimStep{Kind: "loop", Name: "Back to " + s.nameOf(d), Status: "warn", Says: "This goes back to a step the call has already passed."}
		}
	}
	path = append(path, key)
	next := func(dst config.Destination) *SimStep { return s.walk(dst, depth+1, path, direction, from, to) }
	cfg := s.cfg
	missing := func(what string) *SimStep {
		return &SimStep{Kind: "refused", Target: d.Target, Name: "Missing " + what, Status: "fail", Says: "It points at " + what + " " + d.Target + ", which does not exist, so the call fails here."}
	}
	switch d.Type {
	case "extension":
		x := cfg.FindExtension(d.Target)
		if x == nil || !x.Enabled {
			return missing("extension")
		}
		dv := s.e.DivertOf(x.Number)
		if dv.All != "" {
			st := &SimStep{Kind: "extension", Target: x.Number, Name: x.Number + " " + x.Name, Status: "ok",
				Says: x.Name + " forwards every call to " + dv.All + " (set from the phone or the portal)."}
			st.Branches = []SimBranch{{When: "Every call", Taken: true, Step: s.forwardStep(x, dv.All, depth, path, direction, from, to)}}
			return st
		}
		if a, away := s.e.awayNow(x.Number); away {
			if dest, ok := whileAway(x, a); ok {
				st := &SimStep{Kind: "extension", Target: x.Number, Name: x.Number + " " + x.Name, Status: "ok",
					Says: x.Name + " is away (" + a.Reason + "), so it does not ring."}
				var step *SimStep
				if dest.Type == "number" {
					step = s.forwardStep(x, dest.Target, depth, path, direction, from, to)
				} else {
					step = next(dest)
				}
				st.Branches = []SimBranch{{When: "While away", Taken: true, Step: step}}
				return st
			}
		}
		phones := len(s.e.reg.contacts(x.Number))
		st := &SimStep{Kind: "extension", Target: x.Number, Name: x.Number + " " + x.Name, Status: "ok"}
		defer func() {
			if s.e.followMeOn(x) {
				st.Says += " Follow-me then rings " + strings.Join(x.FollowMe.Numbers, ", ") + " (" + x.FollowMe.Strategy + ")."
			}
			if dv.Busy != "" {
				st.Says += " While " + x.Name + " is on a call, calls go to " + dv.Busy + "."
			}
			if dv.NoAnswer != "" {
				st.Branches = []SimBranch{{When: "If nobody answers", Step: s.forwardStep(x, dv.NoAnswer, depth, path, direction, from, to)}}
			}
		}()
		ringing := phones > 0 || s.in.AssumePhones || s.e.hasApps(x.Number)
		if ringing {
			st.Says = fmt.Sprintf("Rings %s for %d seconds.", x.Name, x.RingSeconds)
			if phones == 0 && !s.e.hasApps(x.Number) {
				st.Says += " (No phone is connected right now.)"
			}
		} else {
			st.Says, st.Status = "No phone is connected to "+x.Name+", so it does not ring.", "warn"
		}
		if x.NoAnswer != nil {
			st.Branches = []SimBranch{{When: noAnswerWhen(ringing), Taken: !ringing, Step: next(*x.NoAnswer)}}
		} else if !ringing {
			st.Status = "fail"
		} else {
			st.Says += " If nobody answers, the call ends."
		}
		return st
	case "ringgroup":
		g := cfg.FindRingGroup(d.Target)
		if g == nil {
			return missing("ring group")
		}
		var on, off []string
		for _, m := range g.Members {
			x := cfg.FindExtension(m)
			if x == nil || !x.Enabled {
				continue
			}
			if len(s.e.reg.contacts(m)) > 0 || s.in.AssumePhones || s.e.hasApps(m) {
				on = append(on, x.Name)
			} else {
				off = append(off, x.Name)
			}
		}
		how := "at once"
		if g.Strategy == "sequential" {
			how = "one after another"
		}
		st := &SimStep{Kind: "ringgroup", Target: g.Number, Name: g.Number + " " + g.Name, Status: "ok"}
		if len(on) > 0 {
			st.Says = fmt.Sprintf("Rings %s %s, for %d seconds.", strings.Join(on, ", "), how, g.RingSeconds)
		} else {
			st.Says, st.Status = "Nobody in "+g.Name+" has a phone connected, so nothing rings.", "warn"
		}
		if len(off) > 0 {
			st.Says += " Not ringing (no phone connected): " + strings.Join(off, ", ") + "."
		}
		if g.NoAnswer != nil {
			st.Branches = []SimBranch{{When: noAnswerWhen(len(on) > 0), Taken: len(on) == 0, Step: next(*g.NoAnswer)}}
		} else if len(on) == 0 {
			st.Status = "fail"
		}
		return st
	case "queue":
		q := cfg.FindQueue(d.Target)
		if q == nil {
			return missing("queue")
		}
		st := &SimStep{Kind: "queue", Target: q.Number, Name: q.Number + " " + q.Name, Status: "ok",
			Says: fmt.Sprintf("Waits in the queue for the next free agent (%d agents).", len(q.Agents))}
		if len(q.Agents) == 0 {
			st.Status, st.Says = "warn", "Waits in the queue, but it has no agents."
		}
		if q.Timeout != nil && q.MaxWaitSeconds > 0 {
			st.Branches = []SimBranch{{When: fmt.Sprintf("After waiting %d seconds", q.MaxWaitSeconds), Step: next(*q.Timeout)}}
		}
		return st
	case "menu":
		m := cfg.FindMenu(d.Target)
		if m == nil {
			return missing("menu")
		}
		st := &SimStep{Kind: "menu", Target: m.ID, Name: m.Name, Status: "ok", Says: "Plays the menu and waits for the caller to press a key."}
		if m.DialExtensions {
			st.Says += " Callers can also type an extension number."
		}
		for _, o := range m.Options {
			label := "Press " + o.Key
			if o.Label != "" {
				label += " (" + o.Label + ")"
			}
			st.Branches = append(st.Branches, SimBranch{When: label, Step: next(o.Destination)})
		}
		wait := fmt.Sprintf("No key pressed (after %d seconds, %d times)", m.TimeoutSeconds, max(1, m.Repeats))
		if m.Timeout != nil {
			st.Branches = append(st.Branches, SimBranch{When: wait, Step: next(*m.Timeout)})
		} else {
			st.Branches = append(st.Branches, SimBranch{When: wait, Step: &SimStep{Kind: "hangup", Name: "Goodbye", Status: "ok", Says: "The caller hears goodbye and the call ends."}})
		}
		return st
	case "schedule":
		sc := cfg.FindSchedule(d.Target)
		if sc == nil {
			return missing("schedule")
		}
		open := sc.IsOpen(s.at)
		state := "closed"
		if open {
			state = "open"
		}
		st := &SimStep{Kind: "schedule", Target: sc.ID, Name: sc.Name, Status: "ok", Says: "At " + s.at.Format("Mon 15:04") + " " + sc.Name + " is " + state + "."}
		st.Branches = []SimBranch{
			{When: "Open", Taken: open, Step: next(sc.Open)},
			{When: "Closed", Taken: !open, Step: next(sc.Closed)},
		}
		return st
	case "audiosocket":
		as := cfg.FindAudioStream(d.Target)
		if as == nil {
			return missing("audio stream")
		}
		st := &SimStep{Kind: "audiosocket", Target: as.ID, Name: as.Name, Status: "ok", Says: "BitVoice answers and streams the call to the bot at " + as.Address + " (AudioSocket)."}
		st.Branches = []SimBranch{{When: "The bot cannot take it", Step: next(as.Fallback)}}
		return st
	case "toggle":
		t := cfg.FindToggle(d.Target)
		if t == nil {
			return missing("toggle")
		}
		over := s.e.ToggleOn(t.ID)
		state := "normal"
		if over {
			state = "switched over"
		}
		st := &SimStep{Kind: "toggle", Target: t.ID, Name: t.Name, Status: "ok", Says: t.Name + " is " + state + " now (" + cfg.System.Codes.Toggle + t.Code + " switches it)."}
		st.Branches = []SimBranch{
			{When: "Normal", Taken: !over, Step: next(t.Normal)},
			{When: "Switched over", Taken: over, Step: next(t.Override)},
		}
		return st
	case "script":
		sc := cfg.FindScript(d.Target)
		if sc == nil {
			return missing("script")
		}
		in := script.Call{From: from, To: to, Direction: direction, Weekday: int(s.at.Weekday()), Hour: s.at.Hour(), Minute: s.at.Minute(), Date: s.at.Format("2006-01-02")}
		if sc.Mode == "call" {
			// A call script runs the call: what it does depends on the keys the caller presses, so the
			// simulator runs it pressing nothing, with nobody answering, and says what it did.
			sim := &script.Simulator{Exists: func(d script.Destination) bool {
				if d.Type == "number" {
					if cfg.FindExtension(d.Target) != nil || cfg.FindRingGroup(d.Target) != nil || cfg.FindQueue(d.Target) != nil {
						return true
					}
					for _, r := range cfg.OutboundRoutes { // an outside number: some route must take it
						for _, pat := range r.Patterns {
							if config.MatchPattern(pat, d.Target) {
								return true
							}
						}
					}
					return false
				}
				return s.exists(config.Destination{Type: d.Type, Target: d.Target})
			}}
			out, err := script.RunCall(sc.Language, sc.Source, in, sim, 5*time.Second)
			st := &SimStep{Kind: "script", Target: sc.ID, Name: sc.Name, Status: "ok",
				Says: "A call script answers and runs the call. Pressing nothing, with nobody answering, it does: " + strings.Join(out.Steps, ", ") + "."}
			if len(out.Steps) == 0 {
				st.Says = "A call script answers and runs the call; pressing nothing, it takes no step."
			}
			fallback := err != nil || (!out.Ended && out.Next == nil)
			switch {
			case err != nil:
				st.Status, st.Says = "warn", st.Says+" Then it fails ("+err.Error()+"), so its fallback is used."
			case out.Next != nil:
				d := config.Destination{Type: out.Next.Type, Target: out.Next.Target}
				if d.Type == "number" {
					d.Type = "extension"
					if cfg.FindRingGroup(d.Target) != nil {
						d.Type = "ringgroup"
					} else if cfg.FindQueue(d.Target) != nil {
						d.Type = "queue"
					}
				}
				st.Branches = append(st.Branches, SimBranch{When: "Its choice", Taken: true, Step: next(d)})
			case !out.Ended:
				st.Says += " Then it ends without hanging up or handing the call on, so its fallback is used."
			}
			st.Branches = append(st.Branches, SimBranch{When: "Fallback", Taken: fallback, Step: next(sc.Fallback)})
			return st
		}
		out, err := script.Route(sc.Language, sc.Source, in)
		st := &SimStep{Kind: "script", Target: sc.ID, Name: sc.Name, Status: "ok"}
		chosen := config.Destination{Type: out.Type, Target: out.Target}
		ok := err == nil && out.Type != "" && out.Type != "script" && s.exists(chosen)
		switch {
		case err != nil:
			st.Status, st.Says = "warn", "The script failed ("+err.Error()+"), so its fallback is used."
		case out.Type == "" || out.Type == "script":
			st.Says = "The script chose nothing, so its fallback is used."
		case !ok:
			st.Status, st.Says = "warn", "The script chose "+s.nameOf(chosen)+", which does not exist, so its fallback is used."
		default:
			st.Says = "The script chose " + s.nameOf(chosen) + "."
		}
		if len(out.Log) > 0 {
			st.Says += " It logged: " + strings.Join(out.Log, "; ")
		}
		if ok {
			st.Branches = append(st.Branches, SimBranch{When: "Its choice", Taken: true, Step: next(chosen)})
		}
		st.Branches = append(st.Branches, SimBranch{When: "Fallback", Taken: !ok, Step: next(sc.Fallback)})
		return st
	case "voicemail":
		x := cfg.FindExtension(d.Target)
		if x == nil || !x.Voicemail.Enabled {
			return &SimStep{Kind: "refused", Target: d.Target, Name: "No voicemail " + d.Target, Status: "fail", Says: "Voicemail " + d.Target + " is not switched on, so the call fails here."}
		}
		return &SimStep{Kind: "voicemail", Target: x.Number, Name: "Voicemail " + x.Name, Status: "ok", Says: "The caller hears the greeting and leaves a message for " + x.Name + "."}
	case "fax":
		f := cfg.FindFax(d.Target)
		if f == nil {
			return missing("fax")
		}
		st := &SimStep{Kind: "fax", Target: f.Number, Name: "Fax " + f.Name, Status: "ok",
			Says: "BitVoice answers as a fax machine and receives the fax over T.38; it is kept on the Faxes page"}
		if len(f.Email) > 0 {
			st.Says += " and emailed to " + strings.Join(f.Email, ", ")
		}
		st.Says += "."
		return st
	case "receptionist":
		r := cfg.FindReceptionist(d.Target)
		if r == nil {
			return missing("receptionist")
		}
		a := cfg.System.AI
		st := &SimStep{Kind: "receptionist", Target: r.ID, Name: r.Name, Status: "ok",
			Says: "The AI receptionist answers, says it is automated, answers from its approved information, takes messages and books callbacks."}
		person := next(r.Person)
		if !a.Enabled || a.SpeechModel == "" {
			st.Status, st.Says = "warn", "AI or its speech model is off, so the receptionist hands every call straight to a person."
			st.Branches = []SimBranch{{When: "Every call", Taken: true, Step: person}}
			return st
		}
		st.Branches = []SimBranch{{When: "Caller presses 0, asks for a person, or the AI fails", Step: person}}
		return st
	case "conference":
		r := cfg.FindConference(d.Target)
		if r == nil {
			return missing("conference room")
		}
		says := "The caller joins the conference room " + r.Name + "."
		if r.PIN != "" || r.LeaderPIN != "" {
			says = "The caller is asked for the PIN, then joins the conference room " + r.Name + "."
		}
		return &SimStep{Kind: "conference", Target: r.Number, Name: "Conference " + r.Name, Status: "ok", Says: says}
	case "directory":
		r := cfg.FindDirectory(d.Target)
		if r == nil {
			return missing("directory")
		}
		part := map[string]string{"first": "first name", "any": "name", "last": "last name"}[r.Match]
		return &SimStep{Kind: "directory", Target: r.ID, Name: "Directory " + r.Name, Status: "ok",
			Says: "The caller spells the first letters of a " + part + " and is put through to who they choose."}
	case "dialin":
		r := cfg.FindDialIn(d.Target)
		if r == nil {
			return missing("dial-in")
		}
		return &SimStep{Kind: "dialin", Target: r.ID, Name: "Dial-in " + r.Name, Status: "ok",
			Says: "The caller proves who they are (PIN or known number), then dials a number and is put through as extension " + r.As + "."}
	case "echo":
		return &SimStep{Kind: "echo", Name: "Echo test", Status: "ok", Says: "The caller hears themselves, to test the sound."}
	case "hangup":
		return &SimStep{Kind: "hangup", Name: "Hang up", Status: "ok", Says: "The call ends."}
	case "":
		return &SimStep{Kind: "refused", Name: "Nothing chosen", Status: "fail", Says: "No destination is set here, so the call fails."}
	}
	return &SimStep{Kind: "refused", Name: d.Type, Status: "fail", Says: "BitVoice does not know this kind of destination."}
}

func noAnswerWhen(ringing bool) string {
	if ringing {
		return "If nobody answers"
	}
	return "Since nothing rings"
}

// exists says whether a destination a script chose is there.
func (s *sim) exists(d config.Destination) bool { return s.cfg.DestinationExists(d) }

// nameOf names a destination in words.
func (s *sim) nameOf(d config.Destination) string {
	c := s.cfg
	switch d.Type {
	case "extension":
		if x := c.FindExtension(d.Target); x != nil {
			return "extension " + x.Number + " " + x.Name
		}
	case "voicemail":
		return "voicemail " + d.Target
	case "fax":
		if f := c.FindFax(d.Target); f != nil {
			return "fax " + f.Name
		}
	case "ringgroup":
		if g := c.FindRingGroup(d.Target); g != nil {
			return "ring group " + g.Name
		}
	case "queue":
		if q := c.FindQueue(d.Target); q != nil {
			return "queue " + q.Name
		}
	case "menu":
		if m := c.FindMenu(d.Target); m != nil {
			return "menu " + m.Name
		}
	case "schedule":
		if x := c.FindSchedule(d.Target); x != nil {
			return "schedule " + x.Name
		}
	case "toggle":
		if x := c.FindToggle(d.Target); x != nil {
			return "toggle " + x.Name
		}
	case "audiosocket":
		if x := c.FindAudioStream(d.Target); x != nil {
			return "audio stream " + x.Name
		}
	case "script":
		if x := c.FindScript(d.Target); x != nil {
			return "script " + x.Name
		}
	case "receptionist":
		if r := c.FindReceptionist(d.Target); r != nil {
			return "receptionist " + r.Name
		}
	case "conference":
		if r := c.FindConference(d.Target); r != nil {
			return "conference room " + r.Name
		}
	}
	return strings.TrimSpace(d.Type + " " + d.Target)
}

// outcome follows the taken branches to where this call ends up, and says it.
func outcome(st *SimStep) string {
	for st != nil {
		var next *SimStep
		for _, b := range st.Branches {
			if b.Taken {
				next = b.Step
				break
			}
		}
		if next == nil {
			switch {
			case st.Kind == "menu":
				return "The caller chooses in " + st.Name + "."
			case st.Status == "fail":
				return "Fails: " + st.Says
			}
			return st.Says
		}
		st = next
	}
	return ""
}
