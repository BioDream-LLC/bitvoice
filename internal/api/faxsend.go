package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/fax"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Faxes going out: each is kept with the faxes received (faxes/ID.json, ID.pdf, and ID.pages, the
// page pictures to send), and a worker sends them one at a time per fax number, trying a busy or
// unanswered number again later.

const (
	faxTries     = 3
	faxRetryWait = 5 * time.Minute
)

// faxOutbox holds what the worker needs that the files do not.
type faxOutbox struct {
	mu      sync.Mutex
	wake    chan struct{}
	sending map[string]bool // fax ids being sent now
}

func (s *Server) outbox() *faxOutbox {
	s.faxOnce.Do(func() { s.faxOut = &faxOutbox{wake: make(chan struct{}, 1), sending: map[string]bool{}} })
	return s.faxOut
}

// faxPaper is the paper size faxes are drawn on: the setting, or by country (letter in the
// Americas that use it, A4 elsewhere).
func faxPaper(cfg config.Config) string {
	if p := cfg.System.FaxPaper; p != "" {
		return p
	}
	switch strings.ToUpper(cfg.System.Country) {
	case "US", "CA", "MX", "PH", "CL", "CO", "VE", "GT", "CR", "PA", "DO", "PR", "SV", "NI", "HN", "BO":
		return "letter"
	}
	return "a4"
}

// FaxDoc is one document to fax: its name (for its kind) and content.
type FaxDoc struct {
	Name, Type string
	Data       []byte
}

// QueueFax makes the pages of a fax (a cover note first, when there is one), keeps them and queues
// the fax. by is who asked (a user, or an email address).
func (s *Server) QueueFax(box, to, cover, subject, by string, docs []FaxDoc) (FaxRecord, error) {
	cfg := s.config()
	b := cfg.FindFax(box)
	if b == nil {
		return FaxRecord{}, errors.New("choose a fax number to send from")
	}
	number := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' || r == '*' || r == '#' {
			return r
		}
		return -1
	}, to)
	if len(strings.TrimLeft(number, "+")) < 3 {
		return FaxRecord{}, errors.New("enter the fax number to send to")
	}
	paper := faxPaper(cfg)
	var pages []fax.Bitmap
	if strings.TrimSpace(cover) != "" {
		pages = append(pages, fax.TextPages(cover, paper)...)
	}
	h := fax.FindHelpers()
	for _, d := range docs {
		p, err := h.Convert(d.Name, d.Type, d.Data, paper)
		if err != nil {
			return FaxRecord{}, err
		}
		pages = append(pages, p...)
	}
	if len(pages) == 0 {
		return FaxRecord{}, errors.New("add a document or write a note to fax")
	}
	if len(pages) > fax.MaxPages {
		return FaxRecord{}, fmt.Errorf("a fax can have at most %d pages; this one has %d", fax.MaxPages, len(pages))
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	r := FaxRecord{ID: hex.EncodeToString(id), Direction: "out", Box: b.Number, BoxName: b.Name, To: number,
		Station: b.Station, At: time.Now(), Pages: len(pages), Status: "queued", By: by, Subject: subject}
	// The header line every page carries, as fax machines print it.
	when := r.At.In(cfg.System.Location()).Format("02-Jan-2006 15:04")
	from := b.Station
	if from == "" {
		from = b.Number
	}
	var coded []fax.Page
	for i := range pages {
		pages[i].Stamp(when, b.Name, from, i+1, len(pages))
		coded = append(coded, fax.Page{Data: fax.EncodeMH(pages[i], 0), Params: fax.Params{Fine: pages[i].Fine, Width: pages[i].Width}})
	}
	dir := s.faxDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return FaxRecord{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, r.ID+".pdf"), fax.PDF(coded), 0o600); err != nil {
		return FaxRecord{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, r.ID+".pages"), encodeBitmaps(pages), 0o600); err != nil {
		return FaxRecord{}, err
	}
	s.saveFax(r)
	s.publishFax("fax.queued", r)
	select {
	case s.outbox().wake <- struct{}{}:
	default:
	}
	return r, nil
}

func (s *Server) saveFax(r FaxRecord) {
	j, _ := json.Marshal(r)
	_ = os.WriteFile(filepath.Join(s.faxDir(), r.ID+".json"), j, 0o600)
}

func (s *Server) publishFax(kind string, r FaxRecord) {
	if s.Bus != nil {
		s.Bus.Publish(kind, r)
	}
}

