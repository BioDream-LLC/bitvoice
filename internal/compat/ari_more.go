package compat

// The rest of the ARI surface, beside the channels, bridges, playbacks and recordings of ari.go and
// stasis.go: Asterisk's global variables and ping, applications' subscriptions and event filters,
// device states, mailboxes, sounds, sending texts to endpoints, user events, and on channels in an
// application mute, hold, music on hold, silence, snoop, move, create and dial, and RTP statistics.
// What BitVoice has no use for (loadable modules, Sorcery's dynamic configuration, log channels,
// video sources) answers 501 with the reason, as listed in notOffered.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// MorePBX is what the rest of the ARI needs from the engine beyond PBX; *pbx.Engine has it all.
type MorePBX interface {
	MailboxCounts(box string) (fresh, old int, ok bool)
	SendText(ext, fromName, text string) int
	HoldMusicPCM() []int16
	CallIntoAppAs(id, ext, app string, args []string, callerName string, ring time.Duration, onStatus func(string)) (string, error)
}

// SoundLister lists the sounds BitVoice can play (ARI's /sounds): prompts by key, with the
// languages each is recorded in.
type SoundLister interface {
	ARISounds() []pbx.ARISound
}

// moreChannel is what mute, hold and snoop need of a channel in an application (*pbx.AppChannel).
type moreChannel interface {
	Mute(in, out, on bool)
	Muted() (in, out bool)
	Music(on bool) error
	Silence()
	RTPStats() (rx, sent media.Stats)
	Tap(f func(pcm []int16)) (stop func())
	PauseRecording(on bool)
	MuteRecording(on bool)
}

// notOffered are the operations BitVoice does not have, with why: they answer 501 with the reason.
var notOffered = []struct{ method, path, why string }{
	{"GET", "asterisk/config/dynamic/", "BitVoice is configured in its web interface and API; it has no Sorcery objects"},
	{"PUT", "asterisk/config/dynamic/", "BitVoice is configured in its web interface and API; it has no Sorcery objects"},
	{"DELETE", "asterisk/config/dynamic/", "BitVoice is configured in its web interface and API; it has no Sorcery objects"},
	{"POST", "asterisk/modules/", "BitVoice is one program with everything built in: there are no modules to load"},
	{"PUT", "asterisk/modules/", "BitVoice is one program with everything built in: there are no modules to reload"},
	{"DELETE", "asterisk/modules/", "BitVoice is one program with everything built in: there are no modules to unload"},
	{"POST", "asterisk/logging/", "BitVoice keeps one log; its level is set in Settings"},
	{"DELETE", "asterisk/logging/", "BitVoice keeps one log; its level is set in Settings"},
	{"PUT", "asterisk/logging/", "BitVoice's log is rotated by the system it runs on"},
	{"POST", "bridges/*/videoSource/", "BitVoice's ARI bridges carry sound only"},
	{"DELETE", "bridges/*/videoSource", "BitVoice's ARI bridges carry sound only"},
	{"POST", "endpoints/refer", "BitVoice transfers calls itself; it does not send REFER to phones for programs"},
	{"POST", "endpoints/*/*/refer", "BitVoice transfers calls itself; it does not send REFER to phones for programs"},
	{"POST", "events/claim", "BitVoice hands each call to one application; it has no call broadcast to claim from"},
}

// notOfferedFor returns why an operation is not offered, or "".
func notOfferedFor(method, path string) string {
	for _, n := range notOffered {
		if n.method != method {
			continue
		}
		pat := strings.Split(strings.TrimSuffix(n.path, "/"), "/")
		got := strings.Split(path, "/")
		prefix := strings.HasSuffix(n.path, "/")
		if len(got) < len(pat) || (!prefix && len(got) != len(pat)) {
			continue
		}
		ok := true
		for i, p := range pat {
			if p != "*" && p != got[i] {
				ok = false
				break
			}
		}
		if ok {
			return n.why
		}
	}
	return ""
}

// moreState is the state of the rest of the ARI.
type moreState struct {
	mu        sync.Mutex
	globals   map[string]string
	custom    map[string]string            // Custom:NAME device states
	mailboxes map[string][2]int            // mailboxes set by programs: new, old
	subs      map[string]map[string]bool   // application -> event sources it subscribed to
	allowed   map[string][]map[string]any  // application -> event filter
	disallow  map[string][]map[string]any  // application -> event filter
	created   map[string]*createdChannel   // channels made with POST /channels/create, not dialled yet
	snoops    map[string]*snoopChannel     // snoop channels by id
	bridgeVar map[string]map[string]string // bridge -> variables
	bridgeMOH map[string]chan struct{}     // bridge -> its hold music, stopped by closing
	lastState map[string]string            // device -> state last told to programs
}

type createdChannel struct {
	ID, Endpoint, App, CallerID string
	Args                        []string
	Created                     time.Time
	State                       string
}

func (a *ARI) more() *moreState {
	a.moreOnce.Do(func() {
		a.moreSt = &moreState{globals: map[string]string{}, custom: map[string]string{}, mailboxes: map[string][2]int{},
			subs: map[string]map[string]bool{}, allowed: map[string][]map[string]any{}, disallow: map[string][]map[string]any{},
			created: map[string]*createdChannel{}, snoops: map[string]*snoopChannel{}, bridgeVar: map[string]map[string]string{},
			bridgeMOH: map[string]chan struct{}{}, lastState: map[string]string{}}
	})
	return a.moreSt
}

// filtered says whether an application's event filter keeps an event of a type out.
func (a *ARI) filtered(app, typ string) bool {
	m := a.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	match := func(list []map[string]any) bool {
		for _, f := range list {
			if f["type"] == typ {
				return true
			}
		}
		return false
	}
	if al := m.allowed[app]; len(al) > 0 && !match(al) {
		return true
	}
	return match(m.disallow[app])
}

func (a *ARI) morePBX(w http.ResponseWriter) MorePBX {
	mp, ok := a.PBX.(MorePBX)
	if !ok {
		ariError(w, http.StatusNotImplemented, "not available here")
		return nil
	}
	return mp
}

