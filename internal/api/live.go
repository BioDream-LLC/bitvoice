package api

import (
	"encoding/json"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/cluster"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) liveRoutes() {
	s.handle("GET /api/calls", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		type withBrief struct {
			pbx.CallInfo
			Briefing string `json:"briefing,omitempty"`
		}
		out := []withBrief{}
		for _, c := range s.Engine.Calls() {
			if c.Direction == "cluster" {
				continue // another server has the caller and lists the call under "elsewhere"
			}
			b, _ := s.briefingFor(c.ID)
			out = append(out, withBrief{c, b.Summary})
		}
		type remote struct {
			cluster.Call
			Server string `json:"server"`
		}
		elsewhere := []remote{}
		if s.Cluster != nil {
			for _, n := range s.Cluster.Nodes() {
				if n.Self || !n.Alive {
					continue
				}
				for _, c := range n.Info.Calls {
					elsewhere = append(elsewhere, remote{c, n.Name})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"items": out, "elsewhere": elsewhere})
	})
	s.handle("POST /api/calls/{id}/door", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Engine.OpenDoor(r.PathValue("id"), u.Username); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.audit(r, u.Username, "door.open", r.PathValue("id"))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.handle("DELETE /api/calls/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !s.Engine.Hangup(r.PathValue("id"), firstNonEmpty(u.Name, u.Username)) {
			writeError(w, 404, "no such call")
			return
		}
		s.audit(r, u.Username, "call.hangup", r.PathValue("id"))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.handle("GET /api/queues/status", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Queues()})
	})
	s.handle("GET /api/registrations", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Registrations()})
	})
	s.handle("GET /api/trunks/status", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.TrunkStatus()})
	})
	s.handle("GET /api/stats", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, s.Engine.Stats())
	})
	s.handle("GET /api/security/bans", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.Banned()})
	})
	s.handle("DELETE /api/security/bans/{ip}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.Engine.Unban(r.PathValue("ip"))
		s.audit(r, u.Username, "security.unban", r.PathValue("ip"))
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.handle("GET /api/cdr", false, s.cdrs)
	s.holdsRoutes()
	s.callbacksRoutes()
	s.transcriptRoutes()
	s.crmRoutes()
	s.simulateRoutes()
	s.planCheckRoutes()
	s.qualityRoutes()
	s.metricsRoutes()
	s.costRoutes()
	s.directoryRoutes()
	s.hotDeskRoutes()
	s.conferenceRoutes()
	s.divertRoutes()
	s.briefRoutes()
	s.textRoutes()
	s.timelineRoutes()
	s.callLinkRoutes()
	s.insightRoutes()
	s.awayRoutes()
	s.reputationRoutes()
	s.certificateRoutes()
	s.databaseRoutes()
	s.serverRoutes()
	s.srvRoutes()
	s.mobileRoutes()
	s.emergencyModeRoutes()
	s.automationRoutes()
	s.contactRoutes()
	s.compatibilityRoutes()
	s.locationRoutes()
	s.vaultRoutes()
	s.askRoutes()
	s.handle("GET /api/history", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		h, err := s.Store.History(200)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": h})
	})
	// What restoring a revision would change (against=current, the default), or what the revision itself
	// changed (against=previous).
	s.handle("GET /api/history/{rev}/diff", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		rev, _ := strconv.Atoi(r.PathValue("rev"))
		old, err := s.Store.Revision(rev)
		if err != nil {
			writeError(w, 404, "no revision "+r.PathValue("rev"))
			return
		}
		if r.URL.Query().Get("against") == "previous" {
			prev, err := s.Store.Revision(rev - 1)
			if err != nil {
				prev.Config = config.Config{}
			}
			writeJSON(w, 200, map[string]any{"items": config.Diff(prev.Config, old.Config, 300)})
			return
		}
		cur, _ := s.Store.Config()
		writeJSON(w, 200, map[string]any{"items": config.Diff(cur, old.Config, 300)})
	})
	s.handle("POST /api/history/{rev}/restore", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		rev, _ := strconv.Atoi(r.PathValue("rev"))
		n, err := s.Store.Revert(u.Username, rev)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "config.restore", strconv.Itoa(rev))
		writeJSON(w, 200, map[string]int{"rev": n})
	})
	s.handle("GET /api/config/export", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, rev := s.Store.Config()
		if r.URL.Query().Get("secrets") == "0" {
			config.StripSecrets(&cfg) // for a Git repository: secrets stay here
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="bitvoice-config-rev%d.json"`, rev))
		s.audit(r, u.Username, "config.export", strconv.Itoa(rev))
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(cfg)
	})
	s.handle("POST /api/config/import", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in config.Config
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if r.URL.Query().Get("dryRun") == "1" {
			// Check the file and say what importing it would change, without changing anything.
			check := in.Clone()
			cur0, _ := s.Store.Config()
			config.KeepSecrets(&check, cur0)
			if err := check.Validate(); err != nil {
				writeErr(w, err)
				return
			}
			cur, _ := s.Store.Config()
			writeJSON(w, 200, map[string]any{"items": config.Diff(cur, check, 300)})
			return
		}
		_, rev, err := s.Store.Update(u.Username, "Imported configuration", 0, func(c *config.Config) error {
			config.KeepSecrets(&in, *c) // a file without secrets (from Git) keeps the ones here
			*c = in
			return nil
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "config.import", "")
		writeJSON(w, 200, map[string]int{"rev": rev})
	})
	s.handle("GET /api/audit", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Store.AuditLog(500)})
	})
	s.handle("GET /api/system/upgrade", true, s.getUpgrade)
	s.handle("GET /api/loadtest", true, s.getLoad)
	s.handle("POST /api/loadtest", true, s.startLoad)
	s.handle("POST /api/loadtest/stop", true, s.stopLoad)
	s.handle("POST /api/system/upgrade", true, s.postUpgrade)
	s.handle("POST /api/system/upgrade/apply", true, s.applyUpgrade("switch"))
	s.handle("POST /api/system/upgrade/rollback", true, s.applyUpgrade("rollback"))
	s.handle("POST /api/system/upgrade/cancel", true, s.cancelUpgrade)
	s.handle("GET /api/system", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, rev := s.Store.Config()
		writeJSON(w, 200, map[string]any{"version": s.Version, "name": cfg.System.Name, "rev": rev,
			"addresses": s.Engine.PhoneAddresses(localAddresses()), "publicAddress": s.publicAddress(), "sipPort": s.Engine.SIPPort(), "tlsPort": s.Engine.TLSPort(), "httpsPort": s.HTTPSPort.Load(), "ffmpeg": media.FFmpegHas(s.config().System.FFmpeg), "amiAddress": s.amiAddr(), "uptimeSeconds": int(time.Since(s.started).Seconds()),
			"instanceId": s.Store.State().InstanceID, "tenant": s.Tenant, "hosts": s.Tenancy != nil,
			"publicSafety": cfg.System.PublicSafety || cfg.PublicSafetyInUse() != "", "publicSafetyInUse": cfg.PublicSafetyInUse()})
	})
	s.handle("GET /api/health", false, s.health)
	s.handle("GET /api/speech", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, s.Engine.Speech.Status())
	})
	s.handle("GET /api/sip-trace", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Trace == nil {
			writeJSON(w, 200, map[string]any{"items": []TraceEntry{}})
			return
		}
		writeJSON(w, 200, map[string]any{"items": s.Trace.Search(r.URL.Query().Get("q"), 500)})
	})
	s.handle("GET /api/events", false, s.events)
}

func (s *Server) cdrs(w http.ResponseWriter, r *http.Request, u store.User) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	query := store.CDRQuery{Search: q.Get("search"), Direction: q.Get("direction"), Limit: limit}
	if v := q.Get("since"); v != "" {
		query.Since, _ = time.Parse(time.RFC3339, v)
	}
	items, err := s.Store.CDRs(query)
	if err != nil {
		writeErr(w, err)
		return
	}
	type row struct {
		store.CDR
		Held bool `json:"held,omitempty"`
	}
	out := make([]row, len(items))
	for i, c := range items {
		out[i] = row{c, s.Store.CDRHeld(c)}
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

// events streams live changes as server-sent events. The interface reloads what changed; nothing is polled.
func (s *Server) events(w http.ResponseWriter, r *http.Request, u store.User) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case ev := <-sub.C:
			if ev.Type == "chat.message" {
				continue // people's messages go to them alone, never to the administrators' screens
			}
			if sub.Dropped() {
				fmt.Fprint(w, "event: resync\ndata: {}\n\n")
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
		}
		fl.Flush()
	}
}

// Check is one line of the health page.
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok, warn, fail
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // a route in the interface
}

func (s *Server) health(w http.ResponseWriter, r *http.Request, u store.User) {
	cfg, _ := s.Store.Config()
	var out []Check
	add := func(id, status, title, detail, fix string) {
		out = append(out, Check{id, status, title, detail, fix})
	}
	if s.Engine.SIPPort() > 0 {
		add("sip", "ok", "Phone service is running", fmt.Sprintf("Listening for phones on port %d (UDP and TCP).", s.Engine.SIPPort()), "")
	} else {
		add("sip", "fail", "Phone service is not running", "The SIP port could not be opened. Another phone system may be using it.", "/settings")
	}
	regs := s.Engine.Registrations()
	online := map[string]bool{}
	for _, b := range regs {
		online[b.Extension] = true
	}
	var offline []string
	for _, x := range cfg.Extensions {
		if x.Enabled && !online[x.Number] {
			offline = append(offline, x.Number)
		}
	}
	switch {
	case len(cfg.Extensions) == 0:
		add("phones", "warn", "No extensions yet", "Add the people who will use phones.", "/extensions")
	case len(offline) == 0:
		add("phones", "ok", "Every phone is connected", fmt.Sprintf("%d extensions registered.", len(cfg.Extensions)), "")
	default:
		add("phones", "warn", fmt.Sprintf("%d of %d extensions have no phone connected", len(offline), len(cfg.Extensions)),
			"Not connected: "+strings.Join(offline, ", ")+". Open an extension to see how to set up its phone.", "/extensions")
	}
	if len(cfg.Trunks) == 0 {
		add("trunks", "warn", "No phone line to the outside world", "Internal calls work. Add a SIP trunk to call out and receive calls.", "/trunks")
	}
	up := map[string]bool{} // trunks that can carry a call right now
	for _, t := range s.Engine.TrunkStatus() {
		up[t.ID] = t.State == "registered" || t.State == "reachable"
		switch t.State {
		case "registered", "reachable":
			add("trunk-"+t.ID, "ok", t.Name+" is connected", fmt.Sprintf("Answering in %d ms.", t.LatencyMS), "")
		case "disabled":
			add("trunk-"+t.ID, "warn", t.Name+" is switched off", "", "/trunks")
		default:
			add("trunk-"+t.ID, "fail", t.Name+" is not connected", t.Error, "/trunks")
		}
	}
	// Each emergency number on its own: a route pattern for it, or at least a trunk the engine falls back to.
	var unrouted []string
	for _, n := range cfg.System.EmergencyNumbers {
		routed := false
		for _, rt := range cfg.OutboundRoutes {
			for _, p := range rt.Patterns {
				routed = routed || config.MatchPattern(p, n)
			}
		}
		if !routed {
			unrouted = append(unrouted, n)
		}
	}
	anyTrunk, anyUp := false, false
	for _, tr := range cfg.Trunks {
		anyTrunk = anyTrunk || tr.Enabled
		anyUp = anyUp || up[tr.ID]
	}
	nums := strings.Join(cfg.System.EmergencyNumbers, ", ")
	law := ""
	if cfg.System.Country == "" || cfg.System.Country == "US" {
		law = " This is a legal requirement for business phone systems in the US (Kari's Law)."
	}
	switch {
	case !anyTrunk:
		add("911", "fail", "Emergency calls cannot leave this system", "Add a trunk so calls to "+nums+" can reach the emergency services."+law, "/trunks")
	case !anyUp:
		add("911", "fail", "Emergency calls cannot get through now", "No trunk is connected, so calls to "+nums+" would fail. Fix the trunk, or keep a mobile phone at hand until it is back.", "/trunks")
	case len(unrouted) > 0:
		add("911", "warn", "No route for "+strings.Join(unrouted, ", "), "These emergency calls will still be sent, as dialled, on every trunk in turn. Add them to the emergency route so they use the right trunk and number format.", "/routes")
	default:
		add("911", "ok", "Emergency calls have a route", "Calls to "+nums+" go out on a trunk.", "")
	}
	locLaw := "."
	if law != "" {
		locLaw = " (RAY BAUM'S Act)."
	}
	noLoc := 0
	for _, x := range cfg.Extensions {
		if x.Site == "" && x.Location == "" {
			noLoc++
		}
	}
	if noLoc > 0 {
		add("location", "warn", fmt.Sprintf("%d extensions have no emergency location", noLoc), "Choose each extension's site, floor and room so responders know where to go"+locLaw, "/extensions")
	}
	if m := s.crmProblem(); m != "" && cfg.System.CRM.LogURL != "" {
		add("crm", "warn", "Calls are not reaching the CRM", m+". BitVoice tried four times; check the call log address in Settings, CRM.", "/settings")
	}
	if rec := cfg.System.Recording; rec.Mode != "off" && rec.Mode != "" {
		announce := rec.Notice == "announce"
		for _, x := range rec.Rules {
			announce = announce || x.Action == "announce"
		}
		if _, err := s.Store.Greeting("recording-notice"); announce && err != nil {
			add("notice", "info", "No recording notice recorded", "Recorded calls play three beeps as their notice until you record the words, such as \"This call is recorded for training and quality\", in Settings, Call recording.", "/settings")
		}
	}
	s.teamsProblems(add)
	s.serverProblems(add)
	s.srvProblems(r.Context(), add)
	for _, r := range s.reputationProblems() {
		add("reputation-"+r.Number, "warn", "Calls from "+r.Number+" may be labelled spam", strings.Join(r.Why, " "), "/reputation")
	}
	for ext, m := range s.calendarProblems() {
		add("calendar-"+ext, "warn", "Extension "+ext+"'s calendar cannot be read", m+". Until it can, its meetings do not send calls elsewhere.", "/extensions")
	}
	if m := s.costProblem(); m != "" {
		add("costs", "warn", "A cost alert was raised", m, "/costs")
	}
	for i, m := range s.vaultProblems() {
		add(fmt.Sprintf("vault-%d", i), "fail", "A secret could not be read from its vault", m, "/settings")
	}
	if len(cfg.Sites) == 0 && len(cfg.Extensions) > 0 {
		add("sites", "info", "No sites with a street address", "Add your building's address on Sites so emergency calls can carry it and remote workers can be told apart.", "/sites")
	} else if !cfg.System.SendLocation && len(cfg.Sites) > 0 {
		add("sendloc", "info", "Emergency calls do not carry the address", "Switch on Send the caller's address in Settings if your provider takes PIDF-LO location.", "/settings")
	}
	if len(cfg.System.EmergencyNotify) == 0 {
		add("notify", "warn", "Nobody is told when someone dials "+firstOr(cfg.System.EmergencyNumbers, "911"), "Choose the front desk or security phones in Settings.", "/settings")
	}
	greetings := s.Store.Greetings()
	for _, m := range cfg.Menus {
		if !slices.Contains(greetings, "menu-"+m.ID) {
			add("menu-"+m.ID, "warn", "Menu "+m.Name+" has no recorded prompt", "Callers hear only a beep, so they will not know which key to press.", "/menus")
		}
	}
	if len(cfg.Schedules) > 0 && cfg.System.Timezone == "" {
		add("timezone", "warn", "Schedules run on UTC time", "Set the time zone in Settings so opening hours open and close at the right local time.", "/settings")
	}
	if cfg.System.Recording.Mode != "off" && cfg.System.Recording.KeepDays == 0 {
		add("recording", "warn", "Call recordings are kept for ever", "They will slowly fill the disk. Choose how many days to keep them in Settings.", "/settings")
	}
	secure := slices.ContainsFunc(cfg.Extensions, func(x config.Extension) bool { return x.SecureMedia })
	if secure && s.Engine.TLSPort() == 0 {
		add("tls", "warn", "Encrypted calls without encrypted signalling", "Some extensions use SRTP, but SIP over TLS is not running, so the media keys travel in the clear.", "/settings")
	}
	if pv := cfg.System.Provisioning; pv.Enabled && len(cfg.Devices) == 0 {
		add("provision", "warn", "Automatic phone set-up is on but no phones are listed", "Add each phone's MAC address on the Phones page.", "/phones")
	}
	for _, w := range emergencyRouteWarnings(cfg) {
		add("emergency-route", "warn", w[0], w[1], "/routes")
	}
	if a := cfg.System.AI; a.Enabled && a.LocalSpeech {
		switch st := s.Engine.Speech.Status(); st.State {
		case "running":
			add("speech", "ok", "Speech to text on this server is running", "Model "+st.Model+"; callers' audio stays on this computer.", "")
		case "downloading":
			add("speech", "info", "Speech to text on this server is fetching its model", fmt.Sprintf("%d of %d MB. Transcripts start when it is done.", st.Downloaded>>20, st.Total>>20), "/settings")
		case "starting":
			add("speech", "info", "Speech to text on this server is starting", "", "/settings")
		default:
			add("speech", "fail", "Speech to text on this server is not working", firstNonEmpty(st.Error, "It is not running.")+" Transcripts (live 911 calls included) are not being written.", "/settings")
		}
	}
	if bans := s.Engine.Banned(); len(bans) > 0 {
		add("bans", "warn", fmt.Sprintf("%d addresses blocked for failed sign-ins", len(bans)), "This is usually a scanner on the internet. If a phone of yours is blocked, check its password.", "/security")
	}
	if !slices.ContainsFunc(out, func(c Check) bool { return c.Status != "ok" }) {
		add("all", "ok", "Everything looks good", "", "")
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

// TraceEntry is one SIP message for the trace screen.
type TraceEntry struct {
	At      time.Time `json:"at"`
	Dir     string    `json:"dir"` // in or out
	Peer    string    `json:"peer"`
	CallID  string    `json:"callId"`
	Summary string    `json:"summary"`
	Message string    `json:"message"`
}

// TraceBuffer keeps the most recent SIP messages in memory.
type TraceBuffer struct {
	mu      sync.Mutex
	entries []TraceEntry
	next    int
	full    bool
}

// NewTraceBuffer keeps n messages.
func NewTraceBuffer(n int) *TraceBuffer { return &TraceBuffer{entries: make([]TraceEntry, n)} }

// Add records a message. Authentication headers are masked: the trace is shown to administrators but
// a digest response should never sit on a screen.
func (t *TraceBuffer) Add(dir string, p sip.Peer, m *sip.Message) {
	c := m.Clone()
	for i, h := range c.Headers {
		if h.Name == "Authorization" || h.Name == "Proxy-Authorization" {
			c.Headers[i].Value = "Digest (hidden)"
		}
	}
	summary := ""
	if m.IsRequest {
		summary = m.Method
	} else {
		_, method := m.CSeq()
		summary = strconv.Itoa(m.StatusCode) + " " + m.Reason + " (" + method + ")"
	}
	e := TraceEntry{At: time.Now().UTC(), Dir: dir, Peer: p.String(), CallID: m.CallID(), Summary: summary, Message: c.String()}
	t.mu.Lock()
	t.entries[t.next] = e
	t.next = (t.next + 1) % len(t.entries)
	if t.next == 0 {
		t.full = true
	}
	t.mu.Unlock()
}

// Search returns matching messages, newest first.
func (t *TraceBuffer) Search(q string, limit int) []TraceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.next
	if t.full {
		n = len(t.entries)
	}
	q = strings.ToLower(q)
	out := []TraceEntry{}
	for i := 0; i < n && len(out) < limit; i++ {
		idx := (t.next - 1 - i + len(t.entries)) % len(t.entries)
		e := t.entries[idx]
		if q == "" || strings.Contains(strings.ToLower(e.Message), q) || strings.Contains(e.Peer, q) {
			out = append(out, e)
		}
	}
	return out
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

// emergencyRouteWarnings finds 911 calls whose route would refuse them: an ESInet trunk with no incoming
// route, or a route on one that hangs up. BitVoice rings every call-taker for such a call instead (it
// never refuses one), but the route should say where they go.
func emergencyRouteWarnings(cfg config.Config) (out [][2]string) {
	for _, tr := range cfg.Trunks {
		if !tr.Enabled || !tr.ESInet {
			continue
		}
		any := false
		for _, r := range cfg.InboundRoutes {
			if r.Trunk != "" && r.Trunk != tr.ID {
				continue
			}
			any = true
			if r.Destination.Type == "hangup" {
				out = append(out, [2]string{"The incoming route " + r.Name + " hangs up 911 calls from " + tr.Name,
					"BitVoice will not refuse an emergency call, so these ring every call-taker instead. Send the route to your call-takers' ring group or queue."})
			}
		}
		if !any {
			out = append(out, [2]string{"No incoming route for 911 calls from " + tr.Name,
				"BitVoice will not refuse an emergency call, so these ring every call-taker. Add an incoming route that sends them to your call-takers' ring group or queue."})
		}
	}
	return out
}
