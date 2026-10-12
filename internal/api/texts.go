package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sms"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// smsBase lets tests point a provider elsewhere.
func providerFor(p *config.SMSProvider) (sms.Provider, error) {
	return sms.NewWith(p.Kind, p.Username, p.Key, p.BaseURL, p.Account, p.App)
}

// sendText sends a text from one of our numbers and stores it, sent or failed.
func (s *Server) sendText(ctx context.Context, from, to, body, by string) (store.Text, error) {
	cfg := s.config()
	num := cfg.SMSNumberOf(from)
	if num == nil {
		return store.Text{}, fmt.Errorf("%s is not one of the texting numbers", from)
	}
	pv := cfg.SMSProviderOf(num.Provider)
	if pv == nil {
		return store.Text{}, fmt.Errorf("the provider for %s is gone", from)
	}
	if s.Store.OptedOut(num.Number, to) {
		return store.Text{}, fmt.Errorf("%s replied STOP: they cannot be texted from %s until they reply START", to, num.Number)
	}
	p, err := providerFor(pv)
	if err != nil {
		return store.Text{}, err
	}
	t := store.Text{Direction: "out", Our: num.Number, Other: to, Body: body, By: by, Status: "sent"}
	id, err := p.Send(ctx, num.Number, to, body)
	if err != nil {
		t.Status, t.Error = "failed", err.Error()
	}
	t.ProviderID = id
	saved, serr := s.Store.AddText(t)
	if serr != nil {
		return saved, serr
	}
	s.Bus.Publish("text.changed", saved)
	if err != nil {
		return saved, err
	}
	return saved, nil
}

func (s *Server) textRoutes() {
	s.handle("GET /api/texting/kinds", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": sms.Kinds})
	})
	s.handle("GET /api/texts", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Store.Conversations()})
	})
	s.handle("GET /api/texts/thread", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		our, other := r.URL.Query().Get("our"), r.URL.Query().Get("other")
		if our == "" || other == "" {
			writeError(w, 400, "say which conversation (our and other)")
			return
		}
		items := s.Store.Thread(our, other)
		if items == nil {
			items = []store.Text{}
		}
		writeJSON(w, 200, map[string]any{"items": items, "optedOut": s.Store.OptedOut(our, other)})
	})
	s.handle("POST /api/texts/read", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct{ Our, Other string }
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Store.MarkRead(in.Our, in.Other); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Bus.Publish("text.changed", nil)
		w.WriteHeader(204)
	})
	s.handle("POST /api/texts", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			From string `json:"from"`
			To   string `json:"to"`
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.To = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(in.To))
		in.Text = strings.TrimSpace(in.Text)
		var p config.Problems
		cfg := s.config()
		if cfg.SMSNumberOf(in.From) == nil {
			p = append(p, config.Problem{Field: "from", Message: "choose one of the texting numbers"})
		}
		if !strings.HasPrefix(in.To, "+") || !dialable(in.To) {
			p = append(p, config.Problem{Field: "to", Message: "enter the number with its country code, such as +12125550100"})
		}
		if in.Text == "" {
			p = append(p, config.Problem{Field: "text", Message: "write the message"})
		} else if sms.Parts(in.Text) > 6 {
			p = append(p, config.Problem{Field: "text", Message: "too long: use six texts' worth or less"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		t, err := s.sendText(ctx, in.From, in.To, in.Text, u.Username)
		if err != nil && t.ID == "" {
			writeError(w, 409, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "text.send", Detail: "from " + in.From + " to " + in.To + " " + t.Status, From: clientIP(r).String()})
		if err != nil {
			writeError(w, 502, "the provider did not send it: "+err.Error())
			return
		}
		writeJSON(w, 201, t)
	})
	// Received texts: the provider posts them here. The secret in the address proves it is ours.
	s.mux.HandleFunc("POST /hooks/sms/{provider}/{secret}", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		pv := cfg.SMSProviderOf(r.PathValue("provider"))
		if pv == nil || subtle.ConstantTimeCompare([]byte(r.PathValue("secret")), []byte(pv.HookSecret)) != 1 {
			http.NotFound(w, r)
			return
		}
		p, err := providerFor(pv)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		in, err := p.Inbound(r)
		if errors.Is(err, sms.ErrIgnore) {
			w.WriteHeader(http.StatusNoContent) // a receipt or another event: nothing to keep
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		num := cfg.SMSNumberOf(in.To)
		if num == nil && len(cfg.System.Texting.Numbers) == 1 {
			num = &cfg.System.Texting.Numbers[0] // some providers leave out the number it was sent to
		}
		if num == nil {
			http.Error(w, "not one of our texting numbers", 404)
			return
		}
		t, err := s.Store.AddText(store.Text{Direction: "in", Our: num.Number, Other: in.From, Body: in.Text, At: in.At, Status: "received", ProviderID: in.ID})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.Bus.Publish("text.new", t)
		go s.mailText(cfg, *num, t)
		w.WriteHeader(204)
	})
}

// mailText tells the number's people of a text received.
func (s *Server) mailText(cfg config.Config, num config.SMSNumber, t store.Text) {
	if cfg.System.Email.Host == "" {
		return
	}
	who := t.Other
	if c := s.directoryContact(t.Other); c != nil {
		who = c.Name + " (" + t.Other + ")"
	}
	for _, e := range num.Notify {
		x := cfg.FindExtension(e)
		if x == nil || x.Email == "" {
			continue
		}
		msg, err := buildMail(cfg.System.Email.From, x.Email, "Text from "+oneLine(who, ""), fmt.Sprintf("%s texted %s:\n\n%s\n\nReply in Texts in BitVoice.\n", who, num.Number, t.Body), "", "", nil)
		if err == nil {
			err = sendMail(cfg.System.Email, x.Email, msg)
		}
		if err != nil {
			s.Bus.Publish("email.failed", map[string]string{"text": t.ID, "error": err.Error()})
		}
	}
}
