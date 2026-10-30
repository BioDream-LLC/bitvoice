package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/webphone"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
)

// browserHost is the address the browser used to reach BitVoice, which is where it can send media.
func browserHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		h = r.Host
	}
	if h == "localhost" {
		h = "127.0.0.1"
	}
	return h
}

var echoes sync.Map // id -> func() (closes the echo session)

// iceOptions are BitVoice's part in ICE for a browser session (Settings, Behind NAT).
func (s *Server) iceOptions(r *http.Request) webrtc.ICEOptions {
	cfg := s.config()
	return webrtc.OptionsFrom(cfg.System.ICE, browserHost(r), clientIP(r))
}

func (s *Server) phoneRoutes() {
	// The browser echo test: what the microphone sends comes back, encrypted, through BitVoice. It proves
	// the browser, the network path and BitVoice's WebRTC stack all work before a real call.
	s.handle("POST /api/phone/echo", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			SDP string `json:"sdp"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		offer, err := sdp.Parse([]byte(in.SDP))
		if err != nil {
			writeError(w, http.StatusBadRequest, "that is not an SDP offer")
			return
		}
		ep, err := s.Engine.NewMediaEndpoint()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no media ports free")
			return
		}
		sess, ans, err := webrtc.AnswerWith(ep, offer, browserHost(r), s.iceOptions(r))
		if err != nil {
			ep.Close()
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		ep.Echo()
		ep.Start()
		id := randomID()
		stop := sync.OnceFunc(func() { sess.Close(); ep.Close(); echoes.Delete(id) })
		echoes.Store(id, stop)
		time.AfterFunc(2*time.Minute, stop)
		writeJSON(w, 200, map[string]string{"id": id, "sdp": string(ans.Bytes())})
	})
	s.handle("DELETE /api/phone/echo/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if f, ok := echoes.Load(r.PathValue("id")); ok {
			f.(func())()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Browser phones: a browser signs in as an extension and becomes one of its phones.

type browserPhone struct {
	phone    *webphone.Phone
	ext      string
	device   string // the paired app that opened it, if one did
	lastSeen time.Time
	mu       sync.Mutex
	// sessions are the calls' WebRTC sessions, for an ICE restart when the browser's network changes.
	sessions map[string]*webrtc.Session
}

// keep remembers a call's session until the call ends.
func (bp *browserPhone) keep(call string, sess *webrtc.Session) func() {
	bp.mu.Lock()
	if bp.sessions == nil {
		bp.sessions = map[string]*webrtc.Session{}
	}
	bp.sessions[call] = sess
	bp.mu.Unlock()
	return func() {
		bp.mu.Lock()
		delete(bp.sessions, call)
		bp.mu.Unlock()
	}
}

var phones sync.Map // phone id -> *browserPhone

func (s *Server) gateway() (*webphone.Gateway, error) {
	s.gwOnce.Do(func() {
		s.gw, s.gwErr = webphone.New(context.Background(), s.Engine.SIPPort(), s.Engine)
	})
	return s.gw, s.gwErr
}

func (s *Server) openPhone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Extension string `json:"extension"`
		Password  string `json:"password"`
		// Device and Secret: a paired BitVoice app signing in with its own credentials.
		Device string `json:"device"`
		Secret string `json:"secret"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	cfg, _ := s.Store.Config()
	byDevice, device := false, ""
	if in.Device != "" {
		d, ok := s.Store.CheckDevice(in.Device, in.Secret)
		if !ok {
			s.loginFailed(clientIP(r).String())
			writeError(w, http.StatusUnauthorized, "this phone is no longer paired")
			return
		}
		in.Extension, byDevice, device = d.Extension, true, d.ID
		_, _ = s.Store.UpdateDevice(d.ID, func(x *store.Device) { x.LastSeen = time.Now().UTC() })
	}
	x := cfg.FindExtension(in.Extension)
	ip := clientIP(r).String()
	u, signedIn := s.user(r)
	allowed := (signedIn && u.Role == "admin" || byDevice) && x != nil && x.Enabled
	if !allowed {
		if s.throttled(ip) {
			writeError(w, http.StatusTooManyRequests, "too many attempts; wait a few minutes")
			return
		}
		if x == nil || !x.Enabled || subtle.ConstantTimeCompare([]byte(in.Password), []byte(x.Secret)) != 1 {
			s.loginFailed(ip)
			writeError(w, http.StatusUnauthorized, "that extension and password do not match")
			return
		}
	}
	gw, err := s.gateway()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id := randomID()
	p, err := gw.Open(id, x.Number, x.Secret, clientIP(r).String())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	bp := &browserPhone{phone: p, ext: x.Number, device: device, lastSeen: time.Now()}
	if device != "" {
		// An app opens one phone at a time: one opened before (by the app before Android stopped it)
		// goes now, or calls would ring there too for the minute until it is reaped.
		phones.Range(func(k, v any) bool {
			if old := v.(*browserPhone); old.device == device {
				phones.Delete(k)
				old.phone.Close()
			}
			return true
		})
	}
	phones.Store(id, bp)
	go s.reapPhone(id, bp)
	s.audit(r, "extension "+x.Number, "phone.open", "browser phone")
	writeJSON(w, 200, map[string]string{"id": id, "extension": x.Number, "name": x.Name})
}

// reapPhone closes a browser phone whose page has gone (no event stream for a minute).
func (s *Server) reapPhone(id string, bp *browserPhone) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		bp.mu.Lock()
		idle := time.Since(bp.lastSeen)
		bp.mu.Unlock()
		if idle > time.Minute {
			phones.Delete(id)
			bp.phone.Close()
			return
		}
	}
}

