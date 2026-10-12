package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/fax"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// FaxRecord is a fax received, kept in the data folder (faxes/ID.json, ID.pdf, ID.tif).
type FaxRecord struct {
	ID      string    `json:"id"`
	Box     string    `json:"box"`
	BoxName string    `json:"boxName"`
	From    string    `json:"from"`
	Station string    `json:"station"`
	At      time.Time `json:"at"`
	Pages   int       `json:"pages"`
	Status  string    `json:"status"` // received, partial (some pages, then it broke), failed
	Error   string    `json:"error,omitempty"`
	Emailed []string  `json:"emailed,omitempty"`

	// Faxes sent: Direction "out" (received ones are "in"), and how the sending went.
	Direction string    `json:"direction"`
	To        string    `json:"to,omitempty"`
	By        string    `json:"by,omitempty"`      // who sent it: a user, or an email address
	Subject   string    `json:"subject,omitempty"` // the email's subject, for email to fax
	ReplyTo   string    `json:"replyTo,omitempty"` // who hears by email how it went
	Tries     int       `json:"tries,omitempty"`
	NextTry   time.Time `json:"nextTry,omitempty"`
	Sent      int       `json:"sent,omitempty"`     // pages the receiver confirmed
	Rate      int       `json:"rate,omitempty"`     // bit/s
	RemoteID  string    `json:"remoteId,omitempty"` // the receiving machine's identity
	SentAt    time.Time `json:"sentAt,omitempty"`
}

var faxID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (s *Server) faxDir() string { return filepath.Join(s.DataDir, "faxes") }

// FaxReceived keeps a fax the engine received and emails it to its box's addresses.
func (s *Server) FaxReceived(in pbx.FaxIn) {
	cfg := s.config()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	r := FaxRecord{ID: hex.EncodeToString(b), Direction: "in", Box: in.Box, From: in.From, Station: in.Station, At: in.At,
		Pages: len(in.Result.Pages), Status: "received"}
	box := cfg.FindFax(in.Box)
	if box != nil {
		r.BoxName = box.Name
	}
	if in.Result.Err != nil {
		r.Error = in.Result.Err.Error()
		r.Status = "failed"
		if r.Pages > 0 {
			r.Status = "partial"
		}
	}
	dir := s.faxDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.Log.Error("cannot keep faxes", "err", err)
		return
	}
	var pdf []byte
	if r.Pages > 0 {
		pdf = fax.PDF(in.Result.Pages)
		_ = os.WriteFile(filepath.Join(dir, r.ID+".pdf"), pdf, 0o600)
		_ = os.WriteFile(filepath.Join(dir, r.ID+".tif"), fax.TIFF(in.Result.Pages), 0o600)
	}
	if box != nil && pdf != nil && cfg.System.Email.Host != "" {
		for _, to := range box.Email {
			if err := s.mailFax(cfg, r, to, pdf); err != nil {
				s.Log.Warn("could not email a fax", "to", to, "err", err)
				continue
			}
			r.Emailed = append(r.Emailed, to)
		}
	}
	j, _ := json.Marshal(r)
	_ = os.WriteFile(filepath.Join(dir, r.ID+".json"), j, 0o600)
	s.Log.Info("fax kept", "id", r.ID, "box", r.Box, "pages", r.Pages, "status", r.Status)
	if s.Bus != nil {
		s.Bus.Publish("fax.received", r)
	}
}

func (s *Server) mailFax(cfg config.Config, r FaxRecord, to string, pdf []byte) error {
	from := r.From
	switch {
	case (from == "" || from == "anonymous") && r.Station != "":
		from = r.Station // no caller ID: the sending machine's own identity
	case from == "" || from == "anonymous":
		from = "an unknown number"
	case r.Station != "" && r.Station != r.From:
		from += " (" + r.Station + ")"
	}
	subject := fmt.Sprintf("Fax from %s, %d pages", from, r.Pages)
	if r.Pages == 1 {
		subject = fmt.Sprintf("Fax from %s, 1 page", from)
	}
	text := fmt.Sprintf("%s sent a fax to %s %s at %s. It is attached as a PDF.\n", from, r.Box, r.BoxName,
		r.At.In(cfg.System.Location()).Format("Mon 2 Jan 15:04"))
	if r.Status == "partial" {
		text += "\nThe fax broke off part way: these are the pages that arrived (" + r.Error + ").\n"
	}
	msg, err := buildMail(cfg.System.Email.From, to, subject, text, "fax-"+r.ID+".pdf", "application/pdf", pdf)
	if err != nil {
		return err
	}
	return sendMail(cfg.System.Email, to, msg)
}

func (s *Server) faxRecords() []FaxRecord {
	files, _ := filepath.Glob(filepath.Join(s.faxDir(), "*.json"))
	var out []FaxRecord
	for _, f := range files {
		b, err := os.ReadFile(f)
		var r FaxRecord
		if err == nil && json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

func (s *Server) faxRoutes() {
	s.faxSendRoutes()
	s.handle("GET /api/faxes", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		items := s.faxRecords()
		if items == nil {
			items = []FaxRecord{}
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	s.handle("GET /api/faxes/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id, kind, _ := strings.Cut(r.PathValue("id"), ".")
		if !faxID.MatchString(id) || (kind != "pdf" && kind != "tif") {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join(s.faxDir(), id+"."+kind))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if kind == "pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `inline; filename="fax-`+id+`.pdf"`)
		} else {
			w.Header().Set("Content-Type", "image/tiff")
			w.Header().Set("Content-Disposition", `attachment; filename="fax-`+id+`.tif"`)
		}
		w.Write(b)
	})
	s.handle("DELETE /api/faxes/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		if !faxID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		for _, ext := range []string{".json", ".pdf", ".tif", ".pages"} {
			_ = os.Remove(filepath.Join(s.faxDir(), id+ext))
		}
		s.audit(r, u.Username, "fax.delete", id)
		w.WriteHeader(http.StatusNoContent)
	})
}
