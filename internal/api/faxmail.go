package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Email to fax: BitVoice reads a mailbox; each new message names a fax number (in its subject, or
// after a + in the address it was sent to) and is faxed from the fax number its sender belongs to,
// its attachments as the pages and its text as a cover note. The sender hears by email how it went.

// RunFaxMail reads the email to fax mailbox every minute while one is set.
func (s *Server) RunFaxMail(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if fm := s.config().System.FaxMail; fm.Host != "" {
			if err := s.readFaxMail(fm, nil); err != nil {
				s.Log.Warn("email to fax: could not read the mailbox", "err", err)
				s.setFaxMailStatus(err)
			} else {
				s.setFaxMailStatus(nil)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) setFaxMailStatus(err error) {
	s.faxMu.Lock()
	defer s.faxMu.Unlock()
	s.faxMailChecked = time.Now()
	s.faxMailErr = ""
	if err != nil {
		s.faxMailErr = err.Error()
	}
}

// readFaxMail reads every unread message once and marks it read, faxed or not.
func (s *Server) readFaxMail(fm config.FaxMail, dial func(string) (net.Conn, error)) error {
	c, err := dialIMAP(fm.Host, fm.Port, dial)
	if err != nil {
		return err
	}
	defer c.Logout()
	if err := c.Login(fm.Username, fm.Password); err != nil {
		return err
	}
	if err := c.Select(fm.Folder); err != nil {
		return err
	}
	ids, err := c.Unseen()
	if err != nil {
		return err
	}
	for _, uid := range ids {
		raw, err := c.Fetch(uid)
		if err != nil {
			return err
		}
		// Marked read first: a message that cannot be faxed is never tried twice.
		if err := c.Seen(uid); err != nil {
			return err
		}
		s.faxFromMail(fm, raw)
	}
	return nil
}

// mailDoc is a document found in an email.
type mailDoc struct {
	FaxDoc
	text bool // the message's own text
}

func (s *Server) faxFromMail(fm config.FaxMail, raw []byte) {
	cfg := s.config()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		s.Log.Info("email to fax: not an email", "err", err)
		return
	}
	from, err := mail.ParseAddress(m.Header.Get("From"))
	if err != nil {
		s.Log.Info("email to fax: no sender")
		return
	}
	sender := strings.ToLower(from.Address)
	subject := decodeHeader(m.Header.Get("Subject"))
	// A message the sending domain's server did not vouch for may come from anyone: it is dropped
	// without a reply (a reply would go to whoever was impersonated).
	if !fm.AnySender && !vouched(m.Header, sender) {
		s.Log.Warn("email to fax: refused a message the sender's domain did not vouch for", "from", sender)
		return
	}
	box := faxBoxFor(cfg, sender)
	reply := func(text string) {
		if cfg.System.Email.Host == "" {
			return
		}
		msg, err := buildMail(cfg.System.Email.From, sender, "Fax not sent: "+subject, text, "", "", nil)
		if err == nil {
			err = sendMail(cfg.System.Email, sender, msg)
		}
		if err != nil {
			s.Log.Warn("email to fax: could not reply", "to", sender, "err", err)
		}
	}
	if box == nil {
		s.Log.Warn("email to fax: the sender may not send faxes", "from", sender)
		reply("Your address may not send faxes through this phone system. An administrator can allow it on the fax number, in Faxes.\n")
		return
	}
	to := faxNumberIn(m.Header, subject)
	if to == "" {
		reply("Put the fax number to send to in the subject, like: Fax 212 555 0199\n")
		return
	}
	cover, docs := mailParts(m)
	var fd []FaxDoc
	for _, d := range docs {
		fd = append(fd, d.FaxDoc)
	}
	rec, err := s.QueueFax(box.Number, to, cover, subject, sender, fd)
	if err != nil {
		reply("The fax could not be made: " + err.Error() + ".\n")
		return
	}
	rec.ReplyTo = sender
	s.saveFax(rec)
	s.Log.Info("email to fax: queued", "id", rec.ID, "from", sender, "to", rec.To, "pages", rec.Pages)
}

// faxBoxFor is the fax number a sender may send from: the first whose people (Email) or senders
// list them, by address or by domain.
func faxBoxFor(cfg config.Config, sender string) *config.FaxBox {
	at := strings.LastIndex(sender, "@")
	if at < 0 {
		return nil
	}
	domain := sender[at:]
	for i := range cfg.Faxes {
		b := &cfg.Faxes[i]
		for _, e := range b.Email {
			if strings.EqualFold(strings.TrimSpace(e), sender) {
				return b
			}
		}
		for _, e := range b.Senders {
			if e == sender || e == domain {
				return b
			}
		}
	}
	return nil
}

// vouched says whether the receiving mail server's checks (Authentication-Results, RFC 8601)
// passed for the sender's domain: DMARC, or DKIM or SPF for the same domain.
func vouched(h mail.Header, sender string) bool {
	domain := strings.ToLower(sender[strings.LastIndex(sender, "@")+1:])
	same := func(d string) bool {
		d = strings.ToLower(strings.Trim(d, " ;\"<>"))
		if i := strings.LastIndex(d, "@"); i >= 0 {
			d = d[i+1:]
		}
		return d != "" && (domain == d || strings.HasSuffix(domain, "."+d))
	}
	// Only the first header counts: it was added by the server that received the message; ones
	// further down came with the message and could say anything.
	ar := h["Authentication-Results"]
	if len(ar) == 0 {
		return false
	}
	for _, res := range strings.Split(ar[0], ";")[1:] {
		f := strings.Fields(res)
		if len(f) == 0 {
			continue
		}
		method, result, _ := strings.Cut(strings.ToLower(f[0]), "=")
		if result != "pass" {
			continue
		}
		props := map[string]string{}
		for _, p := range f[1:] {
			if k, v, ok := strings.Cut(p, "="); ok {
				props[strings.ToLower(k)] = v
			}
		}
		switch method {
		case "dmarc":
			if d, ok := props["header.from"]; !ok || same(d) {
				return true
			}
		case "dkim":
			if same(props["header.d"]) || same(props["header.i"]) {
				return true
			}
		case "spf":
			if same(props["smtp.mailfrom"]) {
				return true
			}
		}
	}
	return false
}

var plusNumber = regexp.MustCompile(`\+([0-9]{3,20})@`)
var subjectNumber = regexp.MustCompile(`\+?[0-9][0-9 ().\-]{1,24}[0-9]`)

// faxNumberIn finds the number to fax: after a + in an address the message was sent to, or in the
// subject.
func faxNumberIn(h mail.Header, subject string) string {
	for _, k := range []string{"Delivered-To", "X-Original-To", "To", "Cc"} {
		for _, v := range h[k] {
			if m := plusNumber.FindStringSubmatch(v); m != nil {
				return m[1]
			}
		}
	}
	for _, m := range subjectNumber.FindAllString(subject, -1) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' || r == '+' {
				return r
			}
			return -1
		}, m)
		if len(strings.TrimLeft(digits, "+")) >= 3 {
			return digits
		}
	}
	return ""
}