// moreRoutes serves the operations of this file; false when the request is not one of them.
func (a *ARI) moreRoutes(w http.ResponseWriter, r *http.Request, u *config.CompatUser, path string, parts []string) bool {
	if why := notOfferedFor(r.Method, path); why != "" {
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice: "+why)
		return true
	}
	q := r.URL.Query()
	m := a.more()
	switch {
	case path == "asterisk/ping" && r.Method == http.MethodGet:
		ariJSON(w, map[string]any{"asterisk_id": "bitvoice", "ping": "pong", "timestamp": stamp()})
	case path == "asterisk/variable" && r.Method == http.MethodGet:
		name := q.Get("variable")
		if name == "" {
			ariError(w, http.StatusBadRequest, "variable is required")
			return true
		}
		m.mu.Lock()
		v := m.globals[name]
		m.mu.Unlock()
		ariJSON(w, map[string]string{"value": v})
	case path == "asterisk/variable" && r.Method == http.MethodPost:
		name := q.Get("variable")
		if name == "" || len(name) > 200 {
			ariError(w, http.StatusBadRequest, "variable is required")
			return true
		}
		m.mu.Lock()
		if len(m.globals) < 10000 || m.globals[name] != "" {
			m.globals[name] = q.Get("value")
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case path == "asterisk/modules" && r.Method == http.MethodGet:
		ariJSON(w, modules())
	case len(parts) == 3 && parts[0] == "asterisk" && parts[1] == "modules" && r.Method == http.MethodGet:
		for _, x := range modules() {
			if x["name"] == parts[2] {
				ariJSON(w, x)
				return true
			}
		}
		ariError(w, http.StatusNotFound, "Module could not be found in running modules")
	case path == "asterisk/logging" && r.Method == http.MethodGet:
		ariJSON(w, []map[string]string{{"channel": "bitvoice", "type": "log", "status": "enabled", "configuration": "notice,warning,error"}})
	case parts[0] == "applications" && len(parts) == 3 && parts[2] == "subscription":
		a.subscription(w, r, parts[1])
	case parts[0] == "applications" && len(parts) == 3 && parts[2] == "eventFilter" && r.Method == http.MethodPut:
		a.eventFilter(w, r, parts[1])
	case parts[0] == "deviceStates":
		a.deviceStates(w, r, u, parts)
	case parts[0] == "mailboxes":
		a.mailboxes(w, r, u, parts)
	case parts[0] == "sounds" && r.Method == http.MethodGet:
		a.sounds(w, r, parts)
	case parts[0] == "endpoints" && (path == "endpoints/sendMessage" || len(parts) == 4 && parts[3] == "sendMessage") && r.Method == http.MethodPut:
		a.sendMessage(w, r, u, parts)
	case parts[0] == "endpoints" && len(parts) == 2 && r.Method == http.MethodGet:
		out := []map[string]any{}
		if strings.EqualFold(parts[1], "PJSIP") || strings.EqualFold(parts[1], "SIP") {
			for _, e := range Endpoints(a.PBX) {
				out = append(out, map[string]any{"technology": "PJSIP", "resource": e.Resource, "state": e.State, "channel_ids": nonNil(e.Channels)})
			}
		} else {
			ariError(w, http.StatusNotFound, "Endpoints not found: BitVoice's phones are PJSIP")
			return true
		}
		ariJSON(w, out)
	case len(parts) == 3 && parts[0] == "events" && parts[1] == "user" && r.Method == http.MethodPost:
		a.userEvent(w, r, parts[2])
	case path == "channels/create" && r.Method == http.MethodPost:
		a.createChannel(w, r, u)
	case len(parts) == 2 && parts[0] == "channels" && r.Method == http.MethodPost && parts[1] != "externalMedia" && parts[1] != "create":
		// Originate with the channel's id chosen: as POST /channels with channelId.
		q.Set("channelId", parts[1])
		r.URL.RawQuery = q.Encode()
		if q.Get("app") != "" {
			a.originateIntoApp(w, r, u)
			return true
		}
		return false
	case len(parts) >= 2 && parts[0] == "channels" && a.createdRoute(w, r, u, parts):
	case len(parts) >= 2 && parts[0] == "channels" && a.snoopRoute(w, r, u, parts):
	case len(parts) >= 3 && parts[0] == "channels" && a.moreChannelRoute(w, r, u, parts):
	case len(parts) >= 3 && parts[0] == "bridges" && a.moreBridgeRoute(w, r, u, parts):
	case len(parts) == 3 && parts[0] == "playbacks" && parts[2] == "control" && r.Method == http.MethodPost:
		a.playbackControl(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "recordings" && parts[1] == "stored" && parts[3] == "copy" && r.Method == http.MethodPost:
		a.copyRecording(w, r, u, parts[2])
	case len(parts) == 4 && parts[0] == "recordings" && parts[1] == "live" && (parts[3] == "pause" || parts[3] == "mute"):
		a.liveControl(w, r, parts[2], parts[3])
	default:
		return false
	}
	return true
}

// modules are BitVoice's parts named as the Asterisk modules whose work they do, for tools that check
// a module is loaded before using it.
func modules() []map[string]any {
	mod := func(name, desc string) map[string]any {
		return map[string]any{"name": name, "description": desc, "use_count": 0, "status": "Running", "support_level": "core"}
	}
	return []map[string]any{
		mod("res_ari.so", "Asterisk RESTful Interface (BitVoice)"), mod("res_stasis.so", "Stasis application support (BitVoice)"),
		mod("res_agi.so", "Asterisk Gateway Interface (BitVoice)"), mod("res_pjsip.so", "SIP (BitVoice)"),
		mod("app_voicemail.so", "Voicemail (BitVoice)"), mod("app_queue.so", "Queues (BitVoice)"),
		mod("app_confbridge.so", "Conference rooms (BitVoice)"), mod("res_musiconhold.so", "Hold music (BitVoice)"),
		mod("manager", "Asterisk Manager Interface (BitVoice)"),
	}
}

// appJSON is an application as ARI describes it.
func (a *ARI) appJSON(name string) map[string]any {
	s := a.st()
	m := a.more()
	s.mu.Lock()
	chans, bridges := []string{}, []string{}
	for id, c := range s.chans {
		if c.app == name {
			chans = append(chans, id)
			if c.bridge != "" && !contains(bridges, c.bridge) {
				bridges = append(bridges, c.bridge)
			}
		}
	}
	connected := len(s.conns[name]) > 0
	s.mu.Unlock()
	endpoints, devices := []string{}, []string{}
	m.mu.Lock()
	for src := range m.subs[name] {
		kind, what, _ := strings.Cut(src, ":")
		switch kind {
		case "channel":
			if !contains(chans, what) {
				chans = append(chans, what)
			}
		case "bridge":
			if !contains(bridges, what) {
				bridges = append(bridges, what)
			}
		case "endpoint":
			endpoints = append(endpoints, what)
		case "deviceState":
			devices = append(devices, what)
		}
	}
	allowed := append([]map[string]any{}, m.allowed[name]...)
	disallowed := append([]map[string]any{}, m.disallow[name]...)
	m.mu.Unlock()
	for _, l := range [][]string{chans, bridges, endpoints, devices} {
		sort.Strings(l)
	}
	return map[string]any{"name": name, "channel_ids": chans, "bridge_ids": bridges, "endpoint_ids": endpoints, "device_names": devices,
		"events_allowed": allowed, "events_disallowed": disallowed, "connected": connected}
}

func (a *ARI) subscription(w http.ResponseWriter, r *http.Request, app string) {
	if a.PBX.Config().FindARIApp(app) == nil {
		ariError(w, http.StatusNotFound, "Application does not exist")
		return
	}
	var sources []string
	for _, v := range r.URL.Query()["eventSource"] {
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				sources = append(sources, x)
			}
		}
	}
	if len(sources) == 0 {
		ariError(w, http.StatusBadRequest, "eventSource is required (channel:{id}, bridge:{id}, endpoint:{tech}[/{resource}], deviceState:{name})")
		return
	}
	for _, src := range sources {
		kind, what, ok := strings.Cut(src, ":")
		if !ok || what == "" || !contains([]string{"channel", "bridge", "endpoint", "deviceState"}, kind) {
			ariError(w, http.StatusBadRequest, "Invalid event source "+src)
			return
		}
	}
	m := a.more()
	m.mu.Lock()
	if m.subs[app] == nil {
		m.subs[app] = map[string]bool{}
	}
	for _, src := range sources {
		if r.Method == http.MethodPost {
			m.subs[app][src] = true
		} else if r.Method == http.MethodDelete {
			if !m.subs[app][src] {
				m.mu.Unlock()
				ariError(w, http.StatusConflict, "Application not subscribed to event source "+src)
				return
			}
			delete(m.subs[app], src)
		}
	}
	m.mu.Unlock()
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		ariError(w, http.StatusMethodNotAllowed, "POST or DELETE")
		return
	}
	ariJSON(w, a.appJSON(app))
}