// encodeBitmaps keeps page pictures compactly: each page as its width, its row count and its
// pixels packed eight to a byte.
func encodeBitmaps(pages []fax.Bitmap) []byte {
	var out []byte
	for _, p := range pages {
		fine := byte(0)
		if p.Fine {
			fine = 1
		}
		out = append(out, byte(p.Width>>8), byte(p.Width), byte(len(p.Rows)>>8), byte(len(p.Rows)), fine)
		for _, row := range p.Rows {
			packed := make([]byte, (p.Width+7)/8)
			for x := 0; x < p.Width && x < len(row); x++ {
				if row[x] {
					packed[x/8] |= 0x80 >> uint(x%8)
				}
			}
			out = append(out, packed...)
		}
	}
	return out
}

func decodeBitmaps(b []byte) ([]fax.Bitmap, error) {
	var pages []fax.Bitmap
	for len(b) > 0 {
		if len(b) < 5 {
			return nil, errors.New("damaged pages")
		}
		w, h, fine := int(b[0])<<8|int(b[1]), int(b[2])<<8|int(b[3]), b[4] == 1
		b = b[5:]
		stride := (w + 7) / 8
		if w == 0 || len(b) < stride*h {
			return nil, errors.New("damaged pages")
		}
		p := fax.Bitmap{Width: w, Fine: fine, Rows: make([][]bool, h)}
		for y := 0; y < h; y++ {
			row := make([]bool, w)
			for x := 0; x < w; x++ {
				row[x] = b[y*stride+x/8]&(0x80>>uint(x%8)) != 0
			}
			p.Rows[y] = row
		}
		b = b[stride*h:]
		pages = append(pages, p)
	}
	return pages, nil
}

// RunFaxes sends queued faxes, one at a time per fax number, and tries again later when the
// number was busy or did not answer.
func (s *Server) RunFaxes(ctx context.Context) {
	ob := s.outbox()
	// A fax caught sending when the server stopped goes back in the queue.
	for _, r := range s.faxRecords() {
		if r.Direction == "out" && r.Status == "sending" {
			r.Status = "queued"
			s.saveFax(r)
		}
	}
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		s.sendDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ob.wake:
		}
	}
}

func (s *Server) sendDue(ctx context.Context) {
	ob := s.outbox()
	busy := map[string]bool{}
	ob.mu.Lock()
	for id := range ob.sending {
		busy[id] = true
	}
	ob.mu.Unlock()
	boxBusy := map[string]bool{}
	recs := s.faxRecords()
	for _, r := range recs {
		if busy[r.ID] {
			boxBusy[r.Box] = true
		}
	}
	// Oldest first.
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Direction != "out" || r.Status != "queued" || boxBusy[r.Box] || time.Now().Before(r.NextTry) {
			continue
		}
		if s.Engine == nil || ctx.Err() != nil {
			return
		}
		boxBusy[r.Box] = true
		ob.mu.Lock()
		ob.sending[r.ID] = true
		ob.mu.Unlock()
		go func(r FaxRecord) {
			defer func() {
				ob.mu.Lock()
				delete(ob.sending, r.ID)
				ob.mu.Unlock()
				select {
				case ob.wake <- struct{}{}:
				default:
				}
			}()
			s.sendOne(r)
		}(r)
	}
}

func (s *Server) sendOne(r FaxRecord) {
	b, err := os.ReadFile(filepath.Join(s.faxDir(), r.ID+".pages"))
	var pages []fax.Bitmap
	if err == nil {
		pages, err = decodeBitmaps(b)
	}
	if err != nil {
		r.Status, r.Error = "failed", "the pages to send were lost: "+err.Error()
		s.saveFax(r)
		s.faxDone(r)
		return
	}
	r.Status, r.Tries, r.Error = "sending", r.Tries+1, ""
	s.saveFax(r)
	s.publishFax("fax.sending", r)
	s.Log.Info("sending a fax", "id", r.ID, "from", r.Box, "to", r.To, "pages", len(pages), "try", r.Tries)
	res, err := s.Engine.SendFax(r.Box, r.To, pages)
	r.Sent, r.Rate, r.RemoteID = res.Pages, res.Rate, res.RemoteID
	switch {
	case err == nil:
		r.Status, r.SentAt = "sent", time.Now()
		_ = os.Remove(filepath.Join(s.faxDir(), r.ID+".pages"))
	case r.Tries < faxTries && retryable(err, res):
		cfg := s.config()
		r.Status, r.NextTry = "queued", time.Now().Add(faxRetryWait)
		r.Error = err.Error() + "; trying again at " + r.NextTry.In(cfg.System.Location()).Format("15:04")
	default:
		r.Status, r.Error = "failed", err.Error()
		if res.Pages > 0 {
			r.Status = "partial"
		}
	}
	s.saveFax(r)
	s.Log.Info("fax "+r.Status, "id", r.ID, "to", r.To, "pages", r.Sent, "err", r.Error)
	if r.Status != "queued" {
		s.faxDone(r)
	} else {
		s.publishFax("fax.queued", r)
	}
}