func phoneOf(w http.ResponseWriter, r *http.Request) *browserPhone {
	v, ok := phones.Load(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "this browser phone is no longer signed in")
		return nil
	}
	bp := v.(*browserPhone)
	bp.mu.Lock()
	bp.lastSeen = time.Now()
	bp.mu.Unlock()
	return bp
}

func (s *Server) phoneEvents(w http.ResponseWriter, r *http.Request) {
	bp := phoneOf(w, r)
	if bp == nil {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Subscribe before saying hello: a page that has heard hello misses nothing after it.
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-sub.C:
			// A briefing for this phone's extension: a call just passed to it.
			if b, ok := ev.Data.(Briefing); ok && ev.Type == "call.briefing" && b.Target == bp.ext {
				j, _ := json.Marshal(b)
				fmt.Fprintf(w, "event: briefing\ndata: %s\n\n", j)
				fl.Flush()
			}
			// A live transcript line for a call this extension is on (AI, live call transcripts).
			if ev.Type == "call.transcript" {
				if m, ok := ev.Data.(map[string]any); ok && partyIs(m["parties"], bp.ext) {
					j, _ := json.Marshal(m)
					fmt.Fprintf(w, "event: transcript\ndata: %s\n\n", j)
					fl.Flush()
				}
			}
			// A chat message for this extension (the staff chat), so the app's Chat tab lights up.
			if ev.Type == "chat.message" && s.forPortal(ev, bp.ext) {
				if ce, ok := ev.Data.(ChatEvent); ok {
					j, _ := json.Marshal(ce)
					fmt.Fprintf(w, "event: chat\ndata: %s\n\n", j)
					fl.Flush()
				}
			}
		case ev := <-bp.phone.Events:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: call\ndata: %s\n\n", b)
			fl.Flush()
		case <-tick.C:
			bp.mu.Lock()
			bp.lastSeen = time.Now()
			bp.mu.Unlock()
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

// browserMedia answers a browser's WebRTC offer on a new endpoint.
func (s *Server) browserMedia(r *http.Request, offerSDP string) (*media.Endpoint, *webrtc.Session, *sdp.Session, error) {
	offer, err := sdp.Parse([]byte(offerSDP))
	if err != nil {
		return nil, nil, nil, err
	}
	ep, err := s.Engine.NewMediaEndpoint()
	if err != nil {
		return nil, nil, nil, err
	}
	sess, ans, err := webrtc.AnswerWith(ep, offer, browserHost(r), s.iceOptions(r))
	if err != nil {
		ep.Close()
		return nil, nil, nil, err
	}
	ep.Start()
	return ep, sess, ans, nil
}

func (s *Server) phoneCall(w http.ResponseWriter, r *http.Request) {
	bp := phoneOf(w, r)
	if bp == nil {
		return
	}
	var in struct {
		To  string `json:"to"`
		SDP string `json:"sdp"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	ep, sess, ans, err := s.browserMedia(r, in.SDP)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	c, err := bp.phone.Dial(in.To, ep, sess.PT, sess.EventPT)
	if err != nil {
		sess.Close()
		ep.Close()
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	forget := bp.keep(c.ID, sess)
	c.SetCleanup(func() { forget(); sess.Close(); ep.Close() })
	writeJSON(w, 200, map[string]string{"callId": c.ID, "sdp": string(ans.Bytes())})
}

func (s *Server) phoneAnswer(w http.ResponseWriter, r *http.Request) {
	bp := phoneOf(w, r)
	if bp == nil {
		return
	}
	var in struct {
		CallID string `json:"callId"`
		SDP    string `json:"sdp"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	c := bp.phone.Find(in.CallID)
	if c == nil {
		writeError(w, http.StatusNotFound, "that call has ended")
		return
	}
	ep, sess, ans, err := s.browserMedia(r, in.SDP)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	forget := bp.keep(in.CallID, sess)
	c.SetCleanup(func() { forget(); sess.Close(); ep.Close() })
	if err := c.Answer(ep, sess.PT, sess.EventPT); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"sdp": string(ans.Bytes())})
}

// partyIs reports whether ext is in a call.transcript event's "parties" list (the extensions on the call).
func partyIs(v any, ext string) bool {
	switch ps := v.(type) {
	case []string:
		for _, p := range ps {
			if p == ext {
				return true
			}
		}
	case []any:
		for _, p := range ps {
			if s, ok := p.(string); ok && s == ext {
				return true
			}
		}
	}
	return false
}

func (s *Server) browserPhoneRoutes() {
	// Signing in uses the extension's SIP password, or an administrator's session.
	s.mux.HandleFunc("POST /api/phone", s.openPhone)
	s.mux.HandleFunc("GET /api/phone/{id}/events", s.phoneEvents)
	s.mux.HandleFunc("POST /api/phone/{id}/calls", s.phoneCall)
	s.mux.HandleFunc("POST /api/phone/{id}/answer", s.phoneAnswer)
	// The caller pop-up on a browser phone: who is calling, a link to them in the CRM, how often they
	// have called and any callback they are waiting for (not other people's call summaries).
	s.mux.HandleFunc("GET /api/phone/{id}/caller", func(w http.ResponseWriter, r *http.Request) {
		if phoneOf(w, r) == nil {
			return
		}
		number := strings.TrimSpace(r.URL.Query().Get("number"))
		if number == "" || len(number) > 40 {
			writeError(w, http.StatusBadRequest, "no number")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		writeJSON(w, 200, s.callerCard(ctx, number, false))
	})
	// The directory the phone lists: extensions, then shared contacts.
	s.mux.HandleFunc("GET /api/phone/{id}/directory", func(w http.ResponseWriter, r *http.Request) {
		if phoneOf(w, r) == nil {
			return
		}
		type entry struct {
			Name    string `json:"name"`
			Number  string `json:"number"`
			Company string `json:"company,omitempty"`
			Group   string `json:"group"`
		}
		out := []entry{}
		for _, e := range s.phonebook(s.config()) {
			out = append(out, entry{e.Name, e.Number, e.Company, e.Group})
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	// An ICE restart: the browser's network changed (Wi-Fi to mobile, a new address) and it offers new
	// ICE credentials for the same call; the answer carries BitVoice's, and the call moves to the new
	// path without being dialled again.
	s.mux.HandleFunc("POST /api/phone/{id}/calls/{call}/ice", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		var in struct {
			SDP string `json:"sdp"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		offer, err := sdp.Parse([]byte(in.SDP))
		if err != nil {
			writeError(w, http.StatusBadRequest, "that is not an SDP offer")
			return
		}
		bp.mu.Lock()
		sess := bp.sessions[r.PathValue("call")]
		bp.mu.Unlock()
		if sess == nil {
			writeError(w, http.StatusNotFound, "that call has ended")
			return
		}
		ans, err := sess.Restart(offer, browserHost(r))
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"sdp": string(ans.Bytes())})
	})
	s.mux.HandleFunc("DELETE /api/phone/{id}/calls/{call}", func(w http.ResponseWriter, r *http.Request) {
		if bp := phoneOf(w, r); bp != nil {
			if c := bp.phone.Find(r.PathValue("call")); c != nil {
				c.Hangup()
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	// Transfer the other party to a number (blind), as a desk phone does with REFER. To the park
	// number, it parks the call.
	s.mux.HandleFunc("POST /api/phone/{id}/calls/{call}/transfer", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		var in struct {
			To string `json:"to"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		c := bp.phone.Find(r.PathValue("call"))
		if c == nil {
			writeError(w, http.StatusNotFound, "that call has ended")
			return
		}
		if err := c.Transfer(in.To); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Hold and take the other party back, by re-INVITEing (they hear hold music where it is set).
	hold := func(on bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			bp := phoneOf(w, r)
			if bp == nil {
				return
			}
			c := bp.phone.Find(r.PathValue("call"))
			if c == nil {
				writeError(w, http.StatusNotFound, "that call has ended")
				return
			}
			var err error
			if on {
				err = c.Hold()
			} else {
				err = c.Unhold()
			}
			if err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, 200, map[string]bool{"held": c.Held()})
		}
	}
	s.mux.HandleFunc("POST /api/phone/{id}/calls/{call}/hold", hold(true))
	s.mux.HandleFunc("POST /api/phone/{id}/calls/{call}/unhold", hold(false))
	// This extension's voicemail, its recent calls, and whom to pick up for (presence).
	s.mux.HandleFunc("GET /api/phone/{id}/voicemail", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		list, err := s.Store.Voicemails(bp.ext)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": nonNil(list)})
	})
	s.mux.HandleFunc("GET /api/phone/{id}/voicemail/{msg}/audio", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		b, err := s.Store.VoicemailAudio(bp.ext, r.PathValue("msg"))
		if err != nil {
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		serveWAV(w, r, "voicemail-"+r.PathValue("msg")+".wav", b)
	})
	s.mux.HandleFunc("POST /api/phone/{id}/voicemail/{msg}/heard", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		if _, err := s.Store.SetHeard(bp.ext, r.PathValue("msg"), true); err != nil {
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		s.Engine.MailboxChanged(bp.ext)
		w.WriteHeader(http.StatusNoContent)
	})
	s.mux.HandleFunc("DELETE /api/phone/{id}/voicemail/{msg}", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		if err := s.Store.DeleteVoicemail(bp.ext, r.PathValue("msg")); err != nil {
			if errors.Is(err, store.ErrHeld) {
				writeErr(w, err)
				return
			}
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		s.Engine.MailboxChanged(bp.ext)
		w.WriteHeader(http.StatusNoContent)
	})
	// Recent calls to and from this extension, newest first, for tap-to-call.
	s.mux.HandleFunc("GET /api/phone/{id}/recents", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		rows, err := s.Store.CDRsFor(bp.ext, 60)
		if err != nil {
			writeErr(w, err)
			return
		}
		type recent struct {
			Number    string    `json:"number"`
			Name      string    `json:"name,omitempty"`
			Direction string    `json:"direction"` // in or out, from this extension's view
			When      time.Time `json:"when"`
			Missed    bool      `json:"missed"`
			Seconds   int       `json:"seconds"`
		}
		out := []recent{}
		for _, c := range rows {
			rec := recent{When: c.Start, Seconds: c.Billsec}
			if c.From == bp.ext { // this extension placed the call
				rec.Direction, rec.Number, rec.Name = "out", c.To, c.ToName
			} else {
				rec.Direction, rec.Number, rec.Name = "in", c.From, c.FromName
				rec.Missed = c.Answer.IsZero()
			}
			out = append(out, rec)
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	// Who is free, busy or ringing, so the phone can show it and pick up for a colleague (BLF).
	s.mux.HandleFunc("GET /api/phone/{id}/presence", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		writeJSON(w, 200, map[string]any{"items": s.phonePresence(bp.ext)})
	})
	s.phoneChatRoutes()
	// The video rooms this phone can join: the SFU conference rooms, with how many are in each now.
	s.mux.HandleFunc("GET /api/phone/{id}/video-rooms", func(w http.ResponseWriter, r *http.Request) {
		bp := phoneOf(w, r)
		if bp == nil {
			return
		}
		type room struct {
			Number string `json:"number"`
			Name   string `json:"name"`
			HasPIN bool   `json:"hasPin"`
			InRoom int    `json:"inRoom"`
		}
		out := []room{}
		for _, c := range s.config().Conferences {
			if c.Video != "sfu" {
				continue
			}
			n := 0
			if live, err := s.Engine.SFURoomOf(c.Number); err == nil {
				n = len(live.Publishers)
			}
			out = append(out, room{Number: c.Number, Name: c.Name, HasPIN: c.PIN != "", InRoom: n})
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.mux.HandleFunc("DELETE /api/phone/{id}", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := phones.LoadAndDelete(r.PathValue("id")); ok {
			v.(*browserPhone).phone.Close()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
