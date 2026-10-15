package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/inbox"
	"github.com/biodream-llc/bitvoice/internal/sms"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// maxChatText is the longest message the inbox takes.
const maxChatText = 4000

// inboxConvView is a conversation as the inbox shows it: one shape for every channel, texts included.
type inboxConvView struct {
	store.InboxConv
	Our string `json:"our,omitempty"` // texts: our number
}

// receive stores a message from a contact, opening a conversation when there is none, and sends the
// automatic greeting to a new one.
func (s *Server) receive(channel, contact, name, extID, text string, at time.Time) (store.InboxConv, error) {
	c, ok := s.Store.ConvFor(channel, contact)
	isNew := !ok
	if !ok {
		c = store.InboxConv{ID: config.NewID() + config.NewID(), Channel: channel, Contact: contact, Name: name, State: store.ConvOpen,
			Started: time.Now().UTC(), Updated: time.Now().UTC()}
		if err := s.Store.PutConv(c); err != nil {
			return c, err
		}
	} else if name != "" && c.Name == "" {
		c.Name = name
		_ = s.Store.PutConv(c)
	}
	if extID != "" && s.Store.HasMsg(c.ID, extID) {
		return c, nil // the channel sent it twice
	}
	if len(text) > maxChatText {
		text = text[:maxChatText]
	}
	if _, err := s.Store.AddMsg(store.InboxMsg{Conv: c.ID, In: true, Text: text, ExtID: extID, At: at.UTC()}); err != nil {
		return c, err
	}
	c, _ = s.Store.Conv(c.ID)
	s.Bus.Publish("inbox.message", map[string]string{"conv": c.ID, "channel": channel})
	if g := s.config().System.Inbox.Greeting; isNew && g != "" {
		go func() { _, _ = s.reply(context.Background(), c, g, "auto") }()
	}
	return c, nil
}

// reply sends a message to a conversation's contact on its channel, and stores it, sent or failed.
func (s *Server) reply(ctx context.Context, c store.InboxConv, text, by string) (store.InboxMsg, error) {
	cfg := s.config()
	ib := cfg.System.Inbox
	m := store.InboxMsg{Conv: c.ID, Text: text, By: by, Status: "sent"}
	var id string
	var err error
	switch c.Channel {
	case "whatsapp":
		id, err = inbox.SendWhatsApp(ctx, ib.WhatsApp.PhoneNumberID, ib.WhatsApp.Token, c.Contact, text)
	case "messenger":
		switch inbox.MessengerWindow(c.LastIn) {
		case "open":
			id, err = inbox.SendMessenger(ctx, ib.Messenger.PageToken, c.Contact, text)
		case "agent":
			if by == "auto" {
				err = errors.New("an automatic message may not be sent after 24 hours")
			} else {
				id, err = inbox.SendMessengerTagged(ctx, ib.Messenger.PageToken, c.Contact, text)
			}
		default:
			err = errors.New("Messenger takes no reply more than 7 days after the person's last message")
		}
	case "webchat":
		// The visitor's box collects it.
	default:
		err = errors.New("unknown channel")
	}
	if err != nil {
		m.Status, m.Error = "failed", err.Error()
	}
	m.ExtID = id
	saved, serr := s.Store.AddMsg(m)
	if serr != nil {
		return saved, serr
	}
	s.Bus.Publish("inbox.message", map[string]string{"conv": c.ID, "channel": c.Channel})
	return saved, err
}

// window says which replies the conversation's channel takes now: open, agent (Messenger's human
// agent tag, up to 7 days), template (WhatsApp, only templates) or shut.
func window(c store.InboxConv) string {
	switch c.Channel {
	case "whatsapp":
		if time.Since(c.LastIn) < 24*time.Hour {
			return "open"
		}
		return "template"
	case "messenger":
		return inbox.MessengerWindow(c.LastIn)
	}
	return "open"
}

// templates are the WhatsApp templates, kept a few minutes so the reply box does not ask Meta each time.
type templateCache struct {
	mu   sync.Mutex
	key  string
	at   time.Time
	list []inbox.Template
}

