package compat

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/audiosocket"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
)

type mediaFake struct {
	*fakePBX
	pool     media.Pool
	attached chan string
}

func (m *mediaFake) NewMediaEndpoint() (*media.Endpoint, error) { return m.pool.Allocate() }
func (m *mediaFake) AttachMedia(call string, ep *media.Endpoint, mode string) (func(), error) {
	if call != "abcdef123456" {
		return nil, errors.New("no such call")
	}
	m.attached <- mode
	return func() { m.attached <- "detached" }, nil
}

// externalMedia makes RTP and AudioSocket channels, addChannel joins one to a call (role=listener for
// listen only), DELETE hangs it up; bad requests are refused plainly.
func TestExternalMedia(t *testing.T) {
	f := &mediaFake{fakePBX: newFake(), pool: media.Pool{Host: "127.0.0.1", Min: 34000, Max: 34100}, attached: make(chan string, 4)}
	srv := httptest.NewServer(&ARI{PBX: f, Bus: events.New(), Started: time.Now()})
	defer srv.Close()
	do := func(method, path string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.SetBasicAuth("crm", "a long crm secret")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, _ := do("POST", "/ari/channels/externalMedia?external_host=nonsense"); code != 400 {
		t.Fatalf("bad host: %d", code)
	}
	if code, _ := do("POST", "/ari/channels/externalMedia?external_host=127.0.0.1:9&format=slin16&encapsulation=rtp"); code != 400 {
		t.Fatalf("slin16 over RTP: %d", code)
	}
	peer, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer peer.Close()
	code, body := do("POST", "/ari/channels/externalMedia?app=x&channelId=rtpchan1234&external_host="+peer.LocalAddr().String()+"&format=ulaw")
	if code != 200 || !strings.Contains(body, "UnicastRTP/") || !strings.Contains(body, "UNICASTRTP_LOCAL_PORT") {
		t.Fatalf("rtp: %d %s", code, body)
	}
	if code, body := do("POST", "/ari/bridges/abcdef123456/addChannel?channel=rtpchan1234&role=listener"); code != 204 {
		t.Fatalf("addChannel %d %s", code, body)
	}
	if m := <-f.attached; m != "listen" {
		t.Fatalf("mode %s", m)
	}
	if code, _ := do("POST", "/ari/bridges/abcdef123456/addChannel?channel=rtpchan1234"); code != 409 {
		t.Fatalf("twice: %d", code)
	}
	if code, _ := do("DELETE", "/ari/channels/rtpchan1234"); code != 204 {
		t.Fatalf("hangup %d", code)
	}
	if m := <-f.attached; m != "detached" {
		t.Fatalf("not detached: %s", m)
	}
	// AudioSocket: the program is called with the UUID.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		f, _ := audiosocket.ReadFrame(bufio.NewReader(c))
		got <- f.Payload
		c.Close()
	}()
	code, body = do("POST", "/ari/channels/externalMedia?channelId=aschan12345&external_host="+ln.Addr().String()+"&encapsulation=audiosocket&transport=tcp&format=slin&data=0123456789abcdef0123456789abcdef")
	if code != 200 || !strings.Contains(body, "AudioSocket/") {
		t.Fatalf("audiosocket %d %s", code, body)
	}
	if u := <-got; len(u) != 16 || u[0] != 0x01 || u[15] != 0xef {
		t.Fatalf("uuid % x", u)
	}
}
