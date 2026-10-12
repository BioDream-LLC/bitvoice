package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
)

// fakeIMAP is a mailbox server holding messages, speaking the little of IMAP BitVoice uses.
type fakeIMAP struct {
	mu   sync.Mutex
	msgs map[uint32][]byte
	seen map[uint32]bool
	user string
	pass string
}

func (f *fakeIMAP) serve(t *testing.T) func(string) (net.Conn, error) {
	return func(string) (net.Conn, error) {
		a, b := net.Pipe()
		go f.session(b)
		return a, nil
	}
}

func (f *fakeIMAP) session(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	fmt.Fprint(c, "* OK fake ready\r\n")
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		tag, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		cmd := strings.ToUpper(strings.Fields(rest)[0])
		f.mu.Lock()
		switch {
		case cmd == "LOGIN":
			if rest == fmt.Sprintf("LOGIN %q %q", f.user, f.pass) {
				authed = true
				fmt.Fprintf(c, "%s OK signed in\r\n", tag)
			} else {
				fmt.Fprintf(c, "%s NO wrong password\r\n", tag)
			}
		case !authed:
			fmt.Fprintf(c, "%s BAD sign in first\r\n", tag)
		case cmd == "SELECT":
			fmt.Fprintf(c, "* %d EXISTS\r\n%s OK [READ-WRITE] selected\r\n", len(f.msgs), tag)
		case strings.HasPrefix(rest, "UID SEARCH UNSEEN"):
			var ids []string
			for id := range f.msgs {
				if !f.seen[id] {
					ids = append(ids, fmt.Sprint(id))
				}
			}
			fmt.Fprintf(c, "* SEARCH %s\r\n%s OK done\r\n", strings.Join(ids, " "), tag)
		case strings.HasPrefix(rest, "UID FETCH"):
			var id uint32
			fmt.Sscanf(rest, "UID FETCH %d", &id)
			m := f.msgs[id]
			fmt.Fprintf(c, "* 1 FETCH (UID %d BODY[] {%d}\r\n%s)\r\n%s OK done\r\n", id, len(m), m, tag)
		case strings.HasPrefix(rest, "UID STORE"):
			var id uint32
			fmt.Sscanf(rest, "UID STORE %d", &id)
			f.seen[id] = true
			fmt.Fprintf(c, "%s OK done\r\n", tag)
		case cmd == "LOGOUT":
			fmt.Fprintf(c, "* BYE\r\n%s OK bye\r\n", tag)
			f.mu.Unlock()
			return
		default:
			fmt.Fprintf(c, "%s BAD what\r\n", tag)
		}
		f.mu.Unlock()
	}
}

func mailWith(from, to, subject, auth, body string) []byte {
	m := "Authentication-Results: mx.example.net; " + auth + "\r\n" +
		"Authentication-Results: mx.example.net; dkim=pass header.d=" + strings.SplitN(from, "@", 2)[1] + "\r\n" +
		"From: Pat <" + from + ">\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=XX\r\n\r\n" +
		"--XX\r\nContent-Type: text/plain\r\n\r\n" + body + "\r\n-- \r\nPat, Accounts\r\n" +
		"--XX\r\nContent-Type: text/plain; name=\"notes.txt\"\r\nContent-Disposition: attachment; filename=\"notes.txt\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"VGhlIG5vdGVzLCBvbiBhIHBhZ2Ugb2YgdGhlaXIgb3duLg==\r\n--XX--\r\n"
	return []byte(m)
}

