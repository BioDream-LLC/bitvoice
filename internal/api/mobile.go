package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/push"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Pairing a phone with the BitVoice app: an administrator opens a one-time code for an extension (shown
// as a QR code), the app trades it for its own device secret, then sends its push token. Incoming calls
// wake it through Apple or Google; it then connects like the browser phone and is rung.

type pairing struct {
	ext     string
	server  string
	expires time.Time
}

type mobileState struct {
	mu    sync.Mutex
	codes map[string]pairing
	// sender is built from the settings it was made for, so a change of settings makes a new one.
	sender push.Sender
	made   config.Mobile
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HasDevices is part of pbx.Mobile: the extension has an app that can be woken.
func (s *Server) HasDevices(ext string) bool {
	cfg, _ := s.Store.Config()
	if cfg.System.Mobile.Push == "" {
		return false
	}
	for _, d := range s.Store.Devices(ext) {
		if d.PushToken != "" {
			return true
		}
	}
	return false
}

func (s *Server) pushSender(m config.Mobile) push.Sender {
	st := &s.mobile
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sender != nil && st.made == m {
		return st.sender
	}
	switch m.Push {
	case "relay":
		st.sender = &push.Relay{URL: m.RelayURL, KeyID: m.RelayKey, Secret: m.RelaySecret}
	case "direct":
		var p push.Platforms
		if m.APNs.Key != "" {
			p.IOS = &push.APNs{KeyID: m.APNs.KeyID, TeamID: m.APNs.TeamID, Topic: m.APNs.Topic, Key: m.APNs.Key, Sandbox: m.APNs.Sandbox}
		}
		if m.FCM != "" {
			p.Android = &push.FCM{ServiceAccount: m.FCM}
		}
		st.sender = p
	default:
		st.sender = nil
	}
	st.made = m
	return st.sender
}

// Wake is part of pbx.Mobile: push every app of the extension.
func (s *Server) Wake(ext string, c pbx.CallInfo) {
	cfg := s.config() // vault references resolved
	sender := s.pushSender(cfg.System.Mobile)
	if sender == nil {
		return
	}
	for _, d := range s.Store.Devices(ext) {
		if d.PushToken == "" {
			continue
		}
		server := cfg.System.Mobile.ServerURL
		m := push.Message{Platform: d.Platform, Token: d.PushToken, CallID: c.ID, From: c.From, FromName: c.FromName,
			Server: server, TTL: 30 * time.Second}
		go func(d store.Device) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := sender.Send(ctx, m)
			_, _ = s.Store.UpdateDevice(d.ID, func(x *store.Device) {
				x.LastPush = time.Now().UTC()
				x.PushError = ""
				switch {
				case errors.Is(err, push.ErrGone):
					x.PushToken, x.PushError = "", "the app was removed from the phone"
				case err != nil:
					x.PushError = err.Error()
				}
			})
			if err != nil {
				s.Log.Warn("could not wake the app", "extension", ext, "device", d.Name, "err", err)
			}
		}(d)
	}
}

type deviceView struct {
	ID        string    `json:"id"`
	Extension string    `json:"extension"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	CanWake   bool      `json:"canWake"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
	LastPush  time.Time `json:"lastPush,omitzero"`
	PushError string    `json:"pushError,omitempty"`
}

func view(d store.Device) deviceView {
	return deviceView{d.ID, d.Extension, d.Name, d.Platform, d.PushToken != "", d.Created, d.LastSeen, d.LastPush, d.PushError}
}

