package api

import (
	"net"
	"net/smtp"
	"os"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// TestEmailToFaxLive reads a real IMAP server: BITVOICE_TEST_IMAP=host:imapPort:smtpPort:address:login:password
// (plain connections, as a test mail server such as GreenMail offers them). A message is put in the
// mailbox over SMTP, then read, faxed and marked read.
func TestEmailToFaxLive(t *testing.T) {
	spec := os.Getenv("BITVOICE_TEST_IMAP")
	if spec == "" {
		t.Skip("BITVOICE_TEST_IMAP not set")
	}
	p := strings.SplitN(spec, ":", 6)
	host, imapPort, smtpPort, addr, user, pass := p[0], p[1], p[2], p[3], p[4], p[5]
	s, _ := newServer(t)
	s.DataDir = t.TempDir()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "fax", rev, func(c *config.Config) error {
		c.Faxes = []config.FaxBox{{ID: "f1", Number: "300", Name: "Office fax", Senders: []string{"@example.org"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	msg := mailWith("lee@example.org", addr, "Fax 312 555 0144", "dkim=pass header.d=example.org", "Signed copy attached.")
	if err := smtp.SendMail(net.JoinHostPort(host, smtpPort), nil, "lee@example.org", []string{addr}, msg); err != nil {
		t.Fatal(err)
	}
	fm := config.FaxMail{Host: host, Port: atoi(imapPort), Username: user, Password: pass}
	plain := func(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }
	if err := s.readFaxMail(fm, plain); err != nil {
		t.Fatal(err)
	}
	var found *FaxRecord
	for _, r := range s.faxRecords() {
		if r.To == "3125550144" {
			found = &r
		}
	}
	if found == nil || found.Pages != 2 || found.ReplyTo != "lee@example.org" {
		t.Fatalf("queued: %+v", found)
	}
	// Read again: the message is marked read, nothing new is queued.
	if err := s.readFaxMail(fm, plain); err != nil {
		t.Fatal(err)
	}
	if n := len(s.faxRecords()); n != 1 {
		t.Fatalf("%d faxes after reading twice", n)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