func decodeHeader(v string) string {
	d, err := new(mime.WordDecoder).DecodeHeader(v)
	if err != nil {
		return v
	}
	return d
}

// mailParts is a message's text (for a cover note) and its attachments.
func mailParts(m *mail.Message) (string, []mailDoc) {
	var text string
	var docs []mailDoc
	var walk func(h map[string][]string, body io.Reader, depth int)
	walk = func(h map[string][]string, body io.Reader, depth int) {
		get := func(k string) string {
			if v := h[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		ct, params, err := mime.ParseMediaType(get("Content-Type"))
		if err != nil {
			ct = "text/plain"
		}
		if strings.HasPrefix(ct, "multipart/") && depth < 5 {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextRawPart()
				if err != nil {
					return
				}
				walk(p.Header, p, depth+1)
			}
		}
		data, err := io.ReadAll(io.LimitReader(decodeBody(get("Content-Transfer-Encoding"), body), 30<<20))
		if err != nil {
			return
		}
		disp, dp, _ := mime.ParseMediaType(get("Content-Disposition"))
		name := decodeHeader(dp["filename"])
		if name == "" {
			name = decodeHeader(params["name"])
		}
		if disp != "attachment" && name == "" && ct == "text/plain" {
			if text == "" {
				text = string(data)
			}
			return
		}
		if name == "" || strings.HasPrefix(ct, "text/html") && disp != "attachment" {
			return // the HTML copy of the text, or an inline part with no name (a logo)
		}
		docs = append(docs, mailDoc{FaxDoc: FaxDoc{Name: name, Type: ct, Data: data}})
	}
	walk(m.Header, m.Body, 0)
	return trimQuoted(text), docs
}

func decodeBody(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &stripSpace{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// stripSpace drops the line breaks inside base64.
type stripSpace struct{ r io.Reader }

func (s *stripSpace) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		if c := p[i]; c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			p[j] = c
			j++
		}
	}
	if j == 0 && n > 0 && err == nil {
		return s.Read(p)
	}
	return j, err
}

// trimQuoted keeps a message's own words: a signature ("-- ") and quoted earlier mail are left
// off the cover note.
func trimQuoted(t string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(t, "\r\n", "\n"), "\n") {
		if l == "-- " || strings.HasPrefix(l, "On ") && strings.HasSuffix(strings.TrimSpace(l), "wrote:") {
			break
		}
		if strings.HasPrefix(l, ">") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

var errNoFaxMail = errors.New("email to fax is not set up")

// testFaxMail signs in to a mailbox to check the settings.
func (s *Server) testFaxMail(fm config.FaxMail) error {
	if fm.Host == "" {
		return errNoFaxMail
	}
	if fm.Port == 0 {
		fm.Port = 993
	}
	c, err := dialIMAP(fm.Host, fm.Port, nil)
	if err != nil {
		return err
	}
	defer c.Logout()
	if err := c.Login(fm.Username, fm.Password); err != nil {
		return err
	}
	return c.Select(fm.Folder)
}