func (a *ARI) eventFilter(w http.ResponseWriter, r *http.Request, app string) {
	if a.PBX.Config().FindARIApp(app) == nil {
		ariError(w, http.StatusNotFound, "Application does not exist")
		return
	}
	var in struct {
		Allowed    []map[string]any `json:"allowed"`
		Disallowed []map[string]any `json:"disallowed"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			ariError(w, http.StatusBadRequest, "the body is {\"allowed\": [{\"type\": \"...\"}], \"disallowed\": [...]}")
			return
		}
	}
	for _, list := range [][]map[string]any{in.Allowed, in.Disallowed} {
		for _, f := range list {
			if _, ok := f["type"].(string); !ok {
				ariError(w, http.StatusBadRequest, "each filter names an event type: {\"type\": \"StasisStart\"}")
				return
			}
		}
	}
	m := a.more()
	m.mu.Lock()
	m.allowed[app], m.disallow[app] = in.Allowed, in.Disallowed
	m.mu.Unlock()
	ariJSON(w, a.appJSON(app))
}

// deviceState is an endpoint's state as Asterisk names device states.
func deviceState(e Endpoint, ringing map[string]bool) string {
	switch {
	case e.State != "online" && len(e.Channels) == 0:
		return "UNAVAILABLE"
	case ringing[e.Resource]:
		return "RINGING"
	case len(e.Channels) > 0:
		return "INUSE"
	}
	return "NOT_INUSE"
}

var deviceStates = []string{"UNKNOWN", "NOT_INUSE", "INUSE", "BUSY", "INVALID", "UNAVAILABLE", "RINGING", "RINGINUSE", "ONHOLD"}

// devices lists every device state: the phones (PJSIP/100) and the custom ones programs set.
func (a *ARI) devices() []map[string]string {
	ringing := map[string]bool{}
	for _, c := range a.PBX.Calls() {
		for _, ch := range Channels(c) {
			if ch.State == 5 || ch.State == 4 {
				ringing[ch.CallerNum] = true
			}
		}
	}
	out := []map[string]string{}
	for _, e := range Endpoints(a.PBX) {
		out = append(out, map[string]string{"name": "PJSIP/" + e.Resource, "state": deviceState(e, ringing)})
	}
	m := a.more()
	m.mu.Lock()
	for name, st := range m.custom {
		out = append(out, map[string]string{"name": name, "state": st})
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i]["name"] < out[j]["name"] })
	return out
}

func (a *ARI) deviceStates(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) {
	m := a.more()
	if len(parts) == 1 && r.Method == http.MethodGet {
		ariJSON(w, a.devices())
		return
	}
	name := strings.Join(parts[1:], "/") // PJSIP/100 arrives as two parts
	switch r.Method {
	case http.MethodGet:
		for _, d := range a.devices() {
			if d["name"] == name {
				ariJSON(w, d)
				return
			}
		}
		ariError(w, http.StatusNotFound, "Device name is missing")
	case http.MethodPut, http.MethodDelete:
		if !strings.HasPrefix(name, "Custom:") || len(name) < 8 || len(name) > 100 {
			ariError(w, http.StatusConflict, "Uncontrolled device specified: programs set only Custom: devices")
			return
		}
		st := "UNKNOWN"
		if r.Method == http.MethodPut {
			st = strings.ToUpper(r.URL.Query().Get("deviceState"))
			if !contains(deviceStates, st) {
				ariError(w, http.StatusBadRequest, "deviceState is one of "+strings.Join(deviceStates, ", "))
				return
			}
		}
		m.mu.Lock()
		_, had := m.custom[name]
		if r.Method == http.MethodDelete {
			if !had {
				m.mu.Unlock()
				ariError(w, http.StatusNotFound, "Device name is missing")
				return
			}
			delete(m.custom, name)
		} else {
			if !had && len(m.custom) >= 10000 {
				m.mu.Unlock()
				ariError(w, http.StatusConflict, "too many custom devices")
				return
			}
			m.custom[name] = st
		}
		m.mu.Unlock()
		a.audit(u, "ari.devicestate", name+" "+st)
		a.deviceChanged(name, st)
		w.WriteHeader(http.StatusNoContent)
	default:
		ariError(w, http.StatusMethodNotAllowed, "GET, PUT or DELETE")
	}
}

// deviceChanged tells the applications subscribed to a device (or to every device) of its new state.
func (a *ARI) deviceChanged(name, state string) {
	m := a.more()
	m.mu.Lock()
	if m.lastState[name] == state {
		m.mu.Unlock()
		return
	}
	m.lastState[name] = state
	var apps []string
	for app, subs := range m.subs {
		if subs["deviceState:"+name] {
			apps = append(apps, app)
		}
	}
	m.mu.Unlock()
	for _, app := range apps {
		a.emit(app, map[string]any{"type": "DeviceStateChanged", "device_state": map[string]string{"name": name, "state": state}})
	}
}

// endpointsChanged tells subscribed applications of phones whose state changed (call and registration events).
func (a *ARI) endpointsChanged() {
	m := a.more()
	m.mu.Lock()
	someone := false
	for _, subs := range m.subs {
		if len(subs) > 0 {
			someone = true
			break
		}
	}
	m.mu.Unlock()
	if !someone {
		return
	}
	ringing := map[string]bool{}
	for _, c := range a.PBX.Calls() {
		for _, ch := range Channels(c) {
			if ch.State == 5 || ch.State == 4 {
				ringing[ch.CallerNum] = true
			}
		}
	}
	for _, e := range Endpoints(a.PBX) {
		name := "PJSIP/" + e.Resource
		st := deviceState(e, ringing)
		m.mu.Lock()
		changed := m.lastState[name] != st
		var apps []string
		if changed {
			m.lastState[name] = st
			for app, subs := range m.subs {
				if subs["endpoint:PJSIP/"+e.Resource] || subs["endpoint:PJSIP"] {
					apps = append(apps, app)
				}
			}
		}
		devApps := []string{}
		if changed {
			for app, subs := range m.subs {
				if subs["deviceState:"+name] {
					devApps = append(devApps, app)
				}
			}
		}
		m.mu.Unlock()
		for _, app := range apps {
			a.emit(app, map[string]any{"type": "EndpointStateChange", "endpoint": map[string]any{"technology": "PJSIP", "resource": e.Resource,
				"state": e.State, "channel_ids": nonNil(e.Channels)}})
		}
		for _, app := range devApps {
			a.emit(app, map[string]any{"type": "DeviceStateChanged", "device_state": map[string]string{"name": name, "state": st}})
		}
	}
}

// mailboxName is a BitVoice mailbox as Asterisk names it: 100@default.
func mailboxBox(name string) string {
	box, _, _ := strings.Cut(name, "@")
	return box
}

func (a *ARI) mailboxes(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) {
	mp := a.morePBX(w)
	if mp == nil {
		return
	}
	m := a.more()
	list := func() []map[string]any {
		out := []map[string]any{}
		for _, x := range a.PBX.Config().Extensions {
			if fresh, old, ok := mp.MailboxCounts(x.Number); ok {
				out = append(out, map[string]any{"name": x.Number + "@default", "new_messages": fresh, "old_messages": old})
			}
		}
		m.mu.Lock()
		for name, c := range m.mailboxes {
			out = append(out, map[string]any{"name": name, "new_messages": c[0], "old_messages": c[1]})
		}
		m.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
		return out
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		ariJSON(w, list())
		return
	}
	if len(parts) != 2 {
		ariError(w, http.StatusNotFound, "Mailbox not found")
		return
	}
	name := parts[1]
	own := false
	if _, _, ok := mp.MailboxCounts(mailboxBox(name)); ok && (name == mailboxBox(name) || strings.HasSuffix(name, "@default")) {
		own = true
	}
	switch r.Method {
	case http.MethodGet:
		for _, x := range list() {
			if x["name"] == name || own && x["name"] == mailboxBox(name)+"@default" {
				ariJSON(w, x)
				return
			}
		}
		ariError(w, http.StatusNotFound, "Mailbox not found")
	case http.MethodPut:
		if own {
			ariError(w, http.StatusConflict, "BitVoice keeps this mailbox's counts itself: it is extension "+mailboxBox(name)+"'s voicemail")
			return
		}
		fresh, err1 := strconv.Atoi(r.URL.Query().Get("newMessages"))
		old, err2 := strconv.Atoi(r.URL.Query().Get("oldMessages"))
		if err1 != nil || err2 != nil || fresh < 0 || old < 0 || len(name) > 100 {
			ariError(w, http.StatusBadRequest, "oldMessages and newMessages are counts")
			return
		}
		m.mu.Lock()
		if _, had := m.mailboxes[name]; !had && len(m.mailboxes) >= 10000 {
			m.mu.Unlock()
			ariError(w, http.StatusConflict, "too many mailboxes")
			return
		}
		m.mailboxes[name] = [2]int{fresh, old}
		m.mu.Unlock()
		a.audit(u, "ari.mailbox", name)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		m.mu.Lock()
		_, had := m.mailboxes[name]
		delete(m.mailboxes, name)
		m.mu.Unlock()
		if !had {
			ariError(w, http.StatusNotFound, "Mailbox not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		ariError(w, http.StatusMethodNotAllowed, "GET, PUT or DELETE")
	}
}

func (a *ARI) sounds(w http.ResponseWriter, r *http.Request, parts []string) {
	sl, ok := a.PBX.(SoundLister)
	if !ok {
		ariJSON(w, []any{})
		return
	}
	lang := r.URL.Query().Get("lang")
	sound := func(s pbx.ARISound) map[string]any {
		formats := []map[string]string{}
		for _, l := range s.Languages {
			formats = append(formats, map[string]string{"language": l, "format": "wav"})
		}
		out := map[string]any{"id": s.ID, "formats": formats}
		if s.Text != "" {
			out["text"] = s.Text
		}
		return out
	}
	all := sl.ARISounds()
	if len(parts) == 2 {
		for _, s := range all {
			if s.ID == parts[1] {
				ariJSON(w, sound(s))
				return
			}
		}
		ariError(w, http.StatusNotFound, "Sound not found")
		return
	}
	out := []map[string]any{}
	for _, s := range all {
		if lang != "" && !contains(s.Languages, lang) {
			continue
		}
		out = append(out, sound(s))
	}
	ariJSON(w, out)
}

// sendMessage sends a text to a phone (PUT /endpoints/sendMessage?to=pjsip:100 or .../PJSIP/100/sendMessage).
func (a *ARI) sendMessage(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) {
	mp := a.morePBX(w)
	if mp == nil {
		return
	}
	q := r.URL.Query()
	to := ""
	if len(parts) == 4 {
		to = parts[2]
	} else {
		t := q.Get("to")
		if _, rest, ok := strings.Cut(t, ":"); ok {
			t = rest
		}
		to = Extension(strings.TrimPrefix(t, "//"))
		if i := strings.IndexByte(to, '@'); i >= 0 {
			to = to[:i]
		}
	}
	body := q.Get("body")
	if body == "" && r.ContentLength > 0 {
		var in struct {
			Variables map[string]string `json:"variables"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in)
		body = in.Variables["body"]
	}
	if to == "" || body == "" || len(body) > 2000 {
		ariError(w, http.StatusBadRequest, "to (pjsip:100) and body are required")
		return
	}
	if a.PBX.Config().FindExtension(to) == nil {
		ariError(w, http.StatusNotFound, "Endpoint not found")
		return
	}
	from := q.Get("from")
	if _, rest, ok := strings.Cut(from, ":"); ok {
		from = rest
	}
	if mp.SendText(to, from, body) == 0 {
		ariError(w, http.StatusNotFound, "Endpoint not found: no phone of "+to+" is signed in")
		return
	}
	a.audit(u, "ari.sendmessage", to)
	w.WriteHeader(http.StatusAccepted)
}

