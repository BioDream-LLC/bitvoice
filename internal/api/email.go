package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cloud"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// RunEmail sends new voicemail to mailboxes that ask for it. When AI transcripts are on, it waits for the
// transcript so the email carries it.
func (s *Server) RunEmail(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			v, ok := ev.Data.(store.Voicemail)
			if !ok {
				continue
			}
			cfg := s.config() // vault references resolved
			ai := cfg.System.AI.Enabled && cfg.System.AI.Voicemail
			if (ev.Type == "voicemail.new" && !ai) || ev.Type == "voicemail.transcribed" {
				go func() {
					if err := s.mailVoicemail(cfg, v); err != nil {
						s.Log.Warn("voicemail email failed", "box", v.Box, "err", err)
						s.Bus.Publish("email.failed", map[string]string{"box": v.Box, "error": err.Error()})
					}
				}()
			}
		}
	}
}

func (s *Server) mailVoicemail(cfg config.Config, v store.Voicemail) error {
	x := cfg.FindExtension(v.Box)
	if x == nil || !x.Voicemail.Email || x.Email == "" || cfg.System.Email.Host == "" {
		return nil
	}
	wav, err := s.Store.VoicemailAudio(v.Box, v.ID)
	if err != nil {
		return err
	}
	from := v.From
	if v.FromName != "" {
		from = v.FromName + " (" + v.From + ")"
	}
	subject := fmt.Sprintf("Voicemail from %s, %d s", from, v.Seconds)
	var text strings.Builder
	fmt.Fprintf(&text, "%s left a message for %s %s at %s.\n\n", from, x.Number, x.Name, v.At.In(cfg.System.Location()).Format("Mon 2 Jan 15:04"))
	if v.Summary != "" {
		fmt.Fprintf(&text, "In short: %s\n\n", v.Summary)
	}
	if v.Transcript != "" {
		fmt.Fprintf(&text, "Transcript: %s\n\n", v.Transcript)
	}
	text.WriteString("The recording is attached. Dial " + cfg.System.VoicemailNumber + " from your phone to hear your messages.\n")
	msg, err := buildMail(cfg.System.Email.From, x.Email, subject, text.String(), "voicemail-"+v.ID+".wav", "audio/wav", wav)
	if err != nil {
		return err
	}
	return sendMail(cfg.System.Email, x.Email, msg)
}

// buildMail writes a MIME message with a text part and one attachment.
func buildMail(from, to, subject, body, fileName, fileType string, file []byte) ([]byte, error) {
	// Addresses go into headers as they are: a line break would let them add headers of their own.
	for _, h := range []string{from, to, fileName} {
		if strings.ContainsAny(h, "\r\n") {
			return nil, fmt.Errorf("mail: %q cannot go into a mail header", h)
		}
	}
	if strings.Contains(fileName, `"`) {
		return nil, fmt.Errorf("mail: the attachment name %q has a quote in it", fileName)
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	head := textproto.MIMEHeader{}
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%s\r\n\r\n",
		from, to, mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z), w.Boundary())
	head.Set("Content-Type", "text/plain; charset=utf-8")
	head.Set("Content-Transfer-Encoding", "8bit")
	p, err := w.CreatePart(head)
	if err != nil {
		return nil, err
	}
	p.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	if fileName == "" { // a message with no attachment
		return b.Bytes(), w.Close()
	}
	att := textproto.MIMEHeader{}
	att.Set("Content-Type", fileType)
	att.Set("Content-Transfer-Encoding", "base64")
	att.Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	p, err = w.CreatePart(att)
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(file)
	for len(enc) > 76 {
		p.Write([]byte(enc[:76] + "\r\n"))
		enc = enc[76:]
	}
	p.Write([]byte(enc + "\r\n"))
	return b.Bytes(), w.Close()
}

// sendMail delivers over SMTP: implicit TLS on 465, STARTTLS elsewhere (required when signing in).
func sendMail(em config.Email, to string, msg []byte) error {
	addr := net.JoinHostPort(em.Host, strconv.Itoa(em.Port))
	tlsCfg := &tls.Config{ServerName: em.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	d := &net.Dialer{Timeout: 15 * time.Second}
	if em.Port == 465 {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsCfg)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	c, err := smtp.NewClient(conn, em.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if em.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
		} else if em.Username != "" {
			return errors.New("the mail server does not offer STARTTLS, so BitVoice will not send the password")
		}
	}
	if em.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", em.Username, em.Password, em.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(addrOnly(em.From)); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func addrOnly(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		return strings.TrimSuffix(s[i+1:], ">")
	}
	return s
}

func (s *Server) emailRoutes() {
	s.handle("POST /api/email/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Email config.Email `json:"email"`
			To    string       `json:"to"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		if in.Email.Password == "" && in.Email.Username == cfg.System.Email.Username {
			in.Email.Password = cfg.System.Email.Password
		}
		if cloud.IsReference(in.Email.Password) {
			in.Email.Password, _ = s.vault(cfg.System.Vault).Resolve(r.Context(), in.Email.Password)
		}
		if in.Email.Port == 0 {
			in.Email.Port = 587
		}
		if a, err := mail.ParseAddress(in.To); err != nil || strings.ContainsAny(in.To, "\r\n") || a.Address == "" {
			writeError(w, http.StatusBadRequest, "send the test to one address, like you@example.com")
			return
		}
		check := cfg
		check.System.Email = in.Email
		if err := check.Validate(); err != nil {
			var all, mine config.Problems
			if errors.As(err, &all) {
				for _, p := range all {
					if strings.HasPrefix(p.Field, "system.email") {
						mine = append(mine, p)
					}
				}
			}
			if len(mine) > 0 {
				writeErr(w, mine)
				return
			}
		}
		msg, err := buildMail(in.Email.From, in.To, "BitVoice test message", "Email from BitVoice works.\n", "test.txt", "text/plain", []byte("test\n"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := sendMail(in.Email, in.To, msg); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"sent": in.To})
	})
}
