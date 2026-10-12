package compat

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// ARI serves the Asterisk REST Interface under /ari: channels, endpoints, bridges (read only), queue-free
// basics and the /ari/events WebSocket. Logins come from the same list as AMI, by HTTP basic
// authentication or ?api_key=user:secret as Asterisk accepts.
type ARI struct {
	PBX     PBX
	Bus     *events.Bus
	Audit   func(user, action, detail string)
	Started time.Time
}

type ariChannel struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	State        string         `json:"state"`
	Caller       ariCallerID    `json:"caller"`
	Connected    ariCallerID    `json:"connected"`
	AccountCode  string         `json:"accountcode"`
	Dialplan     ariDialplan    `json:"dialplan"`
	CreationTime string         `json:"creationtime"`
	Language     string         `json:"language"`
	ChannelVars  map[string]any `json:"channelvars,omitempty"`
}

type ariCallerID struct {
	Name   string `json:"name"`
	Number string `json:"number"`
}

type ariDialplan struct {
	Context  string `json:"context"`
	Exten    string `json:"exten"`
	Priority int    `json:"priority"`
	AppName  string `json:"app_name"`
	AppData  string `json:"app_data"`
}

func toARI(ch Channel) ariChannel {
	return ariChannel{ID: ch.ID, Name: ch.Name, State: StateName(ch.State),
		Caller:       ariCallerID{ch.CallerName, ch.CallerNum},
		Connected:    ariCallerID{ch.ConnectedName, ch.ConnectedNum},
		Dialplan:     ariDialplan{Context: "from-internal", Exten: ch.Exten, Priority: 1, AppName: "Dial"},
		CreationTime: ch.Created.Format("2006-01-02T15:04:05.000-0700"), Language: "en"}
}

func ariError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func ariJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// user checks the request's login against the configured list.
func (a *ARI) user(r *http.Request) *config.CompatUser {
	name, secret, ok := r.BasicAuth()
	if !ok {
		name, secret, ok = strings.Cut(r.URL.Query().Get("api_key"), ":")
	}
	if !ok {
		return nil
	}
	for _, u := range a.PBX.Config().System.Compat.Users {
		if strings.EqualFold(u.Username, name) && subtle.ConstantTimeCompare([]byte(u.Secret), []byte(secret)) == 1 {
			return &u
		}
	}
	return nil
}