// userEvent sends a user event to an application (POST /events/user/{eventName}).
func (a *ARI) userEvent(w http.ResponseWriter, r *http.Request, name string) {
	q := r.URL.Query()
	app := q.Get("application")
	if app == "" || a.PBX.Config().FindARIApp(app) == nil {
		ariError(w, http.StatusNotFound, "Application not found")
		return
	}
	vars := map[string]any{}
	if r.ContentLength != 0 {
		var in struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			ariError(w, http.StatusBadRequest, "the body is {\"variables\": {...}}")
			return
		}
		if in.Variables != nil {
			vars = in.Variables
		}
	}
	msg := map[string]any{"type": "ChannelUserevent", "eventname": name, "userevent": vars}
	for _, v := range q["source"] {
		for _, src := range strings.Split(v, ",") {
			kind, what, _ := strings.Cut(strings.TrimSpace(src), ":")
			switch kind {
			case "channel":
				if c := a.st().chanOf(what); c != nil {
					msg["channel"] = a.channelJSON(c.ch, c.app)
				}
			case "bridge":
				a.st().mu.Lock()
				if b := a.st().bridges[what]; b != nil {
					msg["bridge"] = b.json()
				}
				a.st().mu.Unlock()
			case "endpoint":
				msg["endpoint"] = map[string]any{"technology": "PJSIP", "resource": Extension(what), "channel_ids": []string{}}
			case "":
			default:
				ariError(w, http.StatusBadRequest, "Invalid event source "+src)
				return
			}
		}
	}
	a.emit(app, msg)
	w.WriteHeader(http.StatusNoContent)
}