func (s *Server) whatsAppTemplates(ctx context.Context, fresh bool) ([]inbox.Template, error) {
	wa := s.config().System.Inbox.WhatsApp
	key := wa.BusinessAccountID + "\x00" + wa.Token
	tc := &s.inboxTemplates
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if !fresh && tc.key == key && time.Since(tc.at) < 5*time.Minute {
		return tc.list, nil
	}
	list, err := inbox.WhatsAppTemplates(ctx, wa.BusinessAccountID, wa.Token)
	if err != nil {
		return nil, err
	}
	if len(list) > 0 { // an empty answer is asked again next time
		tc.key, tc.at, tc.list = key, time.Now(), list
	}
	return list, nil
}

func (s *Server) inboxRoutes() {
	// WhatsApp's approved templates, for replies after the 24-hour window.
	s.handle("GET /api/inbox/templates", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !s.config().System.Inbox.WhatsApp.Enabled {
			writeError(w, 404, "WhatsApp is off")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		list, err := s.whatsAppTemplates(ctx, r.URL.Query().Get("fresh") != "")
		if err != nil {
			writeError(w, 502, "WhatsApp's templates could not be read: "+err.Error())
			return
		}
		if list == nil {
			list = []inbox.Template{}
		}
		writeJSON(w, 200, list)
	})
	// Send a template into a WhatsApp conversation.
	s.handle("POST /api/inbox/{id}/template", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Name     string   `json:"name"`
			Language string   `json:"language"`
			Params   []string `json:"params"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		c, err := s.Store.Conv(r.PathValue("id"))
		if err != nil || c.Channel != "whatsapp" {
			writeError(w, 404, "no such WhatsApp conversation")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		list, err := s.whatsAppTemplates(ctx, false)
		if err != nil {
			writeError(w, 502, "WhatsApp's templates could not be read: "+err.Error())
			return
		}
		var tpl *inbox.Template
		for i := range list {
			if list[i].Name == in.Name && list[i].Language == in.Language {
				tpl = &list[i]
			}
		}
		switch {
		case tpl == nil:
			writeError(w, 400, "no template "+in.Name+" in "+in.Language)
			return
		case !tpl.Usable:
			writeError(w, 400, "this template cannot be sent from the inbox: "+tpl.Why)
			return
		case len(in.Params) != tpl.Params:
			writeError(w, 400, fmt.Sprintf("this template needs %d values", tpl.Params))
			return
		}
		for i, p := range in.Params {
			in.Params[i] = strings.TrimSpace(p)
			if in.Params[i] == "" || len(p) > 1000 || strings.ContainsAny(p, "\n\t") {
				writeError(w, 400, fmt.Sprintf("value %d must be filled in, on one line", i+1))
				return
			}
		}
		wa := s.config().System.Inbox.WhatsApp
		m := store.InboxMsg{Conv: c.ID, Text: tpl.Fill(in.Params), By: u.Username, Status: "sent"}
		id, err := inbox.SendWhatsAppTemplate(ctx, wa.PhoneNumberID, wa.Token, c.Contact, *tpl, in.Params)
		if err != nil {
			m.Status, m.Error = "failed", err.Error()
		}
		m.ExtID = id
		if c.Assignee == "" {
			c.Assignee = u.Username
			_ = s.Store.PutConv(c)
		}
		saved, serr := s.Store.AddMsg(m)
		if serr != nil {
			writeErr(w, serr)
			return
		}
		s.Bus.Publish("inbox.message", map[string]string{"conv": c.ID, "channel": c.Channel})
		s.audit(r, u.Username, "inbox.template", c.Contact+" "+tpl.Name+" "+saved.Status)
		if err != nil {
			writeJSON(w, 502, map[string]any{"message": "WhatsApp did not take it: " + err.Error(), "item": saved})
			return
		}
		writeJSON(w, 201, saved)
	})
	// Every conversation, texts included, newest first.
	s.handle("GET /api/inbox", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		state := r.URL.Query().Get("state")
		convs, err := s.Store.Convs(state, 500)
		if err != nil {
			writeErr(w, err)
			return
		}
		out := []inboxConvView{}
		for _, c := range convs {
			c.TokenHash = ""
			out = append(out, inboxConvView{InboxConv: c})
		}
		if state == "" || state == store.ConvOpen {
			for _, t := range s.Store.Conversations() {
				out = append(out, inboxConvView{InboxConv: store.InboxConv{ID: "sms:" + t.Our + ":" + t.Other, Channel: "sms", Contact: t.Other,
					Name: s.NameFor(t.Other), State: store.ConvOpen, Unread: t.Unread, Updated: t.Last.At, Preview: t.Last.Body}, Our: t.Our})
			}
		}
		slices.SortFunc(out, func(a, b inboxConvView) int { return b.Updated.Compare(a.Updated) })
		writeJSON(w, 200, out)
	})
	s.handle("GET /api/inbox/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		if our, other, ok := smsConv(id); ok {
			msgs := []store.InboxMsg{}
			for _, t := range s.Store.Thread(our, other) {
				msgs = append(msgs, store.InboxMsg{ID: t.ID, Conv: id, At: t.At, In: t.Direction == "in", Text: t.Body, By: t.By, Status: t.Status, Error: t.Error})
			}
			writeJSON(w, 200, map[string]any{"conversation": inboxConvView{InboxConv: store.InboxConv{ID: id, Channel: "sms", Contact: other, State: store.ConvOpen}, Our: our},
				"messages": msgs, "optedOut": s.Store.OptedOut(our, other)})
			return
		}
		c, err := s.Store.Conv(id)
		if err != nil {
			writeError(w, 404, "no such conversation")
			return
		}
		msgs, err := s.Store.Msgs(id, time.Time{}, 2000)
		if err != nil {
			writeErr(w, err)
			return
		}
		if msgs == nil {
			msgs = []store.InboxMsg{}
		}
		c.TokenHash = ""
		writeJSON(w, 200, map[string]any{"conversation": inboxConvView{InboxConv: c}, "messages": msgs,
			"windowOpen": c.Channel == "webchat" || time.Since(c.LastIn) < 24*time.Hour, "window": window(c)})
	})
	// Read, assign, close or reopen.
	s.handle("POST /api/inbox/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Read     bool    `json:"read"`
			State    string  `json:"state"`
			Assignee *string `json:"assignee"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		id := r.PathValue("id")
		if our, other, ok := smsConv(id); ok {
			if in.Read {
				_ = s.Store.MarkRead(our, other)
				s.Bus.Publish("text.changed", nil)
			}
			w.WriteHeader(204)
			return
		}
		c, err := s.Store.Conv(id)
		if err != nil {
			writeError(w, 404, "no such conversation")
			return
		}
		if in.Read {
			c.Unread = 0
		}
		switch in.State {
		case "":
		case store.ConvOpen, store.ConvClosed:
			c.State = in.State
		default:
			writeError(w, 400, "state is open or closed")
			return
		}
		if in.Assignee != nil {
			if *in.Assignee != "" {
				if _, ok := s.Store.User(*in.Assignee); !ok {
					writeError(w, 400, "no such user")
					return
				}
			}
			c.Assignee = *in.Assignee
		}
		if err := s.Store.PutConv(c); err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("inbox.message", map[string]string{"conv": c.ID, "channel": c.Channel})
		w.WriteHeader(204)
	})
	s.handle("POST /api/inbox/{id}/reply", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" || len(in.Text) > maxChatText {
			writeError(w, 400, "write the message (up to 4000 characters)")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		id := r.PathValue("id")
		if our, other, ok := smsConv(id); ok {
			if sms.Parts(in.Text) > 6 {
				writeError(w, 400, "too long for a text: use six texts' worth or less")
				return
			}
			t, err := s.sendText(ctx, our, other, in.Text, u.Username)
			if err != nil && t.ID == "" {
				writeError(w, 409, err.Error())
				return
			}
			writeJSON(w, 201, store.InboxMsg{ID: t.ID, Conv: id, At: t.At, Text: t.Body, By: t.By, Status: t.Status, Error: t.Error})
			return
		}
		c, err := s.Store.Conv(id)
		if err != nil {
			writeError(w, 404, "no such conversation")
			return
		}
		if c.Assignee == "" {
			c.Assignee = u.Username // the first to answer takes it
			_ = s.Store.PutConv(c)
		}
		m, err := s.reply(ctx, c, in.Text, u.Username)
		s.audit(r, u.Username, "inbox.reply", c.Channel+" "+c.Contact+" "+m.Status)
		if err != nil {
			writeJSON(w, 502, map[string]any{"message": c.Channel + " did not take it: " + err.Error(), "item": m})
			return
		}
		writeJSON(w, 201, m)
	})

	// Meta's webhooks: the address is checked once with a GET, then messages come by POST, signed.
	for _, ch := range []string{"whatsapp", "messenger"} {
		ch := ch
		s.mux.HandleFunc("GET /hooks/"+ch, func(w http.ResponseWriter, r *http.Request) {
			ib := s.config().System.Inbox
			tok := ib.WhatsApp.VerifyToken
			if ch == "messenger" {
				tok = ib.Messenger.VerifyToken
			}
			if c := inbox.Challenge(r.URL.Query(), tok); c != "" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, c)
				return
			}
			http.Error(w, "the verify token does not match", http.StatusForbidden)
		})
		s.mux.HandleFunc("POST /hooks/"+ch, func(w http.ResponseWriter, r *http.Request) {
			ib := s.config().System.Inbox
			on, secret := ib.WhatsApp.Enabled, ib.WhatsApp.AppSecret
			if ch == "messenger" {
				on, secret = ib.Messenger.Enabled, ib.Messenger.AppSecret
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil || !on {
				http.NotFound(w, r)
				return
			}
			if !inbox.VerifySignature(body, r.Header.Get("X-Hub-Signature-256"), secret) {
				http.Error(w, "bad signature", http.StatusUnauthorized)
				return
			}
			var msgs []inbox.Incoming
			if ch == "whatsapp" {
				msgs, err = inbox.ParseWhatsApp(body)
			} else {
				msgs, err = inbox.ParseMessenger(body)
			}
			if err != nil {
				http.Error(w, "not a webhook Meta sends", http.StatusBadRequest)
				return
			}
			for _, m := range msgs {
				name := m.Name
				if ch == "messenger" && name == "" {
					if _, known := s.Store.ConvFor("messenger", m.From); !known {
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						name = inbox.MessengerName(ctx, ib.Messenger.PageToken, m.From)
						cancel()
					}
				}
				if _, err := s.receive(ch, m.From, name, m.ID, m.Text, m.At); err != nil {
					s.Log.Warn("inbox: a message could not be kept", "channel", ch, "err", err)
				}
			}
			w.WriteHeader(200) // Meta sends again until it hears 200
		})
	}
	s.webChatRoutes()
}