func (s *Server) mobileRoutes() {
	s.handle("GET /api/extensions/{number}/devices", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []deviceView{}
		for _, d := range s.Store.Devices(r.PathValue("number")) {
			out = append(out, view(d))
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("POST /api/extensions/{number}/pair", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		x := cfg.FindExtension(r.PathValue("number"))
		if x == nil {
			writeError(w, http.StatusNotFound, "no such extension")
			return
		}
		server := cfg.System.Mobile.ServerURL
		if server == "" {
			// HTTPS only: BitVoice serves nothing else, and behind a proxy that ends TLS the apps still
			// reach it over https.
			server = "https://" + s.phoneReachable(r.Host)
		}
		code := token(16)
		st := &s.mobile
		st.mu.Lock()
		if st.codes == nil {
			st.codes = map[string]pairing{}
		}
		for k, p := range st.codes {
			if time.Now().After(p.expires) {
				delete(st.codes, k)
			}
		}
		exp := time.Now().Add(10 * time.Minute)
		st.codes[code] = pairing{ext: x.Number, server: server, expires: exp}
		st.mu.Unlock()
		s.audit(r, u.Username, "mobile.pair", "extension "+x.Number)
		link := "bitvoice://pair?" + url.Values{"server": {server}, "code": {code}}.Encode()
		writeJSON(w, 200, map[string]any{"code": code, "server": server, "link": link, "expires": exp,
			"push": cfg.System.Mobile.Push != "", "local": strings.HasPrefix(server, "http://")})
	})
	s.handle("DELETE /api/mobile/devices/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.DeleteDevice(r.PathValue("id")); err != nil {
			writeError(w, http.StatusNotFound, "no such device")
			return
		}
		s.audit(r, u.Username, "mobile.unpair", r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})

	// The app's side: no administrator, just the pairing code and then the device's own secret.
	s.mux.HandleFunc("POST /api/mobile/pair", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r).String()
		if s.throttled(ip) {
			writeError(w, http.StatusTooManyRequests, "too many attempts; wait a few minutes")
			return
		}
		var in struct{ Code, Name, Platform string }
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		st := &s.mobile
		st.mu.Lock()
		p, ok := st.codes[in.Code]
		delete(st.codes, in.Code) // a code works once
		st.mu.Unlock()
		if !ok || time.Now().After(p.expires) {
			s.loginFailed(ip)
			writeError(w, http.StatusUnauthorized, "that pairing code has expired or was already used; show a new one")
			return
		}
		if in.Platform != "ios" && in.Platform != "android" {
			writeError(w, http.StatusBadRequest, "platform must be ios or android")
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" || len(name) > 60 {
			name = "Phone"
		}
		cfg, _ := s.Store.Config()
		x := cfg.FindExtension(p.ext)
		if x == nil {
			writeError(w, http.StatusGone, "the extension no longer exists")
			return
		}
		secret := token(32)
		d, err := s.Store.AddDevice(store.Device{ID: config.NewID(), Extension: x.Number, Name: name, Platform: in.Platform}, secret)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, "extension "+x.Number, "mobile.paired", name+" ("+in.Platform+")")
		writeJSON(w, 200, map[string]string{"device": d.ID, "secret": secret, "extension": x.Number, "name": x.Name, "server": p.server})
	})
	s.mux.HandleFunc("POST /api/mobile/token", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Device, Secret, Token string }
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if _, ok := s.Store.CheckDevice(in.Device, in.Secret); !ok {
			writeError(w, http.StatusUnauthorized, "this phone is no longer paired")
			return
		}
		if len(in.Token) > 4096 {
			writeError(w, http.StatusBadRequest, "token too long")
			return
		}
		_, _ = s.Store.UpdateDevice(in.Device, func(d *store.Device) { d.PushToken, d.LastSeen, d.PushError = in.Token, time.Now().UTC(), "" })
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("POST /api/mobile/unpair", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Device, Secret string }
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if _, ok := s.Store.CheckDevice(in.Device, in.Secret); !ok {
			writeError(w, http.StatusUnauthorized, "this phone is no longer paired")
			return
		}
		_ = s.Store.DeleteDevice(in.Device)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}

// phoneReachable is host (host:port, as the browser reached BitVoice) for a phone to use. On this
// computer, at 127.0.0.1 or localhost, that address means the phone itself: the public address
// (Settings) or this computer's first network address takes its place, on the same port.
func (s *Server) phoneReachable(host string) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h, port = host, ""
	}
	if ip := net.ParseIP(h); h != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return host
	}
	alt := s.publicAddress()
	if alt == "" {
		if a := localAddresses(); len(a) > 0 {
			alt = a[0]
		}
	}
	if alt == "" {
		return host
	}
	if port == "" || port == "443" {
		return alt
	}
	return net.JoinHostPort(alt, port)
}
