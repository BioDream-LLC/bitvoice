package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// ChatEvent is a message as the portal stream sends it: the conversation it is in, and who may see it.
type ChatEvent struct {
	Key     string            `json:"key"`
	Message store.ChatMessage `json:"message"`
	To      []string          `json:"-"` // the extensions it goes to
}

// conversationFor checks that ext may use a conversation: a direct one with an existing colleague, or
// a room it is in. It returns the extensions in it.
func conversationFor(cfg *config.Config, ext, key string) ([]string, bool) {
	if rest, ok := strings.CutPrefix(key, "d-"); ok {
		a, b, ok := strings.Cut(rest, "-")
		if !ok || (a != ext && b != ext) || a == b || store.DirectKey(a, b) != key {
			return nil, false
		}
		if cfg.FindExtension(a) == nil || cfg.FindExtension(b) == nil {
			return nil, false
		}
		return []string{a, b}, true
	}
	if id, ok := strings.CutPrefix(key, "r-"); ok {
		for _, r := range cfg.System.Chat.Rooms {
			if r.ID == id && r.InRoom(ext) {
				if len(r.Members) > 0 {
					return r.Members, true
				}
				var all []string
				for _, x := range cfg.Extensions {
					if x.Enabled {
						all = append(all, x.Number)
					}
				}
				return all, true
			}
		}
	}
	return nil, false
}

func (s *Server) chatRoutes() {
	chatOn := func(w http.ResponseWriter, x config.Extension) (config.Config, bool) {
		cfg, _ := s.Store.Config()
		if x.GuestRoom {
			writeError(w, http.StatusForbidden, "chat is for staff, not guest rooms")
			return cfg, false
		}
		if cfg.System.Chat.Off {
			writeError(w, http.StatusForbidden, "chat is switched off")
			return cfg, false
		}
		return cfg, true
	}
	// Who is who, how they are, and the conversations with something unread.
	s.handlePortal("GET /api/me/chat", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		cfg, ok := chatOn(w, x)
		if !ok {
			return
		}
		statuses := s.Store.ChatStatuses()
		people := s.Engine.Colleagues(&cfg)
		type person struct {
			Extension  string `json:"extension"`
			Name       string `json:"name"`
			Department string `json:"department,omitempty"`
			State      string `json:"state"`
			Reason     string `json:"reason,omitempty"`
			Status     string `json:"status,omitempty"`
			Unread     int    `json:"unread"`
		}
		read := s.Store.ChatRead(x.Number)
		unread := func(key string) int {
			ms, _ := s.Store.ChatMessages(key, read[key], 0)
			n := 0
			for _, m := range ms {
				if m.From != x.Number {
					n++
				}
			}
			return n
		}
		out := []person{}
		for _, p := range people {
			if p.Extension == x.Number {
				continue
			}
			out = append(out, person{p.Extension, p.Name, p.Department, p.State, p.Reason, statuses[p.Extension], unread(store.DirectKey(x.Number, p.Extension))})
		}
		type room struct {
			ID      string `json:"id"`
			Key     string `json:"key"`
			Name    string `json:"name"`
			Members int    `json:"members"`
			Unread  int    `json:"unread"`
		}
		rooms := []room{}
		for _, rm := range cfg.System.Chat.Rooms {
			if rm.InRoom(x.Number) {
				n := len(rm.Members)
				if n == 0 {
					n = len(people)
				}
				rooms = append(rooms, room{rm.ID, "r-" + rm.ID, rm.Name, n, unread("r-" + rm.ID)})
			}
		}
		writeJSON(w, 200, map[string]any{"me": x.Number, "status": statuses[x.Number], "people": out, "rooms": rooms})
	})
	s.handlePortal("GET /api/me/chat/{key}", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		cfg, ok := chatOn(w, x)
		if !ok {
			return
		}
		key := r.PathValue("key")
		if _, ok := conversationFor(&cfg, x.Number, key); !ok {
			writeError(w, http.StatusNotFound, "no such conversation")
			return
		}
		var since time.Time
		if v := r.URL.Query().Get("since"); v != "" {
			since, _ = time.Parse(time.RFC3339Nano, v)
		}
		ms, err := s.Store.ChatMessages(key, since, 200)
		if err != nil {
			writeErr(w, err)
			return
		}
		if n := len(ms); n > 0 {
			_ = s.Store.MarkChatRead(x.Number, key, ms[n-1].At)
		}
		writeJSON(w, 200, map[string]any{"items": ms})
	})
	s.handlePortal("POST /api/me/chat/{key}", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		cfg, ok := chatOn(w, x)
		if !ok {
			return
		}
		key := r.PathValue("key")
		to, ok := conversationFor(&cfg, x.Number, key)
		if !ok {
			writeError(w, http.StatusNotFound, "no such conversation")
			return
		}
		var in struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "send the message as text")
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" || utf8.RuneCountInString(in.Text) > 4000 || !utf8.ValidString(in.Text) {
			writeErr(w, config.Problems{{Field: "text", Message: "write a message of up to 4000 characters"}})
			return
		}
		m, err := s.Store.AddChatMessage(key, store.ChatMessage{From: x.Number, Text: in.Text}, cfg.System.Chat.KeepDays)
		if err != nil {
			writeErr(w, err)
			return
		}
		_ = s.Store.MarkChatRead(x.Number, key, m.At)
		s.Bus.Publish("chat.message", ChatEvent{Key: key, Message: m, To: to})
		writeJSON(w, 201, m)
	})
	s.handlePortal("PUT /api/me/chat-status", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		var in struct {
			Status string `json:"status"`
		}
		_ = readJSON(r, &in)
		in.Status = strings.TrimSpace(in.Status)
		if utf8.RuneCountInString(in.Status) > 80 {
			writeErr(w, config.Problems{{Field: "status", Message: "keep it to 80 characters"}})
			return
		}
		if err := s.Store.SetChatStatus(x.Number, in.Status); err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("presence.changed", map[string]string{"extension": x.Number})
		w.WriteHeader(http.StatusNoContent)
	})
	// The portal's live stream: chat messages for this person, and colleagues' presence changing.
	s.handlePortal("GET /api/me/events", func(w http.ResponseWriter, r *http.Request, u store.User, x config.Extension) {
		fl, ok := w.(http.Flusher)
		if !ok {
			writeError(w, 500, "streaming unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
		sub := s.Bus.Subscribe()
		defer s.Bus.Unsubscribe(sub)
		fmt.Fprint(w, "event: hello\ndata: {}\n\n")
		fl.Flush()
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
			case ev := <-sub.C:
				if !s.forPortal(ev, x.Number) {
					continue
				}
				b, _ := json.Marshal(ev.Data)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			}
			fl.Flush()
		}
	})
}

// forPortal says whether an event goes to a person's portal stream: their chat messages, and
// presence (calls starting and ending, do not disturb), never anything else.
func (s *Server) forPortal(ev events.Event, ext string) bool {
	switch ev.Type {
	case "chat.message":
		ce, ok := ev.Data.(ChatEvent)
		if !ok {
			return false
		}
		for _, t := range ce.To {
			if t == ext {
				return true
			}
		}
		return false
	case "presence.changed", "call.started", "call.ended", "registrations.changed":
		return true
	case "voicemail.changed":
		if m, ok := ev.Data.(map[string]any); ok {
			return m["box"] == ext
		}
	}
	return false
}