// originateIntoApp is POST /channels (or /channels/{id}) with app: ring an extension; answered, it
// enters the application. Dial events tell the application how the ringing goes.
func (a *ARI) originateIntoApp(w http.ResponseWriter, r *http.Request, u *config.CompatUser) {
	q := r.URL.Query()
	mp := a.morePBX(w)
	if mp == nil {
		return
	}
	ext := Extension(q.Get("endpoint"))
	app := q.Get("app")
	switch {
	case ext == "":
		ariError(w, http.StatusBadRequest, "endpoint (PJSIP/100) is required")
		return
	case a.PBX.Config().FindARIApp(app) == nil:
		ariError(w, http.StatusBadRequest, "no ARI application "+app+" (add it in Settings, Asterisk compatibility)")
		return
	}
	id, call, ok := channelIDOf(q.Get("channelId"))
	if !ok {
		ariError(w, http.StatusBadRequest, "a channel id is letters, digits, dot, - and _, and ends -1 if it says the side")
		return
	}
	if _, _, found := Find(a.PBX, id); found || a.st().chanOf(id) != nil {
		ariError(w, http.StatusConflict, "a channel has this id")
		return
	}
	ring := 30 * time.Second
	if s, err := strconv.Atoi(q.Get("timeout")); err == nil && s > 0 {
		ring = min(time.Duration(s)*time.Second, 5*time.Minute)
	}
	var args []string
	if v := q.Get("appArgs"); v != "" {
		args = strings.Split(v, ",")
	}
	got, err := mp.CallIntoAppAs(call, ext, app, args, q.Get("callerId"), ring, a.dialStatus(app, id, ext))
	if err != nil {
		ariError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(u, "ari.originate", ext+" into "+app)
	ariJSON(w, ariChannel{ID: got, ProtocolID: call, Name: "PJSIP/" + ext, State: "Down", Dialplan: ariDialplan{Context: "default", Exten: "s", Priority: 1, AppName: "Stasis", AppData: app},
		CreationTime: stamp(), Language: "en"})
}

// channelIDOf turns a channel id a program chose into the call's id; "" makes a new one.
func channelIDOf(v string) (channel, call string, ok bool) {
	if v == "" {
		call = config.NewID()
		return call + "-1", call, true
	}
	if !idRe.MatchString(v) {
		return "", "", false
	}
	call = strings.TrimSuffix(v, "-1")
	return call + "-1", call, true
}

// dialStatus emits the Dial events of an originate into an application.
func (a *ARI) dialStatus(app, id, ext string) func(string) {
	return func(status string) {
		peer := ariChannel{ID: id, ProtocolID: strings.TrimSuffix(id, "-1"), Name: "PJSIP/" + ext, State: "Ringing", Caller: ariCallerID{}, Connected: ariCallerID{Number: ext},
			Dialplan: ariDialplan{Context: "default", Exten: "s", Priority: 1, AppName: "Stasis", AppData: app}, CreationTime: stamp(), Language: "en"}
		if status == "ANSWER" {
			peer.State = "Up"
		}
		a.emit(app, map[string]any{"type": "Dial", "dialstatus": status, "dialstring": ext, "peer": peer})
		if status == "ANSWER" {
			a.emit(app, map[string]any{"type": "ChannelStateChange", "channel": peer})
		}
	}
}

// createChannel is POST /channels/create: a channel to an endpoint that waits, Down, for /dial.
func (a *ARI) createChannel(w http.ResponseWriter, r *http.Request, u *config.CompatUser) {
	q := r.URL.Query()
	ext := Extension(q.Get("endpoint"))
	app := q.Get("app")
	switch {
	case ext == "" || a.PBX.Config().FindExtension(ext) == nil:
		ariError(w, http.StatusBadRequest, "endpoint (PJSIP/100, an extension here) is required")
		return
	case a.PBX.Config().FindARIApp(app) == nil:
		ariError(w, http.StatusBadRequest, "no ARI application "+app)
		return
	}
	id, _, ok := channelIDOf(q.Get("channelId"))
	if !ok {
		ariError(w, http.StatusBadRequest, "a channel id is letters, digits, dot, - and _")
		return
	}
	m := a.more()
	m.mu.Lock()
	if _, dup := m.created[id]; dup || len(m.created) >= 1000 {
		m.mu.Unlock()
		ariError(w, http.StatusConflict, "a channel has this id")
		return
	}
	var args []string
	if v := q.Get("appArgs"); v != "" {
		args = strings.Split(v, ",")
	}
	cc := &createdChannel{ID: id, Endpoint: ext, App: app, Args: args, Created: time.Now(), State: "Down"}
	m.created[id] = cc
	m.mu.Unlock()
	// A channel never dialled is dropped after a minute, as nothing would ever use it.
	time.AfterFunc(time.Minute, func() {
		m.mu.Lock()
		if m.created[id] == cc && cc.State == "Down" {
			delete(m.created, id)
		}
		m.mu.Unlock()
	})
	a.audit(u, "ari.create", ext+" in "+app)
	ariJSON(w, a.createdJSON(cc))
}

func (a *ARI) createdJSON(cc *createdChannel) ariChannel {
	return ariChannel{ID: cc.ID, ProtocolID: strings.TrimSuffix(cc.ID, "-1"), Name: "PJSIP/" + cc.Endpoint, State: cc.State, Connected: ariCallerID{Number: cc.Endpoint},
		Dialplan:     ariDialplan{Context: "default", Exten: "s", Priority: 1, AppName: "Stasis", AppData: cc.App},
		CreationTime: cc.Created.Format("2006-01-02T15:04:05.000-0700"), Language: "en"}
}

// createdRoute serves a created channel: GET, DELETE, and POST .../dial.
func (a *ARI) createdRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	m := a.more()
	m.mu.Lock()
	cc := m.created[parts[1]]
	m.mu.Unlock()
	if cc == nil {
		return false
	}
	switch {
	case len(parts) == 2 && r.Method == http.MethodGet:
		m.mu.Lock()
		out := a.createdJSON(cc)
		m.mu.Unlock()
		ariJSON(w, out)
	case len(parts) == 2 && r.Method == http.MethodDelete:
		m.mu.Lock()
		delete(m.created, cc.ID)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[2] == "dial" && r.Method == http.MethodPost:
		mp := a.morePBX(w)
		if mp == nil {
			return true
		}
		ring := 30 * time.Second
		if s, err := strconv.Atoi(r.URL.Query().Get("timeout")); err == nil && s > 0 {
			ring = min(time.Duration(s)*time.Second, 5*time.Minute)
		}
		m.mu.Lock()
		if cc.State != "Down" {
			m.mu.Unlock()
			ariError(w, http.StatusConflict, "the channel has been dialled already")
			return true
		}
		cc.State = "Ringing"
		m.mu.Unlock()
		status := a.dialStatus(cc.App, cc.ID, cc.Endpoint)
		_, err := mp.CallIntoAppAs(strings.TrimSuffix(cc.ID, "-1"), cc.Endpoint, cc.App, cc.Args, "", ring, func(st string) {
			if st != "RINGING" {
				m.mu.Lock()
				delete(m.created, cc.ID)
				m.mu.Unlock()
			}
			status(st)
		})
		if err != nil {
			m.mu.Lock()
			delete(m.created, cc.ID)
			m.mu.Unlock()
			ariError(w, http.StatusConflict, err.Error())
			return true
		}
		a.audit(u, "ari.dial", cc.Endpoint)
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && (parts[2] == "variable" || parts[2] == "variables"):
		ariError(w, http.StatusConflict, "the channel is not up yet: set its variables once it has entered the application")
	default:
		ariError(w, http.StatusConflict, "Channel not in Stasis application: dial it first")
	}
	return true
}

