package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/certs"
)

// The web port serves HTTPS, and answers plain HTTP only with a redirect to HTTPS.
func TestHTTPSOnly(t *testing.T) {
	cert, err := certs.Load(filepath.Join(t.TempDir(), "tls"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secret page") }),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	go func() { _ = srv.ServeTLS(redirectPlain{ln}, "", "") }()
	defer srv.Close()
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

	// A broken request with a header-splitting Host gets no redirect it chose.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: a/b\r\n\r\n")
	out, _ := io.ReadAll(c)
	c.Close()
	if !strings.Contains(string(out), "Location: https://"+addr+"/") {
		t.Fatalf("bad Host: %q", out)
	}
}