// smsConv splits an inbox ID for a text conversation (sms:our:other).
func smsConv(id string) (our, other string, ok bool) {
	rest, ok := strings.CutPrefix(id, "sms:")
	if !ok {
		return "", "", false
	}
	our, other, ok = strings.Cut(rest, ":")
	return our, other, ok && our != "" && other != ""
}

// webChatCORS lets the chat box on a listed site (any site, when none are listed) reach these routes.
func (s *Server) webChatCORS(w http.ResponseWriter, r *http.Request) bool {
	wc := s.config().System.Inbox.WebChat
	if !wc.Enabled {
		writeError(w, 404, "the web chat is switched off")
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		if len(wc.Origins) > 0 && !slices.Contains(wc.Origins, strings.TrimRight(origin, "/")) {
			writeError(w, 403, "the chat box is not allowed on this site")
			return false
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-BitVoice")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Max-Age", "600")
		h.Add("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return false
	}
	return true
}

// visitor finds the web chat conversation a visitor's key opens.
func (s *Server) visitor(w http.ResponseWriter, r *http.Request) (store.InboxConv, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, err := s.Store.Conv(r.PathValue("id"))
	if err != nil || c.Channel != "webchat" || tok == "" || subtle.ConstantTimeCompare([]byte(store.HashToken(tok)), []byte(c.TokenHash)) != 1 {
		writeError(w, 404, "no such chat")
		return c, false
	}
	return c, true
}

func (s *Server) webChatRoutes() {
	for _, p := range []string{"/api/webchat/start", "/api/webchat/{id}/messages"} {
		s.mux.HandleFunc("OPTIONS "+p, func(w http.ResponseWriter, r *http.Request) { s.webChatCORS(w, r) })
	}
	// How the box looks, for the script.
	s.noteRoute("GET /api/webchat/config", false, true)
	s.mux.HandleFunc("GET /api/webchat/config", func(w http.ResponseWriter, r *http.Request) {
		if !s.webChatCORS(w, r) {
			return
		}
		wc := s.config().System.Inbox.WebChat
		writeJSON(w, 200, map[string]string{"title": wc.Title, "welcome": wc.Welcome, "colour": wc.Colour})
	})
	// A visitor starts a chat: a conversation and the key to it, kept in the visitor's browser.
	s.noteRoute("POST /api/webchat/start", false, true)
	s.mux.HandleFunc("POST /api/webchat/start", func(w http.ResponseWriter, r *http.Request) {
		if !s.webChatCORS(w, r) {
			return
		}
		ip := clientIP(r).String()
		if s.throttled(ip) {
			writeError(w, http.StatusTooManyRequests, "too many chats from here: wait a few minutes")
			return
		}
		s.loginFailed(ip) // counts towards the limit: ten new chats per address in ten minutes
		var in struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Text  string `json:"text"`
			Page  string `json:"page"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Name, in.Text = strings.TrimSpace(in.Name), strings.TrimSpace(in.Text)
		if in.Text == "" || len(in.Text) > maxChatText || len(in.Name) > 80 || len(in.Email) > 120 {
			writeError(w, 400, "write a message (up to 4000 characters) and a name (up to 80)")
			return
		}
		if in.Name == "" {
			in.Name = "Visitor"
		}
		raw := make([]byte, 24)
		_, _ = rand.Read(raw)
		tok := base64.RawURLEncoding.EncodeToString(raw)
		page := ""
		if u, err := url.Parse(in.Page); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
			page = u.Scheme + "://" + u.Host + u.Path
		}
		c := store.InboxConv{ID: config.NewID() + config.NewID(), Channel: "webchat", Contact: "web-" + config.NewID(), Name: in.Name,
			Email: strings.TrimSpace(in.Email), State: store.ConvOpen, Started: time.Now().UTC(), Updated: time.Now().UTC(), Page: page,
			TokenHash: store.HashToken(tok)}
		if err := s.Store.PutConv(c); err != nil {
			writeErr(w, err)
			return
		}
		if _, err := s.receive("webchat", c.Contact, c.Name, "", in.Text, time.Now()); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]string{"id": c.ID, "token": tok})
	})
	// The visitor's messages: send one, or read those after a time (polled by the box).
	s.noteRoute("GET /api/webchat/{id}/messages", false, true)
	s.mux.HandleFunc("GET /api/webchat/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if !s.webChatCORS(w, r) {
			return
		}
		c, ok := s.visitor(w, r)
		if !ok {
			return
		}
		after := time.Time{}
		if ms, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64); err == nil {
			after = time.UnixMilli(ms)
		}
		msgs, err := s.Store.Msgs(c.ID, after, 500)
		if err != nil {
			writeErr(w, err)
			return
		}
		type out struct {
			ID   string `json:"id"`
			At   int64  `json:"at"`
			In   bool   `json:"in"`
			Text string `json:"text"`
		}
		res := []out{}
		for _, m := range msgs {
			if m.Status == "failed" {
				continue
			}
			res = append(res, out{ID: m.ID, At: m.At.UnixMilli(), In: m.In, Text: m.Text})
		}
		writeJSON(w, 200, map[string]any{"messages": res, "closed": c.State == store.ConvClosed})
	})
	s.noteRoute("POST /api/webchat/{id}/messages", false, true)
	s.mux.HandleFunc("POST /api/webchat/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if !s.webChatCORS(w, r) {
			return
		}
		c, ok := s.visitor(w, r)
		if !ok {
			return
		}
		var in struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" || len(in.Text) > maxChatText {
			writeError(w, 400, "write a message (up to 4000 characters)")
			return
		}
		if _, err := s.receive("webchat", c.Contact, "", "", in.Text, time.Now()); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
	// The box itself, for <script src="…/webchat.js" async></script> on your site.
	s.mux.HandleFunc("GET /webchat.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = io.WriteString(w, webChatJS)
	})
}