// retryable says whether a failed fax is worth trying again: the number was busy, did not answer
// or nothing answered as a fax machine, and no page got through yet.
func retryable(err error, res fax.SendResult) bool {
	if res.Pages > 0 {
		return false
	}
	if errors.Is(err, pbx.ErrFaxNoAnswer) {
		return true
	}
	m := err.Error()
	for _, w := range []string{"no fax machine answered", "stopped answering", "call ended", "hung up", "too poor"} {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

// faxDone tells everyone a fax has finished: the screens, and by email whoever sent it by email.
func (s *Server) faxDone(r FaxRecord) {
	s.publishFax("fax.sent", r)
	if r.ReplyTo == "" {
		return
	}
	cfg := s.config()
	if cfg.System.Email.Host == "" {
		return
	}
	var subject, text string
	if r.Status == "sent" {
		subject = fmt.Sprintf("Fax to %s sent, %d pages", r.To, r.Sent)
		text = fmt.Sprintf("Your fax to %s went through: all %d pages, at %d bit/s.\n", r.To, r.Sent, r.Rate)
	} else {
		subject = "Fax to " + r.To + " not sent"
		text = fmt.Sprintf("Your fax to %s could not be sent: %s.\n", r.To, r.Error)
		if r.Sent > 0 {
			text += fmt.Sprintf("The first %d of %d pages got through.\n", r.Sent, r.Pages)
		}
	}
	if r.Subject != "" {
		text += "\nYour message: " + r.Subject + "\n"
	}
	pdf, _ := os.ReadFile(filepath.Join(s.faxDir(), r.ID+".pdf"))
	msg, err := buildMail(cfg.System.Email.From, r.ReplyTo, subject, text, "fax-"+r.ID+".pdf", "application/pdf", pdf)
	if err == nil {
		err = sendMail(cfg.System.Email, r.ReplyTo, msg)
	}
	if err != nil {
		s.Log.Warn("could not send the fax report", "to", r.ReplyTo, "err", err)
	}
}

// faxSendRoutes: send a fax from the screen, and try a failed one again.
func (s *Server) faxSendRoutes() {
	s.handle("GET /api/faxes/kinds", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.faxMu.Lock()
		mail := map[string]any{"checked": s.faxMailChecked, "error": s.faxMailErr}
		s.faxMu.Unlock()
		writeJSON(w, 200, map[string]any{"kinds": fax.FindHelpers().Kinds(), "paper": faxPaper(s.config()), "mail": mail})
	})
	s.handle("POST /api/faxes/mail/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in config.FaxMail
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cur := s.config().System.FaxMail
		if in.Password == "" && in.Username == cur.Username {
			in.Password = cur.Password
		}
		if err := s.testFaxMail(in); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"result": "signed in and opened the folder"})
	})
	s.handle("POST /api/faxes/send", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		r.Body = http.MaxBytesReader(w, r.Body, 40<<20)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeError(w, 400, "the documents are too large together (40 MB at most)")
			return
		}
		defer r.MultipartForm.RemoveAll()
		var docs []FaxDoc
		for _, fh := range r.MultipartForm.File["file"] {
			f, err := fh.Open()
			if err != nil {
				writeError(w, 400, "could not read "+fh.Filename)
				return
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				writeError(w, 400, "could not read "+fh.Filename)
				return
			}
			docs = append(docs, FaxDoc{Name: fh.Filename, Type: fh.Header.Get("Content-Type"), Data: data})
		}
		rec, err := s.QueueFax(r.FormValue("box"), r.FormValue("to"), r.FormValue("cover"), "", u.Username, docs)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		s.audit(r, u.Username, "fax.send", rec.ID+" to "+rec.To)
		writeJSON(w, 201, rec)
	})
	s.handle("POST /api/faxes/{id}/retry", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		if !faxID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		for _, rec := range s.faxRecords() {
			if rec.ID != id {
				continue
			}
			if rec.Direction != "out" || (rec.Status != "failed" && rec.Status != "partial") {
				writeError(w, 409, "only a fax that failed can be sent again")
				return
			}
			if _, err := os.Stat(filepath.Join(s.faxDir(), id+".pages")); err != nil {
				writeError(w, 409, "the pages of this fax are no longer kept; send it again")
				return
			}
			rec.Status, rec.Tries, rec.NextTry, rec.Error = "queued", 0, time.Time{}, ""
			s.saveFax(rec)
			s.publishFax("fax.queued", rec)
			select {
			case s.outbox().wake <- struct{}{}:
			default:
			}
			writeJSON(w, 200, rec)
			return
		}
		http.NotFound(w, r)
	})
}
