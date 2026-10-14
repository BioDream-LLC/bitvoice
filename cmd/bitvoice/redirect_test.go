package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/certs"
)

// The web port serves HTTPS, and answers plain HTTP with a redirect to HTTPS, except phones' settings
// files while phones may use plain HTTP.
func TestHTTPSOnly(t *testing.T) {
	cert, err := certs.Load(filepath.Join(t.TempDir(), "tls"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/provision/") {
			_, _ = io.WriteString(w, "phone settings")
			return
		}
		_, _ = io.WriteString(w, "secret page")
	})
	srv := &http.Server{Handler: page, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	var mu sync.Mutex
	phonesPlain := true
	plainSrv := &http.Server{Handler: plainHandler(page, func() bool { mu.Lock(); defer mu.Unlock(); return phonesPlain })}
	port := newSplitPort(ln)
	go func() { _ = srv.ServeTLS(port.tls, "", "") }()
	go func() { _ = plainSrv.Serve(port.plain) }()
	defer port.Close()
	defer srv.Close()
	defer plainSrv.Close()
	addr := ln.Addr().String()

	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := plain.Get("http://" + addr + "/setup?step=2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusPermanentRedirect || r.Header.Get("Location") != "https://"+addr+"/setup?step=2" {
		t.Fatalf("plain HTTP: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if strings.Contains(string(body), "secret page") {
		t.Fatal("served the page over plain HTTP")
	}

	// Phones' settings files over plain HTTP while allowed; refused (sent to HTTPS) once switched off.
	get := func() (int, string) {
		r, err := plain.Get("http://" + addr + "/provision/y000000000000.cfg")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return r.StatusCode, string(b)
	}
	if code, b := get(); code != 200 || b != "phone settings" {
		t.Fatalf("phone file over HTTP: %d %q", code, b)
	}
	mu.Lock()
	phonesPlain = false
	mu.Unlock()
	if code, b := get(); code != http.StatusPermanentRedirect || strings.Contains(b, "phone settings") {
		t.Fatalf("phone file over HTTP when HTTPS only: %d %q", code, b)
	}

	secure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	for i := 0; i < 2; i++ { // and again on a new connection, after the redirect
		r, err = secure.Get("https://" + addr + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || string(body) != "secret page" {
			t.Fatalf("HTTPS: %d %q", r.StatusCode, body)
		}
		secure.CloseIdleConnections()
	}

	// A request with a malformed Host is refused, or sent to this server's own address: never to its Host.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: a/b\r\n\r\n")
	out, _ := io.ReadAll(c)
	c.Close()
	if strings.Contains(string(out), "a/b") && strings.Contains(string(out), "Location:") {
		t.Fatalf("redirected to a host the request chose: %q", out)
	}
	if !strings.Contains(string(out), " 400 ") && !strings.Contains(string(out), "Location: https://"+addr+"/") {
		t.Fatalf("bad Host: %q", out)
	}
}
