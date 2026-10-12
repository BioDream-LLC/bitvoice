package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

var linkPass struct {
	once sync.Once
	tok  string
}

// linkToken is the pass this run's web gateway puts on call-link calls.
func (s *Server) linkToken() string {
	linkPass.once.Do(func() {
		linkPass.tok = randomID() + randomID()
	})
	if s.Engine != nil {
		s.Engine.SetLinkToken(linkPass.tok)
	}
	return linkPass.tok
}

func (s *Server) callLinkRoutes() {
	// The address people are given: it opens the call page.
	s.mux.HandleFunc("GET /call/{slug}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/#/c/"+r.PathValue("slug"), http.StatusFound)
	})
	// What the call page shows, without signing in.
	s.mux.HandleFunc("GET /api/link/{slug}", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		l := cfg.FindCallLink(r.PathValue("slug"))
		if l == nil {
			writeError(w, 404, "this call link does not exist")
			return
		}
		writeJSON(w, 200, map[string]any{"title": l.Title, "intro": l.Intro, "askName": l.AskName, "open": l.Enabled, "organisation": cfg.System.Name})
	})
	// Place the call: the browser's WebRTC offer in, an answer out, and a phone id for its events.
	s.mux.HandleFunc("POST /api/link/{slug}/call", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r).String()
		if s.throttled(ip) {
			writeError(w, http.StatusTooManyRequests, "too many attempts: wait a minute")
			return
		}
		cfg := s.config()
		l := cfg.FindCallLink(r.PathValue("slug"))
		if l == nil || !l.Enabled {
			s.loginFailed(ip) // guessing addresses counts like a failed sign-in
			writeError(w, 404, "this line is closed")
			return
		}
		var in struct {
			Name string `json:"name"`
			SDP  string `json:"sdp"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if l.AskName && in.Name == "" {
			writeError(w, 422, "enter your name")
			return
		}
		gw, err := s.gateway()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		ep, sess, ans, err := s.browserMedia(r, in.SDP)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		id := randomID()
		p := gw.Guest(id, "web", ip, map[string]string{"X-BitVoice-Link": s.linkToken() + ";" + l.ID, "X-BitVoice-Link-Name": in.Name})
		c, err := p.Dial("1", ep, sess.PT, sess.EventPT)
		if err != nil {
			sess.Close()
			ep.Close()
			p.Close()
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		c.SetCleanup(func() { sess.Close(); ep.Close() })
		bp := &browserPhone{phone: p, ext: "", lastSeen: time.Now()}
		phones.Store(id, bp)
		go s.reapPhone(id, bp)
		s.Store.Audit(store.AuditEntry{Actor: "web caller", Action: "calllink.call", Detail: l.Name + ": " + in.Name, From: ip})
		writeJSON(w, 200, map[string]string{"phoneId": id, "callId": c.ID, "sdp": string(ans.Bytes())})
	})
}
