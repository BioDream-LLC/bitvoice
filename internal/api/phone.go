package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
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
		sess, ans, err := webrtc.Answer(ep, offer, browserHost(r))
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
	lastSeen time.Time
	mu       sync.Mutex
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
	byDevice := false
	if in.Device != "" {
		d, ok := s.Store.CheckDevice(in.Device, in.Secret)
		if !ok {
			s.loginFailed(clientIP(r).String())
			writeError(w, http.StatusUnauthorized, "this phone is no longer paired")
			return
		}
		in.Extension, byDevice = d.Extension, true
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
	bp := &browserPhone{phone: p, ext: x.Number, lastSeen: time.Now()}
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
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
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
	sess, ans, err := webrtc.Answer(ep, offer, browserHost(r))
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
	c.SetCleanup(func() { sess.Close(); ep.Close() })
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
	c.SetCleanup(func() { sess.Close(); ep.Close() })
	if err := c.Answer(ep, sess.PT, sess.EventPT); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"sdp": string(ans.Bytes())})
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
	s.mux.HandleFunc("DELETE /api/phone/{id}/calls/{call}", func(w http.ResponseWriter, r *http.Request) {
		if bp := phoneOf(w, r); bp != nil {
			if c := bp.phone.Find(r.PathValue("call")); c != nil {
				c.Hangup()
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s.mux.HandleFunc("DELETE /api/phone/{id}", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := phones.LoadAndDelete(r.PathValue("id")); ok {
			v.(*browserPhone).phone.Close()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