func (a *ARI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !a.PBX.Config().System.Compat.ARI {
		ariError(w, http.StatusNotFound, "ARI is switched off; turn it on in Settings, Asterisk compatibility")
		return
	}
	u := a.user(r)
	if u == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="BitVoice ARI"`)
		ariError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/ari"), "/")
	parts := strings.Split(path, "/")
	write := r.Method != http.MethodGet
	if write && !u.Write {
		ariError(w, http.StatusForbidden, "This login is read only")
		return
	}
	q := r.URL.Query()
	p := a.PBX
	switch {
	case path == "asterisk/info":
		ariJSON(w, map[string]any{
			"system": map[string]any{"version": "20.0.0-bitvoice", "entity_id": "bitvoice"},
			"status": map[string]any{"startup_time": a.Started.Format(time.RFC3339), "last_reload_time": a.Started.Format(time.RFC3339)},
			"build":  map[string]any{"os": "BitVoice"},
			"config": map[string]any{"name": p.Config().System.Name, "default_language": "en"},
		})
	case path == "channels" && r.Method == http.MethodGet:
		out := []ariChannel{}
		for _, c := range p.Calls() {
			for _, ch := range Channels(c) {
				out = append(out, toARI(ch))
			}
		}
		ariJSON(w, out)
	case path == "channels" && r.Method == http.MethodPost:
		// Originate: endpoint is the extension to ring first, extension the number it then calls.
		ext, num := Extension(q.Get("endpoint")), q.Get("extension")
		if ext == "" || num == "" {
			ariError(w, http.StatusBadRequest, "endpoint (PJSIP/100) and extension (the number to call) are required")
			return
		}
		ring := 30 * time.Second
		if s, err := strconv.Atoi(q.Get("timeout")); err == nil && s > 0 {
			ring = min(time.Duration(s)*time.Second, 5*time.Minute)
		}
		a.audit(u, "ari.originate", ext+" to "+num)
		id, err := p.Originate(ext, num, ring)
		if err != nil {
			ariError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ch, ok := Find(p, id+"-1"); ok {
			ariJSON(w, toARI(ch))
			return
		}
		ariJSON(w, map[string]string{"id": id + "-1"})
	case len(parts) >= 2 && parts[0] == "channels":
		c, ch, ok := Find(p, parts[1])
		if !ok {
			ariError(w, http.StatusNotFound, "Channel not found")
			return
		}
		switch {
		case len(parts) == 2 && r.Method == http.MethodGet:
			ariJSON(w, toARI(ch))
		case len(parts) == 2 && r.Method == http.MethodDelete:
			a.audit(u, "ari.hangup", ch.Name)
			p.Hangup(c.ID, "ARI "+u.Username)
			w.WriteHeader(http.StatusNoContent)
		case len(parts) == 3 && parts[2] == "redirect" && r.Method == http.MethodPost:
			leaving := "callee"
			if ch.Side == "callee" {
				leaving = "caller"
			}
			to := Extension(q.Get("endpoint"))
			if to == "" {
				ariError(w, http.StatusBadRequest, "endpoint is required")
				return
			}
			if err := p.Redirect(c.ID, leaving, to); err != nil {
				ariError(w, http.StatusConflict, err.Error())
				return
			}
			a.audit(u, "ari.redirect", ch.Name+" to "+to)
			w.WriteHeader(http.StatusNoContent)
		default:
			ariError(w, http.StatusNotImplemented, "BitVoice supports GET and DELETE on a channel, and POST .../redirect")
		}
	case path == "endpoints" || path == "endpoints/PJSIP":
		out := []map[string]any{}
		for _, e := range Endpoints(p) {
			out = append(out, map[string]any{"technology": "PJSIP", "resource": e.Resource, "state": e.State, "channel_ids": nonNil(e.Channels)})
		}
		ariJSON(w, out)
	case len(parts) == 3 && parts[0] == "endpoints":
		for _, e := range Endpoints(p) {
			if e.Resource == parts[2] {
				ariJSON(w, map[string]any{"technology": "PJSIP", "resource": e.Resource, "state": e.State, "channel_ids": nonNil(e.Channels)})
				return
			}
		}
		ariError(w, http.StatusNotFound, "Endpoint not found")
	case path == "bridges":
		out := []map[string]any{}
		for _, c := range p.Calls() {
			if c.State == "answered" || c.State == "held" {
				out = append(out, map[string]any{"id": c.ID, "technology": "simple_bridge", "bridge_type": "mixing",
					"bridge_class": "basic", "channels": []string{c.ID + "-1", c.ID + "-2"}})
			}
		}
		ariJSON(w, out)
	case path == "events":
		a.events(w, r)
	default:
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice: "+r.Method+" /ari/"+path)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *ARI) audit(u *config.CompatUser, action, detail string) {
	if a.Audit != nil {
		a.Audit(u.Username, action, detail)
	}
}

// events streams Asterisk-style channel events as JSON over a WebSocket.
func (a *ARI) events(w http.ResponseWriter, r *http.Request) {
	app := r.URL.Query().Get("app")
	ws := upgrade(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()
	sub := a.Bus.Subscribe()
	defer a.Bus.Unsubscribe(sub)
	tr := NewTracker(a.PBX.Calls())
	gone := make(chan struct{})
	go func() { _ = ws.readLoop(); close(gone) }()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-gone:
			return
		case <-ping.C:
			if ws.writeFrame(9, nil) != nil {
				return
			}
		case ev := <-sub.C:
			c, ok := ev.Data.(pbx.CallInfo)
			if !ok {
				continue
			}
			for _, ch := range tr.Update(c, ev.Type == "call.ended") {
				typ := map[string]string{"new": "ChannelCreated", "state": "ChannelStateChange", "gone": "ChannelDestroyed"}[ch.Kind]
				msg := map[string]any{"type": typ, "timestamp": ev.At.Format("2006-01-02T15:04:05.000-0700"),
					"application": app, "asterisk_id": "bitvoice", "channel": toARI(ch.Ch)}
				if ch.Kind == "gone" {
					msg["cause"], msg["cause_txt"] = 16, "Normal Clearing"
				}
				b, _ := json.Marshal(msg)
				if ws.WriteText(b) != nil {
					return
				}
			}
		}
	}
}