// moreChannelRoute serves the rest of the operations on a channel in an application.
func (a *ARI) moreChannelRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	op := parts[2]
	switch op {
	case "mute", "hold", "moh", "silence", "progress", "variables", "move", "rtp_statistics", "transfer_progress", "snoop", "dial":
	case "ring":
		if r.Method != http.MethodDelete {
			return false // POST .../ring is the application's channel route's
		}
	default:
		return false
	}
	c := a.st().chanOf(parts[1])
	if c == nil {
		if _, _, ok := Find(a.PBX, parts[1]); ok {
			ariError(w, http.StatusConflict, "Channel not in Stasis application")
		} else {
			ariError(w, http.StatusNotFound, "Channel not found")
		}
		return true
	}
	mc, _ := c.ch.(moreChannel)
	q := r.URL.Query()
	need := func() bool {
		if mc == nil {
			ariError(w, http.StatusNotImplemented, "not available for this channel")
			return false
		}
		return true
	}
	post, del := r.Method == http.MethodPost, r.Method == http.MethodDelete
	switch {
	case op == "mute" && (post || del):
		if !need() {
			return true
		}
		dir := q.Get("direction")
		if dir == "" {
			dir = "both"
		}
		if !contains([]string{"both", "in", "out"}, dir) {
			ariError(w, http.StatusBadRequest, "direction is both, in or out")
			return true
		}
		mc.Mute(dir != "out", dir != "in", post)
		w.WriteHeader(http.StatusNoContent)
	case (op == "hold" || op == "moh") && (post || del):
		if !need() {
			return true
		}
		if err := mc.Music(post); err != nil {
			ariError(w, http.StatusConflict, err.Error())
			return true
		}
		if op == "hold" {
			typ := "ChannelHold"
			if del {
				typ = "ChannelUnhold"
			}
			a.emit(c.app, map[string]any{"type": typ, "channel": a.channelJSON(c.ch, c.app)})
		}
		w.WriteHeader(http.StatusNoContent)
	case op == "silence" && (post || del):
		if !need() {
			return true
		}
		if post {
			mc.Silence()
		}
		w.WriteHeader(http.StatusNoContent)
	case (op == "ring" && del) || (op == "progress" && post):
		w.WriteHeader(http.StatusNoContent) // answered as it entered the application: nothing to ring
	case op == "variables" && r.Method == http.MethodGet:
		out := []map[string]string{}
		for _, name := range strings.Split(q.Get("variables"), ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			v, _ := c.ch.Var(name)
			out = append(out, map[string]string{"attribute": name, "value": v})
		}
		ariJSON(w, map[string]any{"variables": out})
	case op == "variables" && post:
		var in struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || len(in.Variables) == 0 {
			ariError(w, http.StatusBadRequest, "the body is {\"variables\": {\"NAME\": \"value\"}}")
			return true
		}
		for k, v := range in.Variables {
			c.ch.SetVar(k, v)
			a.emit(c.app, map[string]any{"type": "ChannelVarset", "variable": k, "value": v, "channel": a.channelJSON(c.ch, c.app)})
		}
		w.WriteHeader(http.StatusNoContent)
	case op == "move" && post:
		a.move(w, r, u, c)
	case op == "rtp_statistics" && r.Method == http.MethodGet:
		if !need() {
			return true
		}
		rx, tx := mc.RTPStats()
		ariJSON(w, map[string]any{"txcount": tx.Packets, "rxcount": rx.Packets, "txploss": 0, "rxploss": rx.Lost,
			"local_ssrc": 0, "remote_ssrc": 0, "txoctetcount": tx.Bytes, "rxoctetcount": rx.Bytes, "channel_uniqueid": c.ch.ID(),
			"rxjitter": rx.JitterMS / 1000})
	case op == "transfer_progress" && post:
		if q.Get("states") == "" {
			ariError(w, http.StatusBadRequest, "states is required")
			return true
		}
		w.WriteHeader(http.StatusNoContent) // BitVoice tells phones of transfers itself
	case op == "snoop" && post:
		id := ""
		if len(parts) == 4 {
			id = parts[3]
		}
		a.snoop(w, r, u, c, id)
	case op == "dial":
		ariError(w, http.StatusConflict, "the channel is up already: dial is for channels made with POST /channels/create")
	default:
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice for a channel in an application: "+r.Method+" "+op)
	}
	return true
}

// move takes a channel to another application (POST /channels/{id}/move).
func (a *ARI) move(w http.ResponseWriter, r *http.Request, u *config.CompatUser, c *appChan) {
	q := r.URL.Query()
	to := q.Get("app")
	args := []string{}
	if v := q.Get("appArgs"); v != "" {
		args = strings.Split(v, ",")
	}
	s := a.st()
	s.mu.Lock()
	connected := len(s.conns[to]) > 0
	s.mu.Unlock()
	if a.PBX.Config().FindARIApp(to) == nil || !connected {
		a.emit(c.app, map[string]any{"type": "ApplicationMoveFailed", "destination": to, "args": args, "channel": a.channelJSON(c.ch, c.app)})
		ariError(w, http.StatusConflict, "no program is connected for application "+to)
		return
	}
	from := c.app
	a.emit(from, map[string]any{"type": "StasisEnd", "channel": a.channelJSON(c.ch, from)})
	s.mu.Lock()
	c.app = to
	s.mu.Unlock()
	if m, ok := c.ch.(interface{ Move(string) }); ok {
		m.Move(to)
	}
	ch := c.ch
	ch.OnKey(func(k byte) {
		a.emit(to, map[string]any{"type": "ChannelDtmfReceived", "digit": string(k), "duration_ms": 100, "channel": a.channelJSON(ch, to)})
	})
	a.emit(to, map[string]any{"type": "StasisStart", "args": args, "channel": a.channelJSON(c.ch, to)})
	a.audit(u, "ari.move", c.ch.ID()+" to "+to)
	w.WriteHeader(http.StatusNoContent)
}

// snoopChannel listens to a channel in an application for another program: what its caller says,
// which it can record (spy in). It is a channel of its own in the application it names.
type snoopChannel struct {
	id, app, target string
	of              *appChan
	created         time.Time
	stop            func()
	mu              sync.Mutex
	recording       *media.Capture
	gone            chan struct{}
	once            sync.Once
}

func (sc *snoopChannel) json() ariChannel {
	return ariChannel{ID: sc.id, ProtocolID: sc.id, Name: "Snoop/" + sc.target, State: "Up",
		Dialplan: ariDialplan{Context: "default", Exten: "s", Priority: 1, AppName: "Stasis", AppData: sc.app}, CreationTime: sc.created.Format("2006-01-02T15:04:05.000-0700"), Language: "en"}
}

func (sc *snoopChannel) end() { sc.once.Do(func() { sc.stop(); close(sc.gone) }) }

func (a *ARI) snoop(w http.ResponseWriter, r *http.Request, u *config.CompatUser, c *appChan, id string) {
	q := r.URL.Query()
	mc, ok := c.ch.(moreChannel)
	if !ok {
		ariError(w, http.StatusNotImplemented, "not available for this channel")
		return
	}
	spy, whisper := q.Get("spy"), q.Get("whisper")
	if spy == "" {
		spy = "none"
	}
	if whisper == "" {
		whisper = "none"
	}
	switch {
	case whisper != "none":
		ariError(w, http.StatusBadRequest, "BitVoice's snoop listens (spy=in); to talk to the caller, bridge a channel with theirs")
		return
	case spy != "in" && spy != "both":
		ariError(w, http.StatusBadRequest, "spy=in (the caller's voice) is what BitVoice's snoop hears; their side of a bridge is the bridge's")
		return
	}
	app := q.Get("app")
	if a.PBX.Config().FindARIApp(app) == nil {
		ariError(w, http.StatusBadRequest, "no ARI application "+app)
		return
	}
	if id == "" {
		id = q.Get("snoopId")
	}
	if id == "" {
		id = config.NewID()
	} else if !idRe.MatchString(id) {
		ariError(w, http.StatusBadRequest, "a snoop id is letters, digits, dot, - and _")
		return
	}
	m := a.more()
	sc := &snoopChannel{id: id, app: app, target: c.ch.ID(), of: c, created: time.Now(), gone: make(chan struct{})}
	m.mu.Lock()
	if _, dup := m.snoops[id]; dup || len(m.snoops) >= 100 {
		m.mu.Unlock()
		ariError(w, http.StatusConflict, "a snoop has this id")
		return
	}
	m.snoops[id] = sc
	m.mu.Unlock()
	sc.stop = mc.Tap(func(pcm []int16) {
		sc.mu.Lock()
		if sc.recording != nil {
			sc.recording.AddPCM(pcm)
		}
		sc.mu.Unlock()
	})
	go func() {
		select {
		case <-c.ch.Gone():
		case <-sc.gone:
		}
		sc.end()
		m.mu.Lock()
		delete(m.snoops, id)
		m.mu.Unlock()
		a.emit(app, map[string]any{"type": "StasisEnd", "channel": sc.json()})
	}()
	args := []string{}
	if v := q.Get("appArgs"); v != "" {
		args = strings.Split(v, ",")
	}
	a.emit(app, map[string]any{"type": "StasisStart", "args": args, "channel": sc.json()})
	a.audit(u, "ari.snoop", c.ch.ID())
	ariJSON(w, sc.json())
}