func TestEmailToFax(t *testing.T) {
	s, _ := newServer(t)
	s.DataDir = t.TempDir()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "fax", rev, func(c *config.Config) error {
		c.Faxes = []config.FaxBox{{ID: "f1", Number: "300", Name: "Office fax", Station: "+12125550199",
			Email: []string{"pat@example.com"}, Senders: []string{"@partner.example.org"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &fakeIMAP{user: "fax@example.com", pass: "mail pass", seen: map[uint32]bool{}, msgs: map[uint32][]byte{
		1: mailWith("pat@example.com", "fax@example.com", "Fax 212 555 0123 please", "spf=pass smtp.mailfrom=pat@example.com", "Invoice attached.\r\n\r\n> old quoted mail"),
		2: mailWith("lee@partner.example.org", "fax+3125550144@example.com", "Contract", "dmarc=pass header.from=partner.example.org", "Signed copy."),
		3: mailWith("eve@elsewhere.example", "fax@example.com", "Fax 2125550123", "spf=pass smtp.mailfrom=eve@elsewhere.example", "Let me in."),
		4: mailWith("pat@example.com", "fax@example.com", "Fax 2125550123", "spf=fail smtp.mailfrom=pat@example.com", "Spoofed."),
	}}
	fm := config.FaxMail{Host: "imap.example.com", Port: 993, Username: f.user, Password: f.pass}
	if err := s.readFaxMail(fm, f.serve(t)); err != nil {
		t.Fatal(err)
	}
	for id := uint32(1); id <= 4; id++ {
		if !f.seen[id] {
			t.Fatalf("message %d left unread", id)
		}
	}
	got := map[string]FaxRecord{}
	for _, r := range s.faxRecords() {
		got[r.To] = r
	}
	if len(got) != 2 {
		t.Fatalf("queued %d faxes, want 2 (one sender not allowed, one not vouched for): %+v", len(got), got)
	}
	a, b := got["2125550123"], got["3125550144"]
	// Pat's: a cover note (the words, without signature or quoted mail) and the attachment's page.
	if a.Box != "300" || a.Pages != 2 || a.ReplyTo != "pat@example.com" || a.Status != "queued" {
		t.Fatalf("pat's fax: %+v", a)
	}
	if b.Box != "300" || b.Pages != 2 || b.By != "lee@partner.example.org" {
		t.Fatalf("lee's fax: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(s.faxDir(), a.ID+".pages")); err != nil {
		t.Fatal("pages not kept:", err)
	}
	// A wrong password says so.
	fm.Password = "nope"
	if err := s.readFaxMail(fm, f.serve(t)); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestVouched(t *testing.T) {
	h := func(ar ...string) mail.Header { return mail.Header{"Authentication-Results": ar} }
	for _, c := range []struct {
		ar     []string
		sender string
		want   bool
	}{
		{[]string{"mx; dmarc=pass header.from=example.com"}, "a@example.com", true},
		{[]string{"mx; dkim=pass header.d=example.com"}, "a@mail.example.com", true},
		{[]string{"mx; dkim=pass header.d=evil.example"}, "a@example.com", false},
		{[]string{"mx; spf=pass smtp.mailfrom=b@example.com"}, "a@example.com", true},
		{[]string{"mx; spf=softfail smtp.mailfrom=a@example.com"}, "a@example.com", false},
		{[]string{"mx; none", "forged; dmarc=pass header.from=example.com"}, "a@example.com", false},
		{nil, "a@example.com", false},
		{[]string{"mx; dkim=pass header.d=notexample.com"}, "a@example.com", false},
	} {
		if got := vouched(h(c.ar...), c.sender); got != c.want {
			t.Errorf("%v from %s: %v, want %v", c.ar, c.sender, got, c.want)
		}
	}
}

func TestFaxNumberIn(t *testing.T) {
	for subject, want := range map[string]string{
		"Fax 212 555 0199":         "2125550199",
		"fax to +1 (212) 555-0199": "+12125550199",
		"Invoice 12":               "",
		"Re: room 300":             "300",
	} {
		if got := faxNumberIn(mail.Header{}, subject); got != want {
			t.Errorf("%q: %q, want %q", subject, got, want)
		}
	}
	if got := faxNumberIn(mail.Header{"To": {"Fax <fax+442071234567@example.com>"}}, "Contract 2"); got != "442071234567" {
		t.Errorf("plus address: %q", got)
	}
}

// A fax sent from the screen: a note and a picture are uploaded, the worker sends them over a trunk
// to another BitVoice whose fax number answers, and the fax is marked sent.
func TestSendFaxFromScreen(t *testing.T) {
	// The receiving server.
	rst, err := storetest.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	rcfg, _ := rst.Config()
	rcfg.System.RTPMin, rcfg.System.RTPMax = 34400, 34600
	rcfg.Faxes = []config.FaxBox{{ID: "r1", Number: "300", Name: "Their fax", Station: "+13125550144"}}
	rcfg.Trunks = []config.Trunk{{ID: "in", Name: "In", Enabled: true, Host: "127.0.0.1", AllowFrom: []string{"127.0.0.1"}}}
	rcfg.InboundRoutes = []config.InboundRoute{{Name: "Fax", Destination: config.Destination{Type: "fax", Target: "300"}}}
	if err := rcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := make(chan pbx.FaxIn, 1)
	rx := pbx.New(rcfg, events.New(), rst, nil)
	rx.OnFax = func(in pbx.FaxIn) { got <- in }
	if err := rx.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rx.Stop)

	s, ts := newServer(t)
	s.DataDir = t.TempDir()
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "fax", rev, func(c *config.Config) error {
		c.Faxes = []config.FaxBox{{ID: "f1", Number: "310", Name: "Sales fax", Station: "+12125550110"}}
		c.Trunks = []config.Trunk{{ID: "out", Name: "Line", Enabled: true, Host: "127.0.0.1", Port: rx.SIPPort()}}
		c.OutboundRoutes = []config.OutboundRoute{{Name: "US", Patterns: []string{"NXXNXXXXXX"}, Prepend: "+1", Trunks: []string{"out"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunFaxes(ctx)

	// A small picture: a black square on white.
	img := image.NewGray(image.Rect(0, 0, 200, 200))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 50; y < 150; y++ {
		for x := 50; x < 150; x++ {
			img.Pix[y*200+x] = 0
		}
	}
	var pic bytes.Buffer
	png.Encode(&pic, img)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("box", "310")
	mw.WriteField("to", "(312) 555-0144")
	mw.WriteField("cover", "To: Lee\nThe drawing, as promised.")
	fw, _ := mw.CreateFormFile("file", "square.png")
	fw.Write(pic.Bytes())
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/faxes/send", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-BitVoice", "1")
	resp, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var rec FaxRecord
	json.NewDecoder(resp.Body).Decode(&rec)
	resp.Body.Close()
	if resp.StatusCode != 201 || rec.Pages != 2 || rec.To != "3125550144" {
		t.Fatalf("send: %d %+v", resp.StatusCode, rec)
	}
	select {
	case in := <-got:
		if in.Result.Err != nil || len(in.Result.Pages) != 2 {
			t.Fatalf("received %d pages: %v", len(in.Result.Pages), in.Result.Err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("nothing arrived")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var now FaxRecord
		for _, r := range s.faxRecords() {
			if r.ID == rec.ID {
				now = r
			}
		}
		if now.Status == "sent" && now.Sent == 2 && now.RemoteID == "+13125550144" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record: %+v", now)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Refused before anything is queued: no number, a file that cannot be faxed.
	for _, c := range []struct{ to, name, want string }{{"", "a.txt", "enter the fax number"}, {"2125550100", "a.exe", "cannot be faxed"}} {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		w.WriteField("box", "310")
		w.WriteField("to", c.to)
		f, _ := w.CreateFormFile("file", c.name)
		f.Write([]byte("hello"))
		w.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/api/faxes/send", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("X-BitVoice", "1")
		resp, _ := admin.http.Do(req)
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != 400 || !strings.Contains(fmt.Sprint(out["message"]), c.want) {
			t.Fatalf("%+v: %d %v", c, resp.StatusCode, out)
		}
	}
}
