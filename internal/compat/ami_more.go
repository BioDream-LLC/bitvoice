package compat

// More of AMI's actions and events, for tools written for Asterisk: queue agents (pause, add, remove,
// summary), parked calls, conference rooms (ConfBridge actions), mailboxes, recording pause, device
// and extension state lists, core status, event filters and UserEvent. Each is answered from what the
// engine has; one the engine cannot do says so instead of pretending.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// Optional engine abilities: the real engine has them all; a test fake may have some.
type agentPBX interface {
	SetAgent(ext string, loggedOut, paused bool, reason string) pbx.Presence
	Agents() []pbx.Presence
}
type parkPBX interface{ Parked() []pbx.ParkedCall }
type confPBX interface {
	Conferences() []pbx.ConferenceRoom
	MuteConferenceMember(number, id string, muted bool) error
	RemoveConferenceMember(number, id string) error
	LockConference(number string, locked bool) error
}
type mailPBX interface {
	MailboxCounts(box string) (fresh, old int, ok bool)
}
type recordPBX interface {
	PauseRecording(id string, pause bool, by string) bool
}

func truthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "yes" || v == "1" || v == "on"
}

// filter is one AMI event filter: "Event: Newchannel" keeps matching events; "!..." drops them.
type amiFilter struct {
	re     *regexp.Regexp
	negate bool
}

// passes says whether an event survives the session's filters, as Asterisk applies them: with any
// keeping filter, only events matching one pass; a dropping filter that matches drops it.
func (s *session) passes(kv []string) bool {
	s.fmu.Lock()
	fs := s.filters
	s.fmu.Unlock()
	if len(fs) == 0 {
		return true
	}
	var lines []string
	for i := 0; i+1 < len(kv); i += 2 {
		lines = append(lines, kv[i]+": "+kv[i+1])
	}
	keep, anyKeep := false, false
	for _, f := range fs {
		hit := false
		for _, l := range lines {
			if f.re.MatchString(l) {
				hit = true
				break
			}
		}
		if f.negate && hit {
			return false
		}
		if !f.negate {
			anyKeep = true
			keep = keep || hit
		}
	}
	return !anyKeep || keep
}

// event sends an event when events are on and the filters let it through.
func (s *session) event(kv ...string) {
	if !s.events || !s.passes(kv) {
		return
	}
	s.send(kv...)
}