// snoopRoute serves a snoop channel: GET, DELETE (ends it), and POST .../record.
func (a *ARI) snoopRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	m := a.more()
	m.mu.Lock()
	sc := m.snoops[parts[1]]
	m.mu.Unlock()
	if sc == nil {
		return false
	}
	switch {
	case len(parts) == 2 && r.Method == http.MethodGet:
		ariJSON(w, sc.json())
	case len(parts) == 2 && r.Method == http.MethodDelete:
		sc.end()
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[2] == "record" && r.Method == http.MethodPost:
		a.recordSnoop(w, r, u, sc)
	default:
		ariError(w, http.StatusNotImplemented, "a snoop channel takes GET, DELETE and POST .../record")
	}
	return true
}

// recordSnoop records what a snoop channel hears into the stored recordings.
func (a *ARI) recordSnoop(w http.ResponseWriter, r *http.Request, u *config.CompatUser, sc *snoopChannel) {
	q := r.URL.Query()
	name := q.Get("name")
	path, err := a.recPath(name)
	if err != nil {
		ariError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(path); err == nil && q.Get("ifExists") != "overwrite" {
		ariError(w, http.StatusConflict, "a recording has this name (ifExists=overwrite replaces it)")
		return
	}
	maxDur, _ := strconv.Atoi(q.Get("maxDurationSeconds"))
	if maxDur <= 0 || maxDur > 3600 {
		maxDur = 3600
	}
	s := a.st()
	lr := &liveRec{Name: name, Format: "wav", TargetURI: "channel:" + sc.id, State: "recording", app: sc.app, stop: make(chan struct{}), once: &sync.Once{}}
	s.mu.Lock()
	if _, dup := s.live[name]; dup {
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "a recording of this name is going on")
		return
	}
	s.live[name] = lr
	s.mu.Unlock()
	capt := media.NewCapture(maxDur)
	sc.mu.Lock()
	sc.recording = capt
	sc.mu.Unlock()
	cp := *lr
	a.emit(sc.app, map[string]any{"type": "RecordingStarted", "recording": cp})
	go func() {
		limit := time.NewTimer(time.Duration(maxDur) * time.Second)
		defer limit.Stop()
		select {
		case <-lr.stop:
		case <-sc.gone:
		case <-limit.C:
		}
		sc.mu.Lock()
		sc.recording = nil
		sc.mu.Unlock()
		pcm := capt.Stop()
		s.mu.Lock()
		cancel := lr.cancel
		delete(s.live, name)
		s.mu.Unlock()
		if cancel {
			lr.State = "canceled"
		} else if err := os.MkdirAll(a.Recordings, 0o700); err != nil || os.WriteFile(path, media.EncodeWAV(pcm), 0o600) != nil {
			lr.State, lr.Cause = "failed", "could not save"
		} else {
			lr.State, lr.Duration = "done", len(pcm)/media.Rate
		}
		typ := "RecordingFinished"
		if lr.State == "failed" {
			typ = "RecordingFailed"
		}
		a.emit(sc.app, map[string]any{"type": typ, "recording": *lr})
	}()
	a.audit(u, "ari.record", sc.id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cp)
}

// moreBridgeRoute serves a bridge's variables, hold music, named playbacks and recording.
func (a *ARI) moreBridgeRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	op := parts[2]
	switch op {
	case "variable", "variables", "moh", "record":
	case "play":
		if len(parts) != 4 {
			return false
		}
	default:
		return false
	}
	s := a.st()
	s.mu.Lock()
	b := s.bridges[parts[1]]
	s.mu.Unlock()
	if b == nil {
		ariError(w, http.StatusNotFound, "Bridge not found")
		return true
	}
	m := a.more()
	q := r.URL.Query()
	switch {
	case op == "variable" && r.Method == http.MethodGet:
		m.mu.Lock()
		v, ok := m.bridgeVar[b.id][q.Get("variable")]
		m.mu.Unlock()
		if !ok {
			ariError(w, http.StatusNotFound, "Variable not found")
			return true
		}
		ariJSON(w, map[string]string{"value": v})
	case op == "variable" && r.Method == http.MethodPost:
		if q.Get("variable") == "" {
			ariError(w, http.StatusBadRequest, "variable is required")
			return true
		}
		m.mu.Lock()
		if m.bridgeVar[b.id] == nil {
			m.bridgeVar[b.id] = map[string]string{}
		}
		m.bridgeVar[b.id][q.Get("variable")] = q.Get("value")
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "variables" && r.Method == http.MethodGet:
		out := []map[string]string{}
		m.mu.Lock()
		for _, name := range strings.Split(q.Get("variables"), ",") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, map[string]string{"attribute": name, "value": m.bridgeVar[b.id][name]})
			}
		}
		m.mu.Unlock()
		ariJSON(w, map[string]any{"variables": out})
	case op == "variables" && r.Method == http.MethodPost:
		var in struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || len(in.Variables) == 0 {
			ariError(w, http.StatusBadRequest, "the body is {\"variables\": {\"NAME\": \"value\"}}")
			return true
		}
		m.mu.Lock()
		if m.bridgeVar[b.id] == nil {
			m.bridgeVar[b.id] = map[string]string{}
		}
		for k, v := range in.Variables {
			m.bridgeVar[b.id][k] = v
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "moh" && r.Method == http.MethodPost:
		mp := a.morePBX(w)
		if mp == nil {
			return true
		}
		m.mu.Lock()
		if m.bridgeMOH[b.id] == nil {
			stop := make(chan struct{})
			m.bridgeMOH[b.id] = stop
			go func() {
				for {
					pcm := mp.HoldMusicPCM()
					b.br.Inject(pcm)
					select {
					case <-stop:
						return
					case <-time.After(time.Duration(len(pcm)) * time.Second / media.Rate):
					}
				}
			}()
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "moh" && r.Method == http.MethodDelete:
		m.mu.Lock()
		stop := m.bridgeMOH[b.id]
		delete(m.bridgeMOH, b.id)
		m.mu.Unlock()
		if stop == nil {
			ariError(w, http.StatusConflict, "the bridge has no hold music playing")
			return true
		}
		close(stop)
		b.br.Inject(nil)
		w.WriteHeader(http.StatusNoContent)
	case op == "play" && r.Method == http.MethodPost:
		q.Set("playbackId", parts[3])
		r.URL.RawQuery = q.Encode()
		return a.bridgeRoute(w, r, u, parts[:3])
	case op == "record" && r.Method == http.MethodPost:
		a.recordBridge(w, r, u, b)
	default:
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice for a bridge: "+r.Method+" "+op)
	}
	return true
}

