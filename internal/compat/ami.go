package compat

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// AMI is the Asterisk Manager Interface: "Key: Value" lines, a blank line ending each message.
type AMI struct {
	PBX PBX
	Bus *events.Bus
	Log *slog.Logger
	// Audit records each action that changes a call.
	Audit func(user, action, detail string)
	// Started is when BitVoice started (CoreStatus).
	Started time.Time

	mu    sync.Mutex
	ln    net.Listener
	addr  string
	want  config.Compat // the latest settings, for retrying
	retry *time.Timer
	wg    sync.WaitGroup
}

// Apply starts, moves or stops the listener to match the configuration.
func (a *AMI) Apply(cfg config.Compat) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.want = cfg
	if a.retry != nil {
		a.retry.Stop()
		a.retry = nil
	}
	want := ""
	if cfg.AMI {
		want = cfg.AMIListen
	}
	if want == a.addr {
		return
	}
	if a.ln != nil {
		_ = a.ln.Close() // open sessions end on their own as their logins are checked again
		a.ln = nil
	}
	a.addr = ""
	if want == "" {
		return
	}
	ln, err := net.Listen("tcp", want)
	if err != nil {
		// Often the port is still held by the previous run for a moment; keep trying.
		a.Log.Warn("AMI could not listen; trying again in 5 seconds", "address", want, "err", err)
		a.retry = time.AfterFunc(5*time.Second, func() {
			a.mu.Lock()
			cfg := a.want
			a.mu.Unlock()
			a.Apply(cfg)
		})
		return
	}
	a.ln, a.addr = ln, want
	a.Log.Info("AMI listening", "address", ln.Addr().String())
	a.wg.Add(1)
	go a.accept(ln)
}

// Addr is where AMI listens, or "".
func (a *AMI) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ln == nil {
		return ""
	}
	return a.ln.Addr().String()
}

// Close stops listening.
func (a *AMI) Close() { a.Apply(config.Compat{}) }

func (a *AMI) accept(ln net.Listener) {
	defer a.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go a.serve(conn)
	}
}

type amiMsg map[string]string

func (m amiMsg) get(k string) string { return m[strings.ToLower(k)] }

// readMsg reads one message; keys are case-insensitive, as Asterisk treats them.
func readMsg(r *bufio.Reader) (amiMsg, error) {
	m := amiMsg{}
	for n := 0; ; n++ {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if n > 200 || len(line) > 4096 {
			return nil, errors.New("message too long")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(m) == 0 {
				continue
			}
			return m, nil
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "variable" && m[k] != "" {
			v = m[k] + "," + v
		}
		m[k] = v
	}
}

// session is one connected manager client.
type session struct {
	a         *AMI
	conn      net.Conn
	wmu       sync.Mutex
	w         *bufio.Writer
	user      *config.CompatUser
	challenge string
	events    bool
	fails     int

	fmu     sync.Mutex
	filters []amiFilter
	rooms   map[string]map[string]pbx.ConferenceMember // for ConfbridgeJoin and Leave
}

// clean keeps a value on one line: AMI has no escaping, so a line break would start a new header.
func clean(v string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(v) }

func (s *session) send(kv ...string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(s.w, "%s: %s\r\n", kv[i], clean(kv[i+1]))
	}
	s.w.WriteString("\r\n")
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = s.w.Flush()
}

func (a *AMI) serve(conn net.Conn) {
	defer conn.Close()
	s := &session{a: a, conn: conn, w: bufio.NewWriter(conn), events: true}
	s.wmu.Lock()
	s.w.WriteString("Asterisk Call Manager/7.0.3\r\n")
	_ = s.w.Flush()
	s.wmu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := bufio.NewReader(conn)
	for {
		if s.user == nil {
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second)) // sign in promptly
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		m, err := readMsg(r)
		if err != nil {
			return
		}
		if !s.handle(ctx, m) {
			return
		}
	}
}

// login looks a user up again each time, so a removed login or a changed secret stops working at once.
func (s *session) login(name string) *config.CompatUser {
	for _, u := range s.a.PBX.Config().System.Compat.Users {
		if strings.EqualFold(u.Username, name) {
			return &u
		}
	}
	return nil
}

