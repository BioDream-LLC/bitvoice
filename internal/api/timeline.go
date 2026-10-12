package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// TimelineItem is one thing that happened with a customer: a call, a voicemail, a text, a callback
// or a note.
type TimelineItem struct {
	Kind string    `json:"kind"` // call, voicemail, text, callback, note
	At   time.Time `json:"at"`
	ID   string    `json:"id"`
	// Title is one line in words; Detail is more (a summary, the text, the note).
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Direction for calls and texts: inbound / outbound (calls), in / out (texts).
	Direction string `json:"direction,omitempty"`
	// Recording names a call's recording; Box and the ID find a voicemail.
	Recording string `json:"recording,omitempty"`
	Box       string `json:"box,omitempty"`
	By        string `json:"by,omitempty"`
	Status    string `json:"status,omitempty"`
}

// Timeline is everything about one number, newest first.
type Timeline struct {
	Number  string         `json:"number"`
	Contact *Contact       `json:"contact"`
	Items   []TimelineItem `json:"items"`
}

func (s *Server) timeline(cfg config.Config, number string) Timeline {
	tl := Timeline{Number: number, Items: []TimelineItem{}}
	if d := s.directoryContact(number); d != nil {
		tl.Contact = &Contact{Name: d.Name, Company: d.Company, Notes: d.Notes, Source: "directory"}
	}
	rs, _ := s.Store.CDRs(store.CDRQuery{Since: time.Now().AddDate(-1, 0, 0), Limit: 5000})
	for _, r := range rs {
		other := r.From
		if r.Direction == "outbound" {
			other = r.To
		}
		if !config.SameNumber(other, number) {
			continue
		}
		var title string
		switch {
		case r.Disposition == "blocked":
			title = "Call refused: " + r.Cause
		case r.Direction == "outbound" && r.Disposition == "answered":
			title = firstNonEmpty(r.FromName, r.From) + " called them (" + talk(r.Billsec) + ")"
		case r.Direction == "outbound":
			title = firstNonEmpty(r.FromName, r.From) + " called them: " + strings.ReplaceAll(r.Disposition, "-", " ")
		case r.Disposition == "answered":
			title = "Called; answered by " + firstNonEmpty(r.AnsweredBy, r.ToName, r.To) + " (" + talk(r.Billsec) + ")"
		default:
			title = "Called " + firstNonEmpty(r.ToName, r.To) + ": " + strings.ReplaceAll(r.Disposition, "-", " ")
		}
		it := TimelineItem{Kind: "call", At: r.Start, ID: r.ID, Title: title, Direction: r.Direction, Recording: r.Recording, Status: r.Disposition}
		if r.Recording != "" {
			if t, err := s.Store.CallTranscript(r.Recording); err == nil && t.Summary != "" {
				it.Detail = t.Summary
			}
		}
		tl.Items = append(tl.Items, it)
	}
	if vms, err := s.Store.Voicemails(""); err == nil {
		for _, v := range vms {
			if config.SameNumber(v.From, number) {
				name := v.Box
				if x := cfg.FindExtension(v.Box); x != nil {
					name = x.Name
				}
				tl.Items = append(tl.Items, TimelineItem{Kind: "voicemail", At: v.At, ID: v.ID, Box: v.Box, Title: "Left a voicemail for " + name + " (" + talk(v.Seconds) + ")",
					Detail: firstNonEmpty(v.Summary, v.Transcript)})
			}
		}
	}
	for _, t := range s.Store.Texts() {
		if config.SameNumber(t.Other, number) {
			title := "Texted " + t.Our
			if t.Direction == "out" {
				title = "Sent a text from " + t.Our
				if t.Status == "failed" {
					title += " (not sent)"
				}
			}
			tl.Items = append(tl.Items, TimelineItem{Kind: "text", At: t.At, ID: t.ID, Title: title, Detail: t.Body, Direction: t.Direction, By: t.By, Status: t.Status})
		}
	}
	for _, c := range s.Store.Callbacks() {
		if config.SameNumber(c.Number, number) {
			title := "Asked for a callback"
			if c.When != "" {
				title += " (" + c.When + ")"
			}
			tl.Items = append(tl.Items, TimelineItem{Kind: "callback", At: c.Created, ID: c.ID, Title: title, Detail: c.Reason, Status: c.Status, By: c.By})
		}
	}
	for _, n := range s.Store.Notes(number) {
		tl.Items = append(tl.Items, TimelineItem{Kind: "note", At: n.At, ID: n.ID, Title: "Note by " + n.By, Detail: n.Text, By: n.By})
	}
	slices.SortFunc(tl.Items, func(a, b TimelineItem) int { return b.At.Compare(a.At) })
	return tl
}

func talk(sec int) string {
	if sec < 60 {
		return strconv.Itoa(sec) + " s"
	}
	if sec%60 == 0 {
		return strconv.Itoa(sec/60) + " min"
	}
	return strconv.Itoa(sec/60) + " min " + strconv.Itoa(sec%60) + " s"
}

func (s *Server) timelineRoutes() {
	s.handle("GET /api/timeline", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		n := strings.TrimSpace(r.URL.Query().Get("number"))
		if len(digitsOf(n)) < 3 {
			writeError(w, 400, "say whose timeline: a phone number")
			return
		}
		writeJSON(w, 200, s.timeline(s.config(), n))
	})
	s.handle("POST /api/timeline/notes", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct{ Number, Text string }
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		var p config.Problems
		if len(digitsOf(in.Number)) < 3 {
			p = append(p, config.Problem{Field: "number", Message: "say whose note this is"})
		}
		if in.Text == "" {
			p = append(p, config.Problem{Field: "text", Message: "write the note"})
		} else if len(in.Text) > 4000 {
			p = append(p, config.Problem{Field: "text", Message: "use 4000 characters or fewer"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		n, err := s.Store.AddNote(store.Note{Number: in.Number, By: firstNonEmpty(u.Name, u.Username), Text: in.Text})
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Bus.Publish("timeline.changed", map[string]string{"number": in.Number})
		writeJSON(w, 201, n)
	})
	s.handle("DELETE /api/timeline/notes/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.DeleteNote(r.PathValue("id")); errors.Is(err, store.ErrNoNote) {
			writeError(w, 404, "no such note")
			return
		} else if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Bus.Publish("timeline.changed", nil)
		w.WriteHeader(204)
	})
}
