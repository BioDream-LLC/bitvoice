package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// BookCallback stores a callback an AI receptionist took, tells every open screen and emails the owner
// of the receptionist's message box. The engine calls it (Engine.Callback).
func (s *Server) BookCallback(c store.Callback) error {
	nc, err := s.Store.AddCallback(c)
	if err != nil {
		return err
	}
	s.Bus.Publish("callback.new", nc)
	go func() {
		if err := s.mailCallback(s.config(), nc); err != nil {
			s.Log.Warn("callback email failed", "err", err)
			s.Bus.Publish("email.failed", map[string]string{"callback": nc.ID, "error": err.Error()})
		}
	}()
	return nil
}

// mailCallback emails a new callback to the person whose mailbox takes the receptionist's messages.
func (s *Server) mailCallback(cfg config.Config, c store.Callback) error {
	if cfg.System.Email.Host == "" {
		return nil
	}
	var x *config.Extension
	for _, r := range cfg.Receptionists {
		if r.Name == c.Receptionist {
			x = cfg.FindExtension(r.MessageBox)
		}
	}
	if x == nil || x.Email == "" {
		return nil
	}
	subject := "Callback wanted: " + oneLine(c.Name, c.Number)
	body := fmt.Sprintf("%s took a callback request at %s.\n\nName: %s\nNumber: %s\nWhen: %s\nAbout: %s\n\nOpen Callbacks in BitVoice to ring them back and mark it done.\n",
		c.Receptionist, c.Created.In(cfg.System.Location()).Format("Mon 2 Jan 15:04"), c.Name, c.Number, c.When, c.Reason)
	msg, err := buildMail(cfg.System.Email.From, x.Email, subject, body, "", "", nil)
	if err != nil {
		return err
	}
	return sendMail(cfg.System.Email, x.Email, msg)
}

// oneLine joins a name and a number for a mail subject, without line breaks.
func oneLine(name, number string) string {
	s := strings.TrimSpace(name + " " + number)
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

type callbackRequest struct {
	Name   string `json:"name"`
	Number string `json:"number"`
	When   string `json:"when"`
	Reason string `json:"reason"`
}

func (in callbackRequest) check() (store.Callback, error) {
	var p config.Problems
	c := store.Callback{Name: strings.TrimSpace(in.Name), Number: strings.TrimSpace(in.Number), When: strings.TrimSpace(in.When), Reason: strings.TrimSpace(in.Reason)}
	if !dialable(c.Number) {
		p = append(p, config.Problem{Field: "number", Message: "enter the number to call back, such as +12125550123"})
	}
	if len(c.Name) > 100 {
		p = append(p, config.Problem{Field: "name", Message: "keep the name under 100 characters"})
	}
	if len(c.When) > 200 {
		p = append(p, config.Problem{Field: "when", Message: "keep this under 200 characters"})
	}
	if len(c.Reason) > 500 {
		p = append(p, config.Problem{Field: "reason", Message: "keep this under 500 characters"})
	}
	if len(p) > 0 {
		return c, p
	}
	return c, nil
}

// dialable is true for a phone number or extension: digits, an optional leading +, 2 to 15 digits.
func dialable(n string) bool {
	d := strings.TrimPrefix(n, "+")
	if len(d) < 2 || len(d) > 15 {
		return false
	}
	for _, r := range d {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Server) callbacksRoutes() {
	s.handle("GET /api/callbacks", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cs := s.Store.Callbacks()
		if cs == nil {
			cs = []store.Callback{}
		}
		writeJSON(w, 200, map[string]any{"items": cs})
	})
	s.handle("POST /api/callbacks", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in callbackRequest
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		c, err := in.check()
		if err != nil {
			writeErr(w, err)
			return
		}
		c.Receptionist = "" // booked by a person
		nc, err := s.Store.AddCallback(c)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("callback.changed", nc)
		writeJSON(w, 201, nc)
	})
	s.handle("PUT /api/callbacks/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		switch in.Status {
		case "open", "done", "cancelled":
		default:
			writeErr(w, config.Problems{{Field: "status", Message: "choose open, done or cancelled"}})
			return
		}
		if len(in.Note) > 1000 {
			writeErr(w, config.Problems{{Field: "note", Message: "keep the note under 1000 characters"}})
			return
		}
		c, err := s.Store.SetCallback(r.PathValue("id"), in.Status, strings.TrimSpace(in.Note), u.Username)
		if errors.Is(err, store.ErrNoCallback) {
			writeJSON(w, 404, apiError{Message: "no such callback"})
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("callback.changed", c)
		writeJSON(w, 200, c)
	})
	s.handle("DELETE /api/callbacks/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.DeleteCallback(r.PathValue("id")); errors.Is(err, store.ErrNoCallback) {
			writeJSON(w, 404, apiError{Message: "no such callback"})
			return
		} else if err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("callback.changed", map[string]string{"id": r.PathValue("id")})
		w.WriteHeader(204)
	})
	// Ring back: ring the chosen extension and, once it answers, call the number for it.
	s.handle("POST /api/callbacks/{id}/call", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Extension string `json:"extension"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		var cb *store.Callback
		for _, c := range s.Store.Callbacks() {
			if c.ID == r.PathValue("id") {
				cb = &c
				break
			}
		}
		if cb == nil {
			writeJSON(w, 404, apiError{Message: "no such callback"})
			return
		}
		cfg, _ := s.Store.Config()
		if x := cfg.FindExtension(in.Extension); x == nil {
			writeErr(w, config.Problems{{Field: "extension", Message: "choose the extension to ring first"}})
			return
		}
		id, err := s.Engine.Originate(in.Extension, cb.Number, 25*time.Second)
		if err != nil {
			writeJSON(w, 409, apiError{Message: err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"call": id})
	})
}