func (s *session) handle(ctx context.Context, m amiMsg) bool {
	id := m.get("ActionID")
	reply := func(kv ...string) {
		out := []string{"Response", "Success"}
		if id != "" {
			out = append(out, "ActionID", id)
		}
		s.send(append(out, kv...)...)
	}
	fail := func(msg string) {
		out := []string{"Response", "Error"}
		if id != "" {
			out = append(out, "ActionID", id)
		}
		s.send(append(out, "Message", msg)...)
	}
	action := strings.ToLower(m.get("Action"))
	cfg := s.a.PBX.Config().System.Compat
	if !cfg.AMI {
		fail("AMI has been switched off")
		return false
	}
	if s.user != nil {
		u := s.login(s.user.Username)
		if u == nil || u.Secret != s.user.Secret {
			fail("Your login was changed or removed")
			return false
		}
		s.user = u
	}
	switch action {
	case "challenge":
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		s.challenge = hex.EncodeToString(b)
		reply("Challenge", s.challenge)
		return true
	case "login":
		u := s.login(m.get("Username"))
		ok := false
		if u != nil {
			if strings.EqualFold(m.get("AuthType"), "md5") && s.challenge != "" {
				sum := md5.Sum([]byte(s.challenge + u.Secret))
				ok = subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(strings.ToLower(m.get("Key")))) == 1
			} else {
				ok = subtle.ConstantTimeCompare([]byte(u.Secret), []byte(m.get("Secret"))) == 1
			}
		}
		s.challenge = ""
		if !ok {
			s.fails++
			time.Sleep(time.Second) // slows guessing
			fail("Authentication failed")
			return s.fails < 3
		}
		s.user = u
		if strings.EqualFold(m.get("Events"), "off") {
			s.events = false
		}
		reply("Message", "Authentication accepted")
		s.send("Event", "FullyBooted", "Privilege", "system,all", "Status", "Fully Booted")
		go s.stream(ctx)
		return true
	case "logoff":
		s.send("Response", "Goodbye", "ActionID", id, "Message", "Thanks for all the fish.")
		return false
	}
	if s.user == nil {
		fail("Permission denied: log in first")
		return true
	}
	write := func() bool {
		if !s.user.Write {
			fail("Permission denied: this login is read only")
			return false
		}
		return true
	}
	p := s.a.PBX
	switch action {
	case "ping":
		reply("Ping", "Pong", "Timestamp", strconv.FormatFloat(float64(time.Now().UnixMicro())/1e6, 'f', 6, 64))
	case "events":
		s.events = !strings.EqualFold(m.get("EventMask"), "off")
		reply("Events", map[bool]string{true: "On", false: "Off"}[s.events])
	case "coreshowchannels", "status":
		reply("EventList", "start", "Message", "Channels will follow")
		n := 0
		for _, c := range p.Calls() {
			for _, ch := range Channels(c) {
				ev := "CoreShowChannel"
				if action == "status" {
					ev = "Status"
				}
				s.send(append([]string{"Event", ev, "ActionID", id}, channelFields(ch, c)...)...)
				n++
			}
		}
		done := "CoreShowChannelsComplete"
		if action == "status" {
			done = "StatusComplete"
		}
		s.send("Event", done, "ActionID", id, "EventList", "Complete", "ListItems", strconv.Itoa(n), "Items", strconv.Itoa(n))
	case "pjsipshowendpoints", "sippeers":
		reply("EventList", "start", "Message", "A listing of Endpoints follows, presented as EndpointList events")
		eps := Endpoints(p)
		for _, e := range eps {
			dev := "Unavailable"
			if e.State == "online" {
				dev = "Not in use"
				if len(e.Channels) > 0 {
					dev = "In use"
				}
			}
			s.send("Event", "EndpointList", "ActionID", id, "ObjectType", "endpoint", "ObjectName", e.Resource,
				"Transport", "", "Aor", e.Resource, "Auths", e.Resource, "OutboundAuths", "", "Contacts", "",
				"DeviceState", dev, "ActiveChannels", strconv.Itoa(len(e.Channels)))
		}
		s.send("Event", "EndpointListComplete", "ActionID", id, "EventList", "Complete", "ListItems", strconv.Itoa(len(eps)))
	case "extensionstate":
		ext := m.get("Exten")
		st, txt := -1, "Unknown"
		for _, e := range Endpoints(p) {
			if e.Resource == ext {
				st, txt = 4, "Unavailable"
				if e.State == "online" {
					st, txt = 0, "Idle"
					if len(e.Channels) > 0 {
						st, txt = 1, "InUse"
					}
				}
			}
		}
		reply("Message", "Extension Status", "Exten", ext, "Context", m.get("Context"), "Hint", "PJSIP/"+ext,
			"Status", strconv.Itoa(st), "StatusText", txt)
	case "queuestatus":
		reply("EventList", "start", "Message", "Queue status will follow")
		n := 0
		for _, q := range p.Queues() {
			if want := m.get("Queue"); want != "" && want != q.Number && want != q.Name {
				continue
			}
			s.send(queueParams(q, id)...)
			n++
			for _, ag := range q.Agents {
				s.send("Event", "QueueMember", "ActionID", id, "Queue", q.Number, "Name", ag.Name,
					"Location", "PJSIP/"+ag.Extension, "StateInterface", "PJSIP/"+ag.Extension,
					"Status", agentStatus(ag.State), "Paused", boolNum(ag.State == "paused"), "InCall", boolNum(ag.State == "busy"))
			}
			for i, w := range q.Waiting {
				s.send("Event", "QueueEntry", "ActionID", id, "Queue", q.Number, "Position", strconv.Itoa(i+1),
					"Channel", tech(w.From, "")+"-"+short(w.CallID)+"1", "Uniqueid", w.CallID+"-1",
					"CallerIDNum", w.From, "CallerIDName", w.Name, "Wait", strconv.Itoa(int(time.Since(w.Since).Seconds())))
			}
		}
		s.send("Event", "QueueStatusComplete", "ActionID", id, "EventList", "Complete", "ListItems", strconv.Itoa(n))
	case "hangup":
		if !write() {
			return true
		}
		c, ch, ok := Find(p, m.get("Channel"))
		if !ok || !p.Hangup(c.ID, "AMI "+s.user.Username) {
			fail("No such channel")
			return true
		}
		s.audit("ami.hangup", ch.Name)
		reply("Message", "Channel Hungup")
	case "redirect", "blindtransfer":
		if !write() {
			return true
		}
		c, ch, ok := Find(p, m.get("Channel"))
		if !ok {
			fail("Channel specified does not exist")
			return true
		}
		// The named channel goes to the new number; the other party leaves, as in Asterisk.
		leaving := "callee"
		if ch.Side == "callee" {
			leaving = "caller"
		}
		if err := p.Redirect(c.ID, leaving, m.get("Exten")); err != nil {
			fail(err.Error())
			return true
		}
		s.audit("ami.redirect", ch.Name+" to "+m.get("Exten"))
		reply("Message", "Redirect successful")
	case "originate":
		if !write() {
			return true
		}
		ext, num := Extension(m.get("Channel")), m.get("Exten")
		if ext == "" || num == "" {
			fail("Originate needs Channel (PJSIP/100) and Exten (the number to call)")
			return true
		}
		timeout := 30 * time.Second
		if ms, err := strconv.Atoi(m.get("Timeout")); err == nil && ms > 0 {
			timeout = min(time.Duration(ms)*time.Millisecond, 5*time.Minute)
		}
		s.audit("ami.originate", ext+" to "+num)
		run := func() (string, error) { return p.Originate(ext, num, timeout) }
		if strings.EqualFold(m.get("Async"), "true") || m.get("Async") == "1" || strings.EqualFold(m.get("Async"), "yes") {
			reply("Message", "Originate successfully queued")
			go func() {
				callID, err := run()
				resp, reason := "Success", "4"
				if err != nil {
					resp, reason = "Failure", "0"
				}
				s.send("Event", "OriginateResponse", "ActionID", id, "Response", resp, "Channel", m.get("Channel"),
					"Exten", num, "Reason", reason, "Uniqueid", callID+"-1")
			}()
			return true
		}
		if _, err := run(); err != nil {
			fail("Originate failed: " + err.Error())
			return true
		}
		reply("Message", "Originate successfully queued")
	case "listcommands":
		reply("Ping", "Check the server is alive", "Events", "Turn events on or off", "Filter", "Keep or drop events",
			"CoreShowChannels", "List channels", "Status", "List channels", "PJSIPShowEndpoints", "List endpoints",
			"ExtensionState", "Check an extension's state", "ExtensionStateList", "List extension states", "DeviceStateList", "List device states",
			"QueueStatus", "Show queues", "QueueSummary", "Summarize queues", "QueuePause", "Pause or unpause an agent",
			"QueueAdd", "Sign an agent in", "QueueRemove", "Sign an agent out", "Agents", "List agents", "ParkedCalls", "List parked calls",
			"ConfbridgeListRooms", "List conference rooms", "ConfbridgeList", "List a room's members", "ConfbridgeKick", "Remove a member",
			"ConfbridgeMute", "Mute a member", "ConfbridgeUnmute", "Unmute a member", "ConfbridgeLock", "Lock a room", "ConfbridgeUnlock", "Unlock a room",
			"MailboxCount", "Count a mailbox's messages", "MailboxStatus", "Say whether messages wait", "PauseMonitor", "Pause a call's recording",
			"UnpauseMonitor", "Resume a call's recording", "Getvar", "Read a channel variable", "CoreStatus", "Show core status", "CoreSettings", "Show core settings",
			"UserEvent", "Send an event to every client", "Reload", "Reload (nothing to do)",
			"Hangup", "Hang up a channel", "Redirect", "Transfer a channel", "Originate", "Place a call", "Logoff", "Log off")
	default:
		if s.more(action, m, id, reply, fail, write) {
			return true
		}
		fail("Invalid/unknown command: " + m.get("Action") + ". ListCommands lists what BitVoice supports")
	}
	return true
}

