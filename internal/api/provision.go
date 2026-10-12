package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/provision"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// provisionFile serves a desk phone its configuration. Phones authenticate with the provisioning user
// name and password (HTTP Basic, which every phone supports); failures count towards the same throttle as
// sign-in, so the password cannot be guessed.
func (s *Server) provisionFile(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.Store.Config()
	pv := cfg.System.Provisioning
	ip := clientIP(r).String()
	if !pv.Enabled {
		http.NotFound(w, r)
		return
	}
	if s.throttled(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(pv.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(pass), []byte(pv.Password)) != 1 {
		if ok {
			s.loginFailed(ip)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="BitVoice phones"`)
		http.Error(w, "sign in with the provisioning user name and password", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("file")
	// The address the phone used to reach BitVoice is the one it can register to.
	server := cfg.System.PublicAddress
	srvName := cfg.System.Cluster.Name
	if pr, err := netip.ParsePrefix(cfg.System.Cluster.SharedAddress); err == nil && s.Cluster != nil {
		server = pr.Addr().String() // the address that moves to a running server
	}
	if server == "" {
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			server = h
		} else {
			server = r.Host
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	bookURL := (&url.URL{Scheme: scheme, User: url.UserPassword(pv.Username, pv.Password), Host: r.Host, Path: "/provision"}).String()
	f, found := provision.LookupWith(&cfg, name, server, s.Engine.SIPPort(), s.Engine.TLSPort(), provision.Book{Entries: s.phonebook(cfg), URL: bookURL, Backup: s.backupFor(cfg), SRVName: srvName})
	if !found {
		http.NotFound(w, r)
		return
	}
	s.Store.Audit(store.AuditEntry{Actor: "phone", Action: "provision.fetch", Detail: name + " " + strings.SplitN(r.UserAgent(), " ", 2)[0], From: ip})
	s.Bus.Publish("provision.fetched", map[string]any{"file": name, "from": ip, "agent": r.UserAgent(), "at": time.Now().UTC()})
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f.Body)
}
