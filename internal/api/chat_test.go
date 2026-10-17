package api

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestStaffChat(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	if _, _, err := s.Store.Update("test", "PINs", 0, func(c *config.Config) error {
		c.Extensions = append(c.Extensions, config.Extension{Number: "102", Name: "Sam", Secret: config.NewSecret(), Enabled: true, RingSeconds: 20})
		for _, n := range []string{"100", "101", "102"} {
			if x := c.FindExtension(n); x != nil {
				x.Voicemail.Enabled, x.Voicemail.PIN = true, "2468"
			}
		}
		c.System.Chat.Rooms = []config.ChatRoom{{ID: "front", Name: "Front desk", Members: []string{"100", "101"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Engine.Apply(s.config())
	login := func(ext string) *client {
		c := newClient(t, ts.URL)
		if code, _ := c.do("POST", "/api/portal/login", map[string]any{"extension": ext, "pin": "2468"}); code != 200 {
			t.Fatalf("portal sign-in %s: %d", ext, code)
		}
		return c
	}
	a, b, sam := login("100"), login("101"), login("102")

	// 101's live stream, to see the message arrive.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/me/events", nil)
	resp, err := b.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	if code, _ := a.do("POST", "/api/me/chat/d-100-101", map[string]any{"text": "  Can you take line 2?  "}); code != 201 {
		t.Fatalf("send: %d", code)
	}
	deadline := time.After(15 * time.Second)
	for got := false; !got; {
		select {
		case l := <-lines:
			got = strings.Contains(l, "Can you take line 2?") && strings.Contains(l, `"key":"d-100-101"`)
		case <-deadline:
			t.Fatal("the message did not reach 101's stream")
		}
	}
	// 101 sees one unread from 100, reads it (which marks it read), and replies.
	_, list := b.do("GET", "/api/me/chat", nil)
	if !strings.Contains(asJSON(list), `"extension":"100"`) || !strings.Contains(asJSON(list), `"unread":1`) || !strings.Contains(asJSON(list), `"name":"Front desk"`) {
		t.Fatalf("chat list %v", list)
	}
	_, msgs := b.do("GET", "/api/me/chat/d-100-101", nil)
	if items := msgs["items"].([]any); len(items) != 1 || items[0].(map[string]any)["text"] != "Can you take line 2?" {
		t.Fatalf("messages %v", msgs)
	}
	if _, list := b.do("GET", "/api/me/chat", nil); strings.Contains(asJSON(list), `"unread":1`) {
		t.Fatal("still unread after reading")
	}
	// Nobody reads or writes a conversation they are not in.
	for _, key := range []string{"d-100-101", "r-front", "d-100-999", "d-101-100"} {
		if code, _ := sam.do("GET", "/api/me/chat/"+key, nil); code != 404 {
			t.Errorf("102 read %s: %d", key, code)
		}
	}
	if code, _ := sam.do("POST", "/api/me/chat/r-front", map[string]any{"text": "hi"}); code != 404 {
		t.Error("102 wrote in a room it is not in")
	}
	if code, _ := b.do("POST", "/api/me/chat/r-front", map[string]any{"text": "Lunch at 1"}); code != 201 {
		t.Error("room message")
	}
	if code, _ := a.do("POST", "/api/me/chat/d-100-101", map[string]any{"text": ""}); code != 422 {
		t.Error("empty message accepted")
	}
	// A status, seen by colleagues; presence says who is offline (no phone here).
	if code, _ := a.do("PUT", "/api/me/chat-status", map[string]any{"status": "At lunch"}); code != 204 {
		t.Fatal("status")
	}
	if _, list := sam.do("GET", "/api/me/chat", nil); !strings.Contains(asJSON(list), `"status":"At lunch"`) || !strings.Contains(asJSON(list), `"state":"offline"`) {
		t.Fatalf("presence %v", list)
	}
	// Administrators' screens never get the messages.
	if code, _ := admin.do("GET", "/api/me/chat", nil); code != 401 {
		t.Fatalf("admin read chat: %d", code)
	}
	// Chat switched off: refused.
	_, _, _ = s.Store.Update("test", "off", 0, func(c *config.Config) error { c.System.Chat.Off = true; return nil })
	if code, _ := a.do("GET", "/api/me/chat", nil); code != 403 {
		t.Fatalf("chat off: %d", code)
	}
}