// stopBridgeMusic ends a bridge's hold music, when it goes.
func (a *ARI) stopBridgeMusic(id string) {
	m := a.more()
	m.mu.Lock()
	stop := m.bridgeMOH[id]
	delete(m.bridgeMOH, id)
	delete(m.bridgeVar, id)
	m.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// recordBridge records a bridge's mix into the stored recordings.
func (a *ARI) recordBridge(w http.ResponseWriter, r *http.Request, u *config.CompatUser, b *ariBridge) {
	q := r.URL.Query()
	name := q.Get("name")
	path, err := a.recPath(name)
	if err != nil {
		ariError(w, http.StatusBadRequest, err.Error())
		return
	}
	if f := q.Get("format"); f != "" && f != "wav" {
		ariError(w, http.StatusUnprocessableEntity, "BitVoice records wav")
		return
	}
	if _, err := os.Stat(path); err == nil && q.Get("ifExists") != "overwrite" {
		ariError(w, http.StatusConflict, "a recording has this name (ifExists=overwrite replaces it)")
		return
	}
	maxDur, _ := strconv.Atoi(q.Get("maxDurationSeconds"))
	if maxDur <= 0 || maxDur > 3600 {
		maxDur = 3600
	}
	s := a.st()
	lr := &liveRec{Name: name, Format: "wav", TargetURI: "bridge:" + b.id, State: "recording", stop: make(chan struct{}), once: &sync.Once{}}
	s.mu.Lock()
	if _, dup := s.live[name]; dup {
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "a recording of this name is going on")
		return
	}
	s.live[name] = lr
	s.mu.Unlock()
	capt := media.NewCapture(maxDur)
	b.br.OnMix(func(frame []int16) { capt.AddPCM(frame) })
	cp := *lr
	a.emitAll(map[string]any{"type": "RecordingStarted", "recording": cp})
	go func() {
		limit := time.NewTimer(time.Duration(maxDur) * time.Second)
		defer limit.Stop()
		select {
		case <-lr.stop:
		case <-limit.C:
		}
		b.br.OnMix(nil)
		pcm := capt.Stop()
		s.mu.Lock()
		cancel := lr.cancel
		delete(s.live, name)
		s.mu.Unlock()
		if cancel {
			lr.State = "canceled"
		} else if err := os.MkdirAll(a.Recordings, 0o700); err != nil || os.WriteFile(path, media.EncodeWAV(pcm), 0o600) != nil {
			lr.State, lr.Cause = "failed", "could not save"
		} else {
			lr.State, lr.Duration = "done", len(pcm)/media.Rate
		}
		typ := "RecordingFinished"
		if lr.State == "failed" {
			typ = "RecordingFailed"
		}
		a.emitAll(map[string]any{"type": typ, "recording": *lr})
	}()
	a.audit(u, "ari.record", "bridge "+b.id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cp)
}

// playbackControl is POST /playbacks/{id}/control: pause, unpause, restart, reverse, forward.
func (a *ARI) playbackControl(w http.ResponseWriter, r *http.Request, id string) {
	op := r.URL.Query().Get("operation")
	if !contains([]string{"restart", "pause", "unpause", "reverse", "forward"}, op) {
		ariError(w, http.StatusBadRequest, "operation is restart, pause, unpause, reverse or forward")
		return
	}
	s := a.st()
	s.mu.Lock()
	pb := s.playbacks[id]
	s.mu.Unlock()
	if pb == nil {
		ariError(w, http.StatusNotFound, "Playback not found")
		return
	}
	if pb.control == nil {
		ariError(w, http.StatusConflict, "this playback cannot be controlled (a bridge's)")
		return
	}
	select {
	case pb.control <- op:
		w.WriteHeader(http.StatusNoContent)
	case <-time.After(2 * time.Second):
		ariError(w, http.StatusConflict, "Playback not playing")
	}
}

// copyRecording is POST /recordings/stored/{name}/copy?destinationRecordingName=...
func (a *ARI) copyRecording(w http.ResponseWriter, r *http.Request, u *config.CompatUser, name string) {
	src, err := a.recPath(name)
	if err != nil {
		ariError(w, http.StatusBadRequest, err.Error())
		return
	}
	to := r.URL.Query().Get("destinationRecordingName")
	dst, err := a.recPath(to)
	if err != nil {
		ariError(w, http.StatusBadRequest, "destinationRecordingName: "+err.Error())
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		ariError(w, http.StatusNotFound, "Recording not found")
		return
	}
	if _, err := os.Stat(dst); err == nil {
		ariError(w, http.StatusConflict, "A recording with the same name already exists")
		return
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		ariError(w, http.StatusInternalServerError, "could not copy")
		return
	}
	a.audit(u, "ari.recording.copy", name+" to "+to)
	ariJSON(w, map[string]string{"name": to, "format": "wav"})
}

// liveControl pauses or mutes a live recording on a channel (POST and DELETE .../pause and .../mute).
func (a *ARI) liveControl(w http.ResponseWriter, r *http.Request, name, op string) {
	s := a.st()
	s.mu.Lock()
	lr := s.live[name]
	s.mu.Unlock()
	if lr == nil {
		ariError(w, http.StatusNotFound, "Recording not found")
		return
	}
	id := strings.TrimPrefix(lr.TargetURI, "channel:")
	c := s.chanOf(id)
	var mc moreChannel
	if c != nil {
		mc, _ = c.ch.(moreChannel)
	}
	if mc == nil {
		ariError(w, http.StatusConflict, "Recording not in session: only a channel's recording pauses or mutes")
		return
	}
	on := r.Method == http.MethodPost
	if !on && r.Method != http.MethodDelete {
		ariError(w, http.StatusMethodNotAllowed, "POST or DELETE")
		return
	}
	if op == "pause" {
		mc.PauseRecording(on)
		s.mu.Lock()
		if on {
			lr.State = "paused"
		} else {
			lr.State = "recording"
		}
		s.mu.Unlock()
	} else {
		mc.MuteRecording(on)
	}
	w.WriteHeader(http.StatusNoContent)
}

// buildInfo is AsteriskInfo's build block.
func buildInfo() map[string]any {
	return map[string]any{"os": runtime.GOOS, "kernel": runtime.GOARCH, "options": "", "machine": runtime.GOARCH, "date": "", "user": ""}
}

// storedRecordings lists the stored recordings, newest last.
func (a *ARI) storedRecordings() []map[string]string {
	out := []map[string]string{}
	ents, _ := os.ReadDir(a.Recordings)
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".wav"); ok && !e.IsDir() {
			out = append(out, map[string]string{"name": n, "format": "wav"})
		}
	}
	return out
}

// ARIState is what programs keep in BitVoice through ARI, for the settings screen.
type ARIState struct {
	Applications []map[string]any    `json:"applications"`
	Devices      []map[string]string `json:"devices"`
	Mailboxes    []map[string]any    `json:"mailboxes"`
	Globals      map[string]string   `json:"globals"`
	Bridges      []map[string]any    `json:"bridges"`
}

// State reports the applications (connected or not, their channels and subscriptions), the custom
// device states and mailboxes programs set, the global variables, and the ARI's own bridges.
func (a *ARI) State() ARIState {
	out := ARIState{Applications: []map[string]any{}, Devices: []map[string]string{}, Mailboxes: []map[string]any{}, Globals: map[string]string{}}
	for _, x := range a.PBX.Config().System.Compat.Apps {
		out.Applications = append(out.Applications, a.appJSON(x.Name))
	}
	m := a.more()
	m.mu.Lock()
	for name, st := range m.custom {
		out.Devices = append(out.Devices, map[string]string{"name": name, "state": st})
	}
	for name, c := range m.mailboxes {
		out.Mailboxes = append(out.Mailboxes, map[string]any{"name": name, "new_messages": c[0], "old_messages": c[1]})
	}
	for k, v := range m.globals {
		out.Globals[k] = v
	}
	m.mu.Unlock()
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i]["name"] < out.Devices[j]["name"] })
	sort.Slice(out.Mailboxes, func(i, j int) bool { return out.Mailboxes[i]["name"].(string) < out.Mailboxes[j]["name"].(string) })
	out.Bridges = a.ariBridges()
	return out
}