// queueMember turns an AMI interface (Local/101@from-queue/n, PJSIP/101, SIP/101, 101) into the extension.
func queueMember(iface string) string {
	m := iface
	if i := strings.Index(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m, _, _ = strings.Cut(m, "@")
	m, _, _ = strings.Cut(m, "/")
	return strings.TrimSpace(m)
}

// conferenceMember finds a member by channel (PJSIP/101-..., the member's id, or their number).
func conferenceMember(room pbx.ConferenceRoom, channel string) (pbx.ConferenceMember, bool) {
	want := Extension(channel)
	if want == "" {
		want = channel
	}
	for _, m := range room.Members {
		if m.ID == channel || m.Number == want || strings.HasPrefix(channel, "PJSIP/"+m.Number+"-") {
			return m, true
		}
	}
	return pbx.ConferenceMember{}, false
}

func memberChannel(room pbx.ConferenceRoom, m pbx.ConferenceMember) string {
	return "PJSIP/" + m.Number + "-" + short(m.ID)
}

// more handles the actions beyond the basic set; false when the action is not one of them.
func (s *session) more(action string, m amiMsg, id string, reply func(kv ...string), fail func(string), write func() bool) bool {
	p := s.a.PBX
	list := func(event string, items int) {
		s.send("Event", event, "ActionID", id, "EventList", "Complete", "ListItems", strconv.Itoa(items))
	}
	switch action {
	case "queuesummary":
		reply("EventList", "start", "Message", "Queue summary will follow")
		n := 0
		for _, q := range p.Queues() {
			if want := m.get("Queue"); want != "" && want != q.Number && want != q.Name {
				continue
			}
			logged, avail, longest := 0, 0, 0
			for _, ag := range q.Agents {
				if ag.State != "offline" {
					logged++
				}
				if ag.State == "available" || ag.State == "idle" {
					avail++
				}
			}
			for _, w := range q.Waiting {
				longest = max(longest, int(time.Since(w.Since).Seconds()))
			}
			s.send("Event", "QueueSummary", "ActionID", id, "Queue", q.Number, "LoggedIn", strconv.Itoa(logged),
				"Available", strconv.Itoa(avail), "Callers", strconv.Itoa(len(q.Waiting)), "HoldTime", strconv.Itoa(q.Totals.LongestWait),
				"TalkTime", "0", "LongestHoldTime", strconv.Itoa(longest))
			n++
		}
		list("QueueSummaryComplete", n)
	case "queuepause", "queueadd", "queueremove":
		if !write() {
			return true
		}
		ap, ok := p.(agentPBX)
		if !ok {
			fail("Queue agents are not available here")
			return true
		}
		ext := queueMember(m.get("Interface"))
		if ext == "" || p.Config().FindExtension(ext) == nil {
			fail("Interface not found: give Interface as PJSIP/101 (an extension)")
			return true
		}
		cur := presenceOf(ap.Agents(), ext)
		switch action {
		case "queuepause":
			paused := truthy(m.get("Paused"))
			ap.SetAgent(ext, cur.LoggedOut, paused, m.get("Reason"))
			s.audit("ami.queuepause", ext+map[bool]string{true: " paused", false: " unpaused"}[paused])
			reply("Message", map[bool]string{true: "Interface paused successfully", false: "Interface unpaused successfully"}[paused])
		case "queueadd":
			ap.SetAgent(ext, false, truthy(m.get("Paused")), "")
			s.audit("ami.queueadd", ext)
			reply("Message", "Added interface to queue (BitVoice signs the agent in to all of their queues)")
		case "queueremove":
			ap.SetAgent(ext, true, false, "")
			s.audit("ami.queueremove", ext)
			reply("Message", "Removed interface from queue (BitVoice signs the agent out of all of their queues)")
		}
	case "agents":
		ap, ok := p.(agentPBX)
		if !ok {
			fail("Queue agents are not available here")
			return true
		}
		reply("EventList", "start", "Message", "Agents will follow")
		cfg := p.Config()
		n := 0
		for _, ag := range ap.Agents() {
			name := ag.Extension
			if x := cfg.FindExtension(ag.Extension); x != nil {
				name = x.Name
			}
			st := "AGENT_IDLE"
			if ag.LoggedOut {
				st = "AGENT_LOGGEDOFF"
			} else if ag.Paused {
				st = "AGENT_ONCALL"
			}
			s.send("Event", "Agents", "ActionID", id, "Agent", ag.Extension, "Name", name, "Status", st, "LoggedInTime", strconv.FormatInt(ag.Since.Unix(), 10),
				"Paused", boolNum(ag.Paused), "PausedReason", ag.Reason)
			n++
		}
		list("AgentsComplete", n)
	case "parkedcalls":
		pp, ok := p.(parkPBX)
		if !ok {
			fail("Parking is not available here")
			return true
		}
		reply("EventList", "start", "Message", "Parked calls will follow")
		parked := pp.Parked()
		for _, pc := range parked {
			s.send("Event", "ParkedCall", "ActionID", id, "ParkingSpace", pc.Slot, "ParkingLot", "default", "ParkeeChannel", "PJSIP/"+pc.Party+"-"+short(pc.CallID)+"1",
				"ParkeeUniqueid", pc.CallID+"-1", "ParkeeCallerIDNum", pc.Party, "ParkerDialString", "PJSIP/"+pc.ParkedBy,
				"ParkingDuration", strconv.Itoa(int(time.Since(pc.Since).Seconds())))
		}
		list("ParkedCallsComplete", len(parked))
	case "confbridgelistrooms":
		cp, ok := p.(confPBX)
		if !ok {
			fail("Conference rooms are not available here")
			return true
		}
		reply("EventList", "start", "Message", "Conferences will follow")
		rooms := cp.Conferences()
		for _, r := range rooms {
			muted := 0
			for _, mb := range r.Members {
				if mb.Muted {
					muted++
				}
			}
			s.send("Event", "ConfbridgeListRooms", "ActionID", id, "Conference", r.Number, "Parties", strconv.Itoa(len(r.Members)),
				"Marked", "0", "Locked", map[bool]string{true: "Yes", false: "No"}[r.Locked], "Muted", map[bool]string{true: "Yes", false: "No"}[muted == len(r.Members) && muted > 0])
		}
		list("ConfbridgeListRoomsComplete", len(rooms))
	case "confbridgelist":
		cp, ok := p.(confPBX)
		if !ok {
			fail("Conference rooms are not available here")
			return true
		}
		room, ok := findRoom(cp, m.get("Conference"))
		if !ok {
			fail("No active conferences.")
			return true
		}
		reply("EventList", "start", "Message", "Confbridge user list will follow")
		for _, mb := range room.Members {
			s.send("Event", "ConfbridgeList", "ActionID", id, "Conference", room.Number, "CallerIDNum", mb.Number, "CallerIDName", mb.Name,
				"Channel", memberChannel(room, mb), "Admin", map[bool]string{true: "Yes", false: "No"}[mb.Leader], "MarkedUser", "No",
				"Muted", map[bool]string{true: "Yes", false: "No"}[mb.Muted], "Talking", map[bool]string{true: "Yes", false: "No"}[mb.Speaking],
				"AnsweredTime", strconv.Itoa(int(time.Since(mb.Joined).Seconds())))
		}
		list("ConfbridgeListComplete", len(room.Members))
	case "confbridgekick", "confbridgemute", "confbridgeunmute":
		if !write() {
			return true
		}
		cp, ok := p.(confPBX)
		if !ok {
			fail("Conference rooms are not available here")
			return true
		}
		room, ok := findRoom(cp, m.get("Conference"))
		if !ok {
			fail("No Conference by that name found.")
			return true
		}
		targets := room.Members
		if ch := m.get("Channel"); !strings.EqualFold(ch, "all") && !strings.EqualFold(ch, "participants") {
			mb, ok := conferenceMember(room, ch)
			if !ok {
				fail("No Channel by that name found in Conference.")
				return true
			}
			targets = []pbx.ConferenceMember{mb}
		}
		for _, mb := range targets {
			var err error
			switch action {
			case "confbridgekick":
				err = cp.RemoveConferenceMember(room.Number, mb.ID)
			default:
				err = cp.MuteConferenceMember(room.Number, mb.ID, action == "confbridgemute")
			}
			if err != nil {
				fail(err.Error())
				return true
			}
		}
		s.audit("ami."+action, room.Number+" "+m.get("Channel"))
		reply("Message", map[string]string{"confbridgekick": "User kicked", "confbridgemute": "User muted", "confbridgeunmute": "User unmuted"}[action])
	case "confbridgelock", "confbridgeunlock":
		if !write() {
			return true
		}
		cp, ok := p.(confPBX)
		if !ok {
			fail("Conference rooms are not available here")
			return true
		}
		room, ok := findRoom(cp, m.get("Conference"))
		if !ok {
			fail("No Conference by that name found.")
			return true
		}
		if err := cp.LockConference(room.Number, action == "confbridgelock"); err != nil {
			fail(err.Error())
			return true
		}
		s.audit("ami."+action, room.Number)
		reply("Message", map[bool]string{true: "Conference locked", false: "Conference unlocked"}[action == "confbridgelock"])
	case "mailboxcount", "mailboxstatus":
		mp, ok := p.(mailPBX)
		if !ok {
			fail("Voicemail is not available here")
			return true
		}
		box, _, _ := strings.Cut(m.get("Mailbox"), "@")
		fresh, old, ok := mp.MailboxCounts(box)
		if !ok {
			fail("Mailbox not found")
			return true
		}
		if action == "mailboxcount" {
			reply("Message", "Mailbox Message Count", "Mailbox", m.get("Mailbox"), "UrgMessages", "0", "NewMessages", strconv.Itoa(fresh), "OldMessages", strconv.Itoa(old))
		} else {
			reply("Message", "Mailbox Status", "Mailbox", m.get("Mailbox"), "Waiting", boolNum(fresh > 0))
		}
	case "pausemonitor", "unpausemonitor", "pausemixmonitor", "unpausemixmonitor":
		if !write() {
			return true
		}
		rp, ok := p.(recordPBX)
		if !ok {
			fail("Recording is not available here")
			return true
		}
		c, ch, ok := Find(p, m.get("Channel"))
		if !ok {
			fail("No such channel")
			return true
		}
		pause := !strings.HasPrefix(action, "un")
		if !rp.PauseRecording(c.ID, pause, "AMI "+s.user.Username) {
			fail("This call is not being recorded")
			return true
		}
		s.audit("ami."+action, ch.Name)
		reply("Message", map[bool]string{true: "Paused monitoring of the channel", false: "Unpaused monitoring of the channel"}[pause])
	case "extensionstatelist", "devicestatelist":
		reply("EventList", "start", "Message", "Extension statuses will follow")
		eps := Endpoints(p)
		for _, e := range eps {
			st, txt, dev := 4, "Unavailable", "UNAVAILABLE"
			if e.State == "online" {
				st, txt, dev = 0, "Idle", "NOT_INUSE"
				if len(e.Channels) > 0 {
					st, txt, dev = 1, "InUse", "INUSE"
				}
			}
			if action == "devicestatelist" {
				s.send("Event", "DeviceStateChange", "ActionID", id, "Device", "PJSIP/"+e.Resource, "State", dev)
			} else {
				s.send("Event", "ExtensionStatus", "ActionID", id, "Exten", e.Resource, "Context", "ext-local", "Hint", "PJSIP/"+e.Resource,
					"Status", strconv.Itoa(st), "StatusText", txt)
			}
		}
		list(map[bool]string{true: "DeviceStateListComplete", false: "ExtensionStateListComplete"}[action == "devicestatelist"], len(eps))
	case "corestatus":
		start := s.a.Started
		if start.IsZero() {
			start = time.Now()
		}
		calls := 0
		for _, c := range p.Calls() {
			calls += len(Channels(c))
		}
		reply("CoreStartupDate", start.Format("2006-01-02"), "CoreStartupTime", start.Format("15:04:05"),
			"CoreReloadDate", start.Format("2006-01-02"), "CoreReloadTime", start.Format("15:04:05"), "CoreCurrentCalls", strconv.Itoa(calls))
	case "coresettings":
		reply("AMIversion", "7.0.3", "AsteriskVersion", "20.0.0-bitvoice", "SystemName", p.Config().System.Name,
			"CoreMaxCalls", "0", "CoreMaxLoadAvg", "0.000000", "CoreRunUser", "", "CoreRunGroup", "", "CoreMaxFilehandles", "0",
			"CoreRealTimeEnabled", "No", "CoreCDRenabled", "Yes", "CoreHTTPenabled", "Yes")
	case "getvar":
		c, ch, ok := Find(p, m.get("Channel"))
		if !ok && m.get("Channel") != "" {
			fail("No such channel")
			return true
		}
		v := m.get("Variable")
		val := ""
		switch strings.ToUpper(v) {
		case "CALLERID(NUM)":
			val = ch.CallerNum
		case "CALLERID(NAME)":
			val = ch.CallerName
		case "EXTEN":
			val = ch.Exten
		case "UNIQUEID":
			val = ch.ID
		case "LINKEDID":
			val = c.ID + "-1"
		case "CHANNEL(STATE)":
			val = StateName(ch.State)
		default:
			fail("BitVoice answers CALLERID(num), CALLERID(name), EXTEN, UNIQUEID, LINKEDID and CHANNEL(state)")
			return true
		}
		reply("Variable", v, "Value", val)
	case "filter":
		op := strings.ToLower(m.get("Operation"))
		if op == "" {
			op = "add"
		}
		if op != "add" {
			fail("BitVoice takes Operation: Add")
			return true
		}
		expr := m.get("Filter")
		neg := strings.HasPrefix(expr, "!")
		re, err := regexp.Compile(strings.TrimPrefix(expr, "!"))
		if err != nil || expr == "" {
			fail("Filter is a regular expression, like Event: Newchannel (with ! to drop)")
			return true
		}
		s.fmu.Lock()
		if len(s.filters) >= 50 {
			s.fmu.Unlock()
			fail("Too many filters")
			return true
		}
		s.filters = append(s.filters, amiFilter{re: re, negate: neg})
		s.fmu.Unlock()
		reply("Message", "Filter Added Successfully")
	case "userevent":
		name := m.get("UserEvent")
		if name == "" {
			fail("UserEvent is required")
			return true
		}
		kv := []string{"Event", "UserEvent", "Privilege", "user,all", "UserEvent", name}
		keys := make([]string, 0, len(m))
		for k := range m {
			if k != "action" && k != "actionid" && k != "userevent" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			kv = append(kv, headerCase(k), m[k])
		}
		s.a.Bus.Publish("ami.userevent", amiUserEvent(kv))
		reply("Message", "Event Sent")
	case "reload":
		reply("Message", "Module Reloaded (BitVoice applies every change at once)")
	case "command":
		fail("BitVoice has no Asterisk command line: use the web interface or its REST API")
	default:
		return false
	}
	return true
}

type amiUserEvent []string

func findRoom(cp confPBX, name string) (pbx.ConferenceRoom, bool) {
	for _, r := range cp.Conferences() {
		if r.Number == name || strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return pbx.ConferenceRoom{}, false
}

func presenceOf(all []pbx.Presence, ext string) pbx.Presence {
	for _, p := range all {
		if p.Extension == ext {
			return p
		}
	}
	return pbx.Presence{Extension: ext}
}

// headerCase writes a lowered header name back in the usual form (callerid -> Callerid).
func headerCase(k string) string {
	if k == "" {
		return k
	}
	return strings.ToUpper(k[:1]) + k[1:]
}

// moreEvents turns engine events other than channel changes into AMI events.
func (s *session) moreEvents(ev any, typ string, peers map[string]string) {
	switch d := ev.(type) {
	case amiUserEvent:
		s.event(d...)
	case map[string]any:
		switch typ {
		case "voicemail.changed":
			box, _ := d["box"].(string)
			fresh, _ := d["new"].(int)
			old, _ := d["old"].(int)
			s.event("Event", "MessageWaiting", "Privilege", "call,all", "Mailbox", box+"@default", "Waiting", map[bool]string{true: "yes", false: "no"}[fresh > 0],
				"New", strconv.Itoa(fresh), "Old", strconv.Itoa(old))
		case "registrations.changed":
			for _, e := range Endpoints(s.a.PBX) {
				st := "Unreachable"
				if e.State == "online" {
					st = "Reachable"
				}
				if prev, ok := peers[e.Resource]; ok && prev == st {
					continue
				}
				peers[e.Resource] = st
				s.event("Event", "PeerStatus", "Privilege", "system,all", "ChannelType", "PJSIP", "Peer", "PJSIP/"+e.Resource, "PeerStatus", st)
				s.event("Event", "ContactStatus", "Privilege", "system,all", "URI", "sip:"+e.Resource, "ContactStatus", map[string]string{"Reachable": "Reachable", "Unreachable": "Removed"}[st],
					"AOR", e.Resource, "EndpointName", e.Resource)
			}
		}
	case pbx.ConferenceRoom:
		if typ == "conference.updated" {
			s.confEvents(d)
		}
	}
}

// confEvents reports who joined and left a room since the last look.
func (s *session) confEvents(r pbx.ConferenceRoom) {
	s.fmu.Lock()
	if s.rooms == nil {
		s.rooms = map[string]map[string]pbx.ConferenceMember{}
	}
	before := s.rooms[r.Number]
	now := map[string]pbx.ConferenceMember{}
	for _, m := range r.Members {
		now[m.ID] = m
	}
	s.rooms[r.Number] = now
	s.fmu.Unlock()
	for id, m := range now {
		if _, ok := before[id]; !ok {
			s.event("Event", "ConfbridgeJoin", "Privilege", "call,all", "Conference", r.Number, "Channel", memberChannel(r, m),
				"CallerIDNum", m.Number, "CallerIDName", m.Name, "Admin", map[bool]string{true: "Yes", false: "No"}[m.Leader], "Muted", map[bool]string{true: "Yes", false: "No"}[m.Muted])
		}
	}
	for id, m := range before {
		if _, ok := now[id]; !ok {
			s.event("Event", "ConfbridgeLeave", "Privilege", "call,all", "Conference", r.Number, "Channel", memberChannel(r, m),
				"CallerIDNum", m.Number, "CallerIDName", m.Name)
		}
	}
	if len(now) == 0 && len(before) > 0 {
		s.event("Event", "ConfbridgeEnd", "Privilege", "call,all", "Conference", r.Number)
	}
}