func (s *session) audit(action, detail string) {
	if s.a.Audit != nil {
		s.a.Audit(s.user.Username, action, detail)
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func boolNum(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// agentStatus maps an agent state to Asterisk's device state numbers.
func agentStatus(s string) string {
	switch s {
	case "available", "idle":
		return "1"
	case "busy", "ringing":
		return "2"
	case "offline":
		return "5"
	}
	return "0"
}

func queueParams(q pbx.QueueStatus, id string) []string {
	return []string{"Event", "QueueParams", "ActionID", id, "Queue", q.Number, "Max", "0", "Strategy", "",
		"Calls", strconv.Itoa(len(q.Waiting)), "Holdtime", strconv.Itoa(q.Totals.LongestWait),
		"Completed", strconv.Itoa(q.Totals.Answered), "Abandoned", strconv.Itoa(q.Totals.Abandoned)}
}

// prefixed names another channel's fields as Asterisk does for the far side (DestChannel, DestUniqueid).
func prefixed(p string, kv []string) []string {
	out := make([]string, len(kv))
	for i := 0; i < len(kv); i += 2 {
		out[i], out[i+1] = p+kv[i], kv[i+1]
	}
	return out
}

func channelFields(ch Channel, c pbx.CallInfo) []string {
	dur := int(time.Since(c.Start).Seconds())
	return []string{"Channel", ch.Name, "ChannelState", strconv.Itoa(ch.State), "ChannelStateDesc", StateName(ch.State),
		"CallerIDNum", ch.CallerNum, "CallerIDName", ch.CallerName, "ConnectedLineNum", ch.ConnectedNum,
		"ConnectedLineName", ch.ConnectedName, "Language", "en", "AccountCode", "", "Context", "from-internal",
		"Exten", ch.Exten, "Priority", "1", "Uniqueid", ch.ID, "Linkedid", c.ID + "-1",
		"Application", "Dial", "ApplicationData", "", "Duration", fmt.Sprintf("%02d:%02d:%02d", dur/3600, dur/60%60, dur%60),
		"BridgeId", map[bool]string{true: c.ID, false: ""}[ch.State == 6]}
}

// stream sends channel events while the session lasts.
func (s *session) stream(ctx context.Context) {
	sub := s.a.Bus.Subscribe()
	defer s.a.Bus.Unsubscribe(sub)
	tr := NewTracker(s.a.PBX.Calls())
	peers := map[string]string{}
	for _, e := range Endpoints(s.a.PBX) {
		peers[e.Resource] = map[bool]string{true: "Reachable", false: "Unreachable"}[e.State == "online"]
	}
	held := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if !s.events {
				continue
			}
			c, ok := ev.Data.(pbx.CallInfo)
			if !ok {
				s.moreEvents(ev.Data, ev.Type, peers)
				continue
			}
			for _, ch := range tr.Update(c, ev.Type == "call.ended") {
				f := channelFields(ch.Ch, c)
				caller := channelFields(Channels(c)[0], c)
				switch ch.Kind {
				case "new":
					s.event(append([]string{"Event", "Newchannel", "Privilege", "call,all"}, f...)...)
					if ch.Ch.Side == "callee" { // the caller's side starts dialling this one
						s.event(append(append([]string{"Event", "DialBegin", "Privilege", "call,all"}, caller...), prefixed("Dest", f)...)...)
						if ch.Ch.State == 6 { // seen first already answered
							s.event(append(append(append([]string{"Event", "DialEnd", "Privilege", "call,all"}, caller...), prefixed("Dest", f)...), "DialStatus", "ANSWER")...)
						}
					}
				case "state":
					s.event(append([]string{"Event", "Newstate", "Privilege", "call,all"}, f...)...)
					if ch.Ch.Side == "callee" && ch.Ch.State == 6 {
						s.event(append(append(append([]string{"Event", "DialEnd", "Privilege", "call,all"}, caller...), prefixed("Dest", f)...), "DialStatus", "ANSWER")...)
					}
				case "gone":
					if ch.Ch.Side == "callee" && ch.Ch.State != 6 {
						s.event(append(append(append([]string{"Event", "DialEnd", "Privilege", "call,all"}, caller...), prefixed("Dest", f)...), "DialStatus", "NOANSWER")...)
					}
					s.event(append([]string{"Event", "Hangup", "Privilege", "call,all"}, append(f, "Cause", "16", "Cause-txt", "Normal Clearing")...)...)
				}
			}
			// Hold and unhold, from the call's state.
			if was, now := held[c.ID], c.State == "held"; was != now && ev.Type != "call.ended" {
				held[c.ID] = now
				s.event(append([]string{"Event", map[bool]string{true: "Hold", false: "Unhold"}[now], "Privilege", "call,all"}, channelFields(Channels(c)[0], c)...)...)
			}
			if ev.Type == "call.ended" {
				delete(held, c.ID)
			}
		}
	}
}
