package pbx

import (
	"context"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A plan is how a call will be offered: stages of targets rung together, each stage for a time, then
// somewhere to go if nobody answers.
type plan struct {
	callerExt string // for a call out passed on by another server: whose caller ID it carries
	intercom  bool   // a page passed on by another server: ring with auto-answer
	stages    []stage
	noAnswer  *config.Destination
	local     string // calls BitVoice answers itself: echo, voicemail (leave one), messages (hear them)
	box       string // the mailbox, for voicemail and messages
	reject    int    // a SIP code to refuse with, when nothing can be rung
	reason    string
	toName    string
	direction string
	trunk     string
	// pinSet is the PIN set the outbound route asks for; pinThen the call that goes out after the PIN.
	pinSet  string
	pinThen *plan
}

type stage struct {
	targets []target
	// wake lists extensions whose mobile apps are woken; the stage then rings its whole time, so an app
	// that connects late is rung too.
	wake     []waking
	timeout  time.Duration
	failover bool // trunk stages: a failure moves on to the next trunk
}

type target struct {
	viaTrunk string // a call out on this trunk, placed by the server (binding.node) holding it
	ext      string
	name     string
	binding  *Binding
	trunk    *config.Trunk
	number   string // what to send to a trunk
	confirm  bool   // follow-me to an outside number: whoever answers presses 1 to take the call
	// divertFrom and divertReason: a call forwarded out from an extension, sent as Diversion (RFC 5806)
	// so the carrier, or a PRI as its redirecting number, knows whose call it is.
	divertFrom, divertReason string
}

// resolve turns a destination into a plan. depth limits no-answer chains so a loop cannot ring forever.
func (e *Engine) resolve(cfg *config.Config, d config.Destination, depth int) plan {
	return e.resolveFor(cfg, d, depth, scriptCall{})
}

// resolveFor is resolve for a known call, which routing scripts are told about.
func (e *Engine) resolveFor(cfg *config.Config, d config.Destination, depth int, sc scriptCall) plan {
	if depth > 4 {
		return plan{reject: 482, reason: "Loop Detected"}
	}
	switch d.Type {
	case "receptionist":
		r := cfg.FindReceptionist(d.Target)
		if r == nil {
			return plan{reject: 404}
		}
		return plan{local: "receptionist", box: r.ID, toName: r.Name}
	case "echo":
		return plan{local: "echo", toName: "Echo test", direction: "test"}
	case "forward":
		// An extension's own forwarding when nobody answers (set with *52), never in the configuration.
		x := cfg.FindExtension(d.Via)
		if x == nil {
			return plan{reject: 404}
		}
		return diverted(e.forwardPlan(cfg, x, d.Target, depth, sc), x, "no-answer")
	case "conference":
		r := cfg.FindConference(d.Target)
		if r == nil {
			return plan{reject: 404}
		}
		return plan{local: "conference", box: r.Number, toName: r.Name}
	case "directory":
		r := cfg.FindDirectory(d.Target)
		if r == nil {
			return plan{reject: 404}
		}
		return plan{local: "directory", box: r.ID, toName: r.Name}
	case "dialin":
		r := cfg.FindDialIn(d.Target)
		if r == nil {
			return plan{reject: 404}
		}
		return plan{local: "dialin", box: r.ID, toName: r.Name}
	case "hangup":
		return plan{reject: 480, reason: "Temporarily Unavailable"}
	case "script":
		s := cfg.FindScript(d.Target)
		if s == nil {
			return plan{reject: 404}
		}
		return e.runScript(cfg, s, depth, sc)
	case "menu":
		m := cfg.FindMenu(d.Target)
		if m == nil {
			return plan{reject: 404}
		}
		return plan{local: "menu", box: m.ID, toName: m.Name}
	case "schedule":
		s := cfg.FindSchedule(d.Target)
		if s == nil {
			return plan{reject: 404}
		}
		next := s.Closed
		if s.IsOpen(time.Now().In(cfg.System.Location())) {
			next = s.Open
		}
		return e.resolveFor(cfg, next, depth+1, sc)
	case "audiosocket":
		s := cfg.FindAudioStream(d.Target)
		if s == nil {
			return plan{reject: 404}
		}
		return plan{local: "audiosocket", box: s.ID, toName: s.Name}
	case "toggle":
		t := cfg.FindToggle(d.Target)
		if t == nil {
			return plan{reject: 404}
		}
		next := t.Normal
		if e.ToggleOn(t.ID) {
			next = t.Override
		}
		return e.resolveFor(cfg, next, depth+1, sc)
	case "esrp":
		return plan{local: "esrp", toName: "ESRP"} // routed by the caller's location, in newCall
	case "fax":
		f := cfg.FindFax(d.Target)
		if f == nil {
			return plan{reject: 404}
		}
		return plan{local: "fax", box: f.Number, toName: "Fax " + f.Name}
	case "queue":
		q := cfg.FindQueue(d.Target)
		if q == nil {
			return plan{reject: 404}
		}
		return plan{local: "queue", box: q.Number, toName: q.Name}
	case "voicemail":
		x := cfg.FindExtension(d.Target)
		if x == nil || !x.Voicemail.Enabled {
			return plan{reject: 480, reason: "No Voicemail"}
		}
		return plan{local: "voicemail", box: x.Number, toName: "Voicemail " + x.Name}
	case "extension":
		x := cfg.FindExtension(d.Target)
		if x == nil || !x.Enabled {
			return plan{reject: 404}
		}
		dv := e.DivertOf(x.Number)
		if dv.All != "" && depth < 4 {
			return diverted(e.forwardPlan(cfg, x, dv.All, depth, sc), x, "unconditional")
		}
		if a, away := e.awayNow(x.Number); away && depth < 8 {
			if to, ok := whileAway(x, a); ok {
				if to.Type == "number" {
					if cfg.FindExtension(to.Target) != nil {
						return e.resolveFor(cfg, config.Destination{Type: "extension", Target: to.Target}, depth+1, sc)
					}
					if x.Outbound { // forwarded to a mobile, through the outbound routes
						p := e.outbound(cfg, x, to.Target)
						p.toName = x.Name + " (forwarded)"
						if p.reject == 0 {
							return p
						}
					}
				} else {
					return e.resolveFor(cfg, to, depth+1, sc)
				}
			}
		}
		if dv.Busy != "" && depth < 4 && e.onCall(x.Number) {
			return diverted(e.forwardPlan(cfg, x, dv.Busy, depth, sc), x, "user-busy")
		}
		p := plan{toName: x.Name, noAnswer: x.NoAnswer}
		if dv.NoAnswer != "" && depth < 4 {
			p.noAnswer = &config.Destination{Type: "forward", Target: dv.NoAnswer, Via: x.Number}
		}
		var ts []target
		for _, b := range e.reg.contacts(x.Number) {
			ts = append(ts, target{ext: x.Number, name: x.Name, binding: b})
		}
		var follow []stage
		if e.followMeOn(x) && depth < 4 {
			follow = e.followMeStages(cfg, x, depth)
		}
		if e.hasApps(x.Number) {
			p.stages = append([]stage{{targets: ts, timeout: time.Duration(x.RingSeconds) * time.Second, wake: []waking{{x.Number, x.Name}}}}, follow...)
			return p
		}
		if len(ts) == 0 && len(follow) == 0 {
			if p.noAnswer != nil {
				return e.resolveFor(cfg, *p.noAnswer, depth+1, sc)
			}
			return plan{reject: 480, reason: "Not Registered", toName: x.Name}
		}
		if len(ts) > 0 {
			p.stages = []stage{{targets: ts, timeout: time.Duration(x.RingSeconds) * time.Second}}
		}
		p.stages = append(p.stages, follow...)
		return p
	case "ringgroup":
		g := cfg.FindRingGroup(d.Target)
		if g == nil {
			return plan{reject: 404}
		}
		p := plan{toName: g.Name, noAnswer: g.NoAnswer}
		var all []target
		var allWake []waking
		for _, m := range g.Members {
			x := cfg.FindExtension(m)
			if x == nil || !x.Enabled {
				continue
			}
			if _, away := e.awayNow(m); away {
				continue // in a meeting or off: the group rings the others
			}
			var ts []target
			for _, b := range e.reg.contacts(m) {
				ts = append(ts, target{ext: m, name: x.Name, binding: b})
			}
			apps := e.hasApps(m)
			if len(ts) == 0 && !apps {
				continue
			}
			var w []waking
			if apps {
				w = []waking{{m, x.Name}}
			}
			if g.Strategy == "sequential" {
				p.stages = append(p.stages, stage{targets: ts, timeout: time.Duration(g.RingSeconds) * time.Second, wake: w})
			} else {
				all = append(all, ts...)
				allWake = append(allWake, w...)
			}
		}
		if len(all) > 0 || len(allWake) > 0 {
			p.stages = []stage{{targets: all, timeout: time.Duration(g.RingSeconds) * time.Second, wake: allWake}}
		}
		if len(p.stages) == 0 {
			if g.NoAnswer != nil {
				return e.resolveFor(cfg, *g.NoAnswer, depth+1, sc)
			}
			return plan{reject: 480, reason: "Nobody Available", toName: g.Name}
		}
		return p
	}
	return plan{reject: 404}
}

// route decides where a number dialled from an extension goes.
func (e *Engine) route(cfg *config.Config, caller *config.Extension, dialled string) plan {
	dialled = cleanNumber(dialled)
	if em := cfg.System.EmergencyMode; em.Code != "" && dialled == em.Code {
		if caller == nil || !slices.Contains(em.Allowed, caller.Number) {
			return plan{reject: 403, reason: "Not Allowed To Switch Emergency Mode"}
		}
		return plan{local: "emode", toName: "Emergency mode", direction: "internal"}
	}
	if caller != nil && (dialled == cfg.System.AgentLogin || dialled == cfg.System.AgentPause) {
		return plan{local: "agent", box: dialled, toName: "Agent", direction: "internal"}
	}
	if c := cfg.System.Radio.Console; c != "" && dialled == c {
		return plan{local: "console", toName: "Radio console", direction: "internal"}
	}
	if caller != nil {
		if p, ok := e.featureCode(cfg, caller, dialled); ok {
			if p.direction == "" {
				p.direction = "internal"
			}
			return p
		}
	}
	if dialled == cfg.System.EchoNumber {
		return plan{local: "echo", toName: "Echo test", direction: "test"}
	}
	if slices.Contains(parkSlots(cfg), dialled) {
		if e.Cluster != nil && e.parkedHere(dialled) == nil {
			if node := e.Cluster.ParkedAt(dialled); node != "" {
				// The call waits on another server: pick it up there.
				return plan{toName: "Parked call", direction: "internal", stages: []stage{{targets: []target{remoteTarget(node, dialled, "Parked call")}, timeout: time.Minute}}}
			}
		}
		return plan{local: "pickup", box: dialled, toName: "Parked call", direction: "internal"}
	}
	if dialled == cfg.System.VoicemailNumber && cfg.System.VoicemailNumber != "" {
		if caller == nil || !caller.Voicemail.Enabled {
			return plan{reject: 404, reason: "No Voicemail For This Extension"}
		}
		return plan{local: "messages", box: caller.Number, toName: "Voicemail", direction: "internal"}
	}
	if slices.Contains(cfg.System.EmergencyNumbers, dialled) {
		p := e.outbound(cfg, caller, dialled)
		if p.reject != 0 {
			// An emergency call must never fail for want of a matching pattern (the numbers were changed
			// and the routes were not, say): send it as dialled on every enabled trunk in turn.
			p = emergencyFallback(cfg, dialled)
		}
		p.direction = "emergency"
		e.lastEmergency.Store(time.Now().Unix())
		if p.reject != 0 {
			p.reason = "No Trunk For Emergency Calls"
			p.reject = 503
		}
		return p
	}
	if cfg.FindExtension(dialled) != nil {
		p := e.resolve(cfg, config.Destination{Type: "extension", Target: dialled}, 0)
		p.direction = "internal"
		return p
	}
	if r := cfg.FindConference(dialled); r != nil {
		return plan{local: "conference", box: r.Number, toName: r.Name, direction: "internal"}
	}
	if r := cfg.FindDirectoryNumber(dialled); r != nil {
		return plan{local: "directory", box: r.ID, toName: r.Name, direction: "internal"}
	}
	if r := cfg.FindAudioStreamNumber(dialled); r != nil {
		return plan{local: "audiosocket", box: r.ID, toName: r.Name, direction: "internal"}
	}
	if r := cfg.FindDialInNumber(dialled); r != nil {
		return plan{local: "dialin", box: r.ID, toName: r.Name, direction: "internal"}
	}
	if g := cfg.FindPageGroup(dialled); g != nil {
		return plan{local: "page", box: g.Number, toName: "Page " + g.Name, direction: "internal"}
	}
	if m := cfg.FindMenuNumber(dialled); m != nil {
		p := e.resolve(cfg, config.Destination{Type: "menu", Target: m.ID}, 0)
		p.direction = "internal"
		return p
	}
	if cfg.FindQueue(dialled) != nil {
		p := e.resolve(cfg, config.Destination{Type: "queue", Target: dialled}, 0)
		p.direction = "internal"
		return p
	}
	if cfg.FindRingGroup(dialled) != nil {
		p := e.resolve(cfg, config.Destination{Type: "ringgroup", Target: dialled}, 0)
		p.direction = "internal"
		return p
	}
	if caller != nil && !caller.Outbound {
		return plan{reject: 403, reason: "Outbound Calls Not Allowed", direction: "outbound"}
	}
	if e.vacantBarred(cfg, caller) {
		return plan{reject: 403, reason: "Room Not Checked In", direction: "outbound"}
	}
	p := e.outbound(cfg, caller, dialled)
	p.direction = "outbound"
	if p.pinSet != "" && p.reject == 0 {
		// The route asks for a PIN: BitVoice answers, asks for it, then places the call.
		then := p
		then.pinSet = ""
		return plan{local: "pinout", box: p.pinSet, toName: "PIN for " + dialled, direction: "outbound", trunk: p.trunk, pinThen: &then}
	}
	return p
}

// outbound finds the first outbound route matching a number and offers the call to its trunks in order.
func (e *Engine) outbound(cfg *config.Config, caller *config.Extension, dialled string) plan {
	routes := cfg.OutboundRoutes
	if slices.Contains(cfg.System.EmergencyNumbers, dialled) {
		// A route that names the emergency number exactly wins over a broad pattern (9.) placed above it.
		routes = slices.Clone(routes)
		slices.SortStableFunc(routes, func(a, b config.OutboundRoute) int {
			return boolInt(!slices.Contains(a.Patterns, dialled)) - boolInt(!slices.Contains(b.Patterns, dialled))
		})
	}
	for _, r := range routes {
		matched := false
		for _, pat := range r.Patterns {
			if config.MatchPattern(pat, dialled) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		number := dialled
		if r.Strip > 0 && r.Strip < len(number) {
			number = number[r.Strip:]
		}
		number = r.Prepend + number
		if cfg.Security.BlockPremium && isPremium(number) && !slices.Contains(cfg.System.EmergencyNumbers, dialled) {
			return plan{reject: 403, reason: "Premium Number Blocked"}
		}
		var p plan
		for _, id := range r.Trunks {
			t := cfg.FindTrunk(id)
			if t == nil || !t.Enabled {
				continue
			}
			hosts := []string{t.Host}
			if t.Teams && slicesContainsFold(TeamsHubs, t.Host) {
				hosts = TeamsHubs // Microsoft's three proxies, in the order Microsoft asks for
			}
			p.trunk = t.Name
			if node := e.heldElsewhere(t); node != "" {
				if t.MaxChannels > 0 && e.trunkChannels(t.ID) >= t.MaxChannels {
					continue // every channel is in use, on one server or another: the next trunk
				}
				via := stage{targets: []target{trunkTarget(node, t, number)}, timeout: 3 * time.Minute, failover: true}
				if slices.Contains(cfg.System.EmergencyNumbers, dialled) {
					// An emergency call tries this server's own way out first: one fewer thing to fail.
					tc := *t
					p.stages = append(p.stages, stage{targets: []target{{trunk: &tc, number: number, name: number}}, timeout: 3 * time.Minute, failover: true})
				}
				p.stages = append(p.stages, via)
				continue
			}
			for _, h := range hosts {
				tc := *t
				tc.Host = h
				p.stages = append(p.stages, stage{targets: []target{{trunk: &tc, number: number, name: number}},
					timeout: 3 * time.Minute, failover: true})
			}
		}
		if len(p.stages) == 0 {
			return plan{reject: 503, reason: "No Trunk Available"}
		}
		p.toName = dialled
		if !slices.Contains(cfg.System.EmergencyNumbers, dialled) {
			p.pinSet = r.PINSet
		}
		return p
	}
	return plan{reject: 404, reason: "No Route"}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// emergencyFallback tries every enabled trunk, those of the outbound routes first, with the number as dialled.
func emergencyFallback(cfg *config.Config, dialled string) plan {
	var p plan
	seen := map[string]bool{}
	try := func(t *config.Trunk) {
		if t == nil || !t.Enabled || seen[t.ID] {
			return
		}
		seen[t.ID] = true
		tc := *t
		p.stages = append(p.stages, stage{targets: []target{{trunk: &tc, number: dialled, name: dialled}},
			timeout: 3 * time.Minute, failover: true})
		if p.trunk == "" {
			p.trunk = t.Name
		}
	}
	for _, r := range cfg.OutboundRoutes {
		for _, id := range r.Trunks {
			try(cfg.FindTrunk(id))
		}
	}
	for i := range cfg.Trunks {
		try(&cfg.Trunks[i])
	}
	if len(p.stages) == 0 {
		return plan{reject: 503}
	}
	p.toName = dialled
	return p
}

// isPremium catches the North American premium and high-fraud prefixes that toll fraud aims for.
func isPremium(n string) bool {
	n = strings.TrimPrefix(n, "+")
	n = strings.TrimPrefix(n, "011")
	for _, p := range []string{"1900", "1976", "900", "976"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// newCall handles an INVITE that starts a new dialog.
func (e *Engine) newCall(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	cfg := e.Config()
	trying := sip.NewResponse(m, 100, "")
	tx.Respond(trying)

	from, err := sip.ParseAddress(m.Get("From"))
	if err != nil || from.Tag() == "" {
		tx.Respond(sip.NewResponse(m, 400, "Bad From"))
		return
	}
	var offer *sdp.Session
	// An NG911 call brings its location and Additional Data with it: a multipart body, the SDP one
	// part of it, and a Geolocation header.
	var emergencyData *i3.Call
	if m.Get("Geolocation") != "" || strings.HasPrefix(strings.ToLower(m.Get("Content-Type")), "multipart/") ||
		strings.HasPrefix(strings.ToLower(m.RequestURI), "urn:service:sos") {
		d := i3.FromInvite(m, m.RequestURI, m.Get("Content-Type"), m.Body)
		emergencyData = &d
	}
	sdpBody := m.Body
	if emergencyData != nil {
		sdpBody = emergencyData.SDP
	}
	if len(sdpBody) > 0 {
		offer, err = sdp.Parse(sdpBody)
		if err != nil || offer.Audio() == nil && !hasMSRP(offer) { // text to 911 comes with no sound
			tx.Respond(sip.NewResponse(m, 488, "No Audio"))
			return
		}
		if !offerAcceptable(offer) {
			tx.Respond(sip.NewResponse(m, 488, "No Common SRTP Suite"))
			return
		}
	}

	c := &Call{e: e, ID: config.NewID(), start: time.Now(), answered: make(chan struct{}),
		cancelled: make(chan struct{}), results: make(chan forkResult, 64)}
	if emergencyData != nil && (emergencyData.Location != nil || emergencyData.LocationURI != "" || !emergencyData.Additional.Empty()) {
		c.i3 = emergencyData
	}
	var p plan
	var inTrunk string // the ID of the trunk an inbound call arrived on
	ip := in.From.Addr.Addr()

	if m.Get(nodeHeader) != "" && e.Cluster != nil {
		// A call another server in the cluster sends for one of this server's phones.
		cp, err := e.clusterCall(cfg, m, ip)
		if err != nil {
			e.Log.Warn("refused a call from another server", "from", in.From.Addr.String(), "err", err)
			tx.Respond(sip.NewResponse(m, 403, "Not A Cluster Member"))
			return
		}
		if cp.reject != 0 {
			tx.Respond(sip.NewResponse(m, cp.reject, cp.reason))
			return
		}
		p = cp
		c.from, c.fromName = cleanNumber(from.URI.User), from.Display
		c.to = cleanNumber(userOf(m.RequestURI))
		if x := cfg.FindExtension(cp.callerExt); x != nil {
			c.callerExt = x.Number // its caller ID goes out on the trunk
		}
	} else if trunk := e.trunkFor(cfg, in.From.Addr); trunk != nil && !e.reg.registeredFrom(in.From.Addr) {
		if isIAX(trunk) && (e.iax() == nil || m.Get(IAXTokenHeader) != e.iax().Token()) {
			tx.Respond(sip.NewResponse(m, 403, "Not The IAX2 Gateway"))
			return
		}
		did := cleanNumber(userOf(m.Get("To")))
		if r := userOf(m.RequestURI); r != "" && !strings.EqualFold(r, trunk.Username) {
			did = cleanNumber(r)
		}
		route := inboundRoute(cfg, trunk.ID, did)
		if trunk.Teams && route == nil {
			// A Teams user dialling an extension: Teams sends it as dialled (with a dial plan rule
			// that leaves short numbers alone), with or without a +.
			if x := cfg.FindExtension(strings.TrimPrefix(did, "+")); x != nil {
				route = &config.InboundRoute{Name: "Teams to extension", Destination: config.Destination{Type: "extension", Target: x.Number}}
			}
		}
		if em := cfg.System.EmergencyMode; em.On {
			// Emergency mode: every incoming call goes to one place, whatever the routes say.
			route = &config.InboundRoute{Name: "Emergency mode", Destination: em.Destination}
		}
		if route == nil {
			tx.Respond(sip.NewResponse(m, 404, "No Inbound Route"))
			return
		}
		c.from, c.fromName = cleanNumber(from.URI.User), from.Display
		c.inTrunkID = trunk.ID
		if info := m.Get("X-BitVoice-CAMA"); info != "" && trunk.Protocol == "dahdi" {
			// A 911 call from a selective router's CAMA trunk, on a telephony card (only the card
			// program can send this): the caller's number came as ANI, the location comes from ALI.
			c.esinet, c.cama = true, info
			if c.from == "anonymous" {
				c.from = ""
			}
		}
		if trunk.ESInet {
			// The ESInet edge: the BCF checks the source before anything else is done for the call.
			c.srcIP = in.From.Addr.Addr().Unmap().String()
			why, suspicious := e.bcfCheck(cfg, c, in.From.Addr.Addr())
			if why != "" {
				r := sip.NewResponse(m, 503, "Service Unavailable")
				r.Set("Retry-After", "60")
				tx.Respond(r)
				return
			}
			c.suspicious = suspicious
			if e.bcfIdentity(cfg, c, m.Get("Identity"), did, in.From.Addr.Addr()) {
				c.suspicious = true
			}
		}
		if refuse := e.screenCall(cfg, c, m, trunk, did); refuse != "" {
			tx.Respond(sip.NewResponse(m, 603, refuse))
			return
		}
		if e.NameFor != nil && (c.fromName == "" || cleanNumber(c.fromName) == c.from || c.screened != "") {
			// The provider sent no name (or only the number): the company directory may know it.
			if n := e.NameFor(c.from); n != "" {
				c.fromName = n
			}
		}
		if l := cfg.FindLookup(route.Lookup); l != nil && (c.fromName == "" || cleanNumber(c.fromName) == c.from) {
			// Still no name: ask the route's caller ID lookup, for a moment at most.
			if n := e.lookupName(context.Background(), l, c.from); n != "" {
				c.fromName = n
			}
		}
		if c.screened == "labelled" {
			c.fromName = strings.TrimSpace(labelOf(cfg) + " " + c.fromName)
		}
		c.to = did
		p = e.resolveFor(cfg, route.Destination, 0, scriptCall{from: c.from, fromName: c.fromName, to: did, direction: "inbound", trunk: trunk.Name})
		if p.local == "esrp" {
			p = e.esrpPlan(cfg, c, did)
		}
		p.direction = "inbound"
		p.trunk = trunk.Name
		inTrunk = trunk.ID
		c.esinet = trunk.ESInet || c.cama != ""
	} else if link := e.linkCall(cfg, m, ip); link != nil {
		// A caller from a call link: no extension, no password; the gateway vouched for the link.
		name := guestName(m.Get("X-BitVoice-Link-Name"))
		max := link.MaxCalls
		if max == 0 {
			max = 5
		}
		if e.activeCallsFor("web:"+link.ID) >= max {
			tx.Respond(sip.NewResponse(m, 486, "Line Busy"))
			return
		}
		c.from, c.fromName = "web", "Web: "+firstNonEmptyStr(name, link.Name)
		c.callerExt = ""
		c.linkID = "web:" + link.ID
		c.to = link.Name
		p = e.resolveFor(cfg, link.Destination, 0, scriptCall{from: c.from, fromName: c.fromName, to: link.Name, direction: "inbound", trunk: "web"})
		p.direction = "inbound"
		p.trunk = "Call link: " + link.Name
	} else {
		lookup := func(user string) (string, bool) {
			x := cfg.FindExtension(e.localUser(user))
			if x == nil || !x.Enabled {
				return "", false
			}
			return x.Secret, true
		}
		result, user := e.auth.Verify(m.Get("Proxy-Authorization"), sip.INVITE, lookup)
		user = e.localUser(user)
		switch result {
		case sip.AuthOK:
			e.bans.succeed(clientIP(in))
		case sip.AuthBad:
			if e.bans.fail(clientIP(in), "failed call authentication as "+user) {
				e.publish("security.banned", map[string]any{"ip": clientIP(in).String(), "reason": "failed call authentication"})
			}
			fallthrough
		default:
			r := sip.NewResponse(m, 407, "")
			r.Set("To", withTag(m.Get("To")))
			r.Set("Proxy-Authenticate", e.auth.Challenge(result == sip.AuthStale))
			tx.Respond(r)
			return
		}
		ext := cfg.FindExtension(user)
		if n := e.activeCallsFor(user); n >= cfg.Security.MaxCallsPerExtension && !e.isLoadTest(m, ip) {
			tx.Respond(sip.NewResponse(m, 486, "Too Many Calls"))
			return
		}
		dialled := userOf(m.RequestURI)
		desk := ext
		if ext.HotDesk {
			c.deskExt = ext.Number
			if as := cfg.FindExtension(e.userAt(ext.Number)); as != nil && as.Enabled {
				ext = as // a hot desk calls as whoever is signed in at it
			}
		}
		if hd := cfg.System.HotDesk; desk.HotDesk && (cleanNumber(dialled) == hd.Login || cleanNumber(dialled) == hd.Logout) {
			p = plan{local: "hotdesk", box: cleanNumber(dialled), toName: "Hot desk", direction: "internal"}
		} else {
			p = e.route(cfg, ext, dialled)
		}
		c.from, c.fromName = ext.Number, ext.Name
		c.callerExt = ext.Number
		c.to = cleanNumber(dialled)
		if p.direction == "emergency" {
			where := ip
			if ip.IsLoopback() { // the browser phone gateway says where the browser is
				if a, err := netip.ParseAddr(m.Get("X-BitVoice-Client")); err == nil {
					where = a
				}
			}
			pl := e.emergency(cfg, desk, c.to, where, c.ID) // where the phone is: a hot desk's own place
			c.place = &pl
		}
	}
	if offer != nil && offer.Audio() == nil { // a text session: only text to 911 is taken
		if c.i3 == nil && !c.esinet { // (no phone free does not matter: a text waits on the 911 screen)
			tx.Respond(sip.NewResponse(m, 488, "No Audio"))
			return
		}
		if p.leavesByTrunk() { // an NG911 core passing calls on: it relays no text sessions
			tx.Respond(sip.NewResponse(m, 488, "Text To 911 Is Answered At The PSAP"))
			return
		}
		p = plan{local: "text911", toName: "Text to 911", direction: p.direction, trunk: p.trunk}
		c.textOnly = true
	}
	c.direction, c.toName, c.trunk, c.intercom = p.direction, p.toName, p.trunk, p.intercom
	if why := e.overLimit(p.direction); why != "" {
		tx.Respond(sip.NewResponse(m, 503, why))
		return
	}

	via := ""
	if in.From.Local.IsValid() {
		via = in.From.Local.String() // a call to the shared address gets its media there too
	}
	ep, err := e.pool.AllocateOn(via)
	if err != nil {
		tx.Respond(sip.NewResponse(m, 503, "No Media Ports"))
		return
	}
	contact, _ := sip.ParseAddress(m.Get("Contact"))
	target := m.RequestURI
	if contact != nil && contact.URI != nil {
		target = contact.URI.String()
	}
	localTag := sip.NewTag()
	toAddr, err := sip.ParseAddress(m.Get("To"))
	if err != nil || toAddr == nil {
		ep.Close()
		tx.Respond(sip.NewResponse(m, 400, "Bad To"))
		return
	}
	toAddr.Params.Set("tag", localTag)
	a := &Leg{e: e, call: c, inbound: true, Number: c.from, Name: c.fromName, Ext: c.callerExt,
		callID: m.CallID(), localTag: localTag, remoteTag: from.Tag(), localURI: toAddr.String(),
		remoteURI: m.Get("From"), remoteTarget: target, routeSet: m.GetAll("Record-Route"), peer: in.From,
		ep: ep, invite: tx, localSeq: 1}
	if p.direction == "inbound" {
		a.TrunkID = inTrunk
	}
	a.textOK = c.i3 != nil || c.esinet
	if offer != nil {
		a.setRemoteSDP(offer)
	}
	c.caller = a
	c.offer = offer
	ep.Start()
	c.startText()
	tx.NoAck = func() { c.hangup("caller never acknowledged the answer", "caller") }

	e.mu.Lock()
	e.calls[c.ID] = c
	e.pending[tx.Branch()] = c
	e.mu.Unlock()
	e.addLeg(a)
	e.publish("call.started", c.info())
	go c.locateByNumber()

	if p.reject != 0 {
		c.reject(p.reject, p.reason)
		return
	}
	if p.local != "" {
		c.answerLocal(p)
		return
	}
	go c.run(p)
}

func inboundRoute(cfg *config.Config, trunkID, did string) *config.InboundRoute {
	var fallback, near *config.InboundRoute
	for i := range cfg.InboundRoutes {
		r := &cfg.InboundRoutes[i]
		if r.Trunk != "" && r.Trunk != trunkID {
			continue
		}
		if r.DID == "" {
			if fallback == nil {
				fallback = r
			}
			continue
		}
		switch sameNumber(r.DID, did) {
		case 2:
			return r
		case 1:
			if near == nil {
				near = r
			}
		}
	}
	if near != nil {
		return near
	}
	return fallback
}

// sameNumber compares two phone numbers as carriers write them: 2 when the digits are the same, 1 when
// one is the other with a country code or national 0 in front (+1 212..., 1212... and 212...; +44 20...
// and 020...), 0 otherwise. Only numbers of 9 digits or more are matched loosely.
func sameNumber(a, b string) int {
	digits := func(s string) string {
		s = strings.TrimPrefix(cleanNumber(s), "+")
		s = strings.TrimPrefix(s, "00")
		return strings.TrimLeft(s, "0")
	}
	a, b = digits(a), digits(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 2
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) >= 9 && strings.HasSuffix(a, b) && len(a)-len(b) <= 3 {
		return 1
	}
	return 0
}

func (e *Engine) activeCallsFor(ext string) int {
	n := 0
	for _, c := range e.allCalls() {
		if c.callerExt == ext || c.linkID == ext {
			n++
		}
	}
	return n
}

// emergency tells the people who need to know that someone dialled an emergency number: an event for every
// screen and a text message to each phone on the notification list (Kari's Law on-site notification).
func (e *Engine) emergency(cfg *config.Config, ext *config.Extension, number string, from netip.Addr, callID string) Place {
	pl := e.placeOf(cfg, ext, from)
	text := fmt.Sprintf("EMERGENCY: %s dialled from extension %s (%s), %s", number, ext.Number, ext.Name, pl.Text)
	e.Log.Warn("emergency call", "number", number, "extension", ext.Number, "location", pl.Text)
	e.publish("emergency.call", map[string]any{"number": number, "extension": ext.Number, "name": ext.Name,
		"location": pl.Text, "place": pl, "call": callID, "at": time.Now().UTC()})
	for _, n := range cfg.System.EmergencyNotify {
		for _, b := range e.reg.contacts(n) {
			msg := &sip.Message{IsRequest: true, Method: sip.MESSAGE, RequestURI: b.requestURI()}
			msg.Set("From", "\"Emergency alert\" <sip:alert@"+e.stack.LocalHost(b.peer)+">;tag="+sip.NewTag())
			msg.Set("To", "<sip:"+n+"@"+e.stack.LocalHost(b.peer)+">")
			msg.Set("Call-ID", sip.NewCallID())
			msg.Set("CSeq", "1 MESSAGE")
			msg.Set("Content-Type", "text/plain;charset=UTF-8")
			msg.Body = []byte(text)
			tx := e.stack.Request(msg, b.peer)
			go func() {
				for range tx.Responses {
				}
			}()
		}
	}
	return pl
}

// trunkFor identifies a trunk by where a request came from: the provider's address and SIP port, or any
// address listed in Allow from. Matching the port as well as the address means a phone that happens to share
// the provider's address (the same NAT, or a test bench) is never taken for the provider.
func (e *Engine) trunkFor(cfg *config.Config, src netip.AddrPort) *config.Trunk {
	ip := src.Addr()
	if ip.IsLoopback() && e.iax() != nil {
		// The IAX2 and H.323 gateways: each such trunk is a loopback port of its own.
		for _, t := range cfg.Trunks {
			if t.Enabled && isIAX(&t) && int(src.Port()) == e.iax().SIPPort(t.ID) {
				tc := t
				return &tc
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// First a trunk whose own address and port sent this; only then one that merely allows the
	// address (two trunks on one host, a carrier's range and a gateway, must not be confused).
	for _, t := range cfg.Trunks {
		if !t.Enabled {
			continue
		}
		if st := e.trunks[t.ID]; st != nil {
			port := t.Port
			if port == 0 {
				port = 5060
			}
			for _, a := range st.addrs {
				if a == ip && int(src.Port()) == port {
					tc := t
					return &tc
				}
			}
		}
	}
	for _, t := range cfg.Trunks {
		if !t.Enabled {
			continue
		}
		allowFrom := t.AllowFrom
		if t.Teams {
			allowFrom = append(slices.Clone(TeamsRanges), allowFrom...)
		}
		for _, allow := range allowFrom {
			if p, err := netip.ParsePrefix(allow); err == nil && p.Contains(ip) {
				tc := t
				return &tc
			}
			if a, err := netip.ParseAddr(allow); err == nil && a == ip {
				tc := t
				return &tc
			}
		}
	}
	return nil
}

// waking is an extension whose apps a stage wakes.
type waking struct{ ext, name string }

func (e *Engine) hasApps(ext string) bool { return e.Mobile != nil && e.Mobile.HasDevices(ext) }
