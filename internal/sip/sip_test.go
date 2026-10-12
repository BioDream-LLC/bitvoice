package sip

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sampleInvite = "INVITE sip:bob@biloxi.example.com SIP/2.0\r\n" +
	"v: SIP/2.0/UDP pc33.atlanta.example.com;branch=z9hG4bK776asdhds, SIP/2.0/UDP proxy.example.com;branch=z9hG4bKabc\r\n" +
	"Max-Forwards: 70\r\n" +
	"t: Bob <sip:bob@biloxi.example.com>\r\n" +
	"f: \"Alice, Smith\" <sip:alice@atlanta.example.com>;tag=1928301774\r\n" +
	"i: a84b4c76e66710@pc33.atlanta.example.com\r\n" +
	"CSeq: 314159 INVITE\r\n" +
	"m: <sip:alice@pc33.atlanta.example.com>\r\n" +
	"Subject: a long\r\n  folded subject\r\n" +
	"c: application/sdp\r\n" +
	"l: 4\r\n\r\n" +
	"v=0\r\n"

func TestParseRequest(t *testing.T) {
	m, err := Parse([]byte(sampleInvite))
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsRequest || m.Method != INVITE || m.RequestURI != "sip:bob@biloxi.example.com" {
		t.Fatalf("request line: %+v", m)
	}
	if vias := m.GetAll("Via"); len(vias) != 2 {
		t.Fatalf("want 2 Vias split from one line, got %q", vias)
	}
	if got := m.Get("Subject"); got != "a long folded subject" {
		t.Fatalf("folded header: %q", got)
	}
	if string(m.Body) != "v=0\r" {
		t.Fatalf("body honours Content-Length: %q", m.Body)
	}
	n, method := m.CSeq()
	if n != 314159 || method != INVITE {
		t.Fatalf("CSeq: %d %s", n, method)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	from, err := ParseAddress(m.Get("From"))
	if err != nil || from.Display != "Alice, Smith" || from.Tag() != "1928301774" || from.URI.User != "alice" {
		t.Fatalf("From: %+v %v", from, err)
	}
}

func TestRoundTrip(t *testing.T) {
	m, err := Parse([]byte(sampleInvite))
	if err != nil {
		t.Fatal(err)
	}
	m.Body = []byte("v=0\r\no=- 1 1 IN IP4 10.0.0.1\r\n")
	out := m.Bytes()
	if !strings.Contains(string(out), "Content-Length: 30\r\n") {
		t.Fatalf("Content-Length not recomputed:\n%s", out)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("Call-ID") != m.Get("Call-ID") || string(again.Body) != string(m.Body) {
		t.Fatal("round trip changed the message")
	}
}

func TestParseStream(t *testing.T) {
	two := []byte("\r\n\r\n" + sampleInvite + sampleInvite)
	m, n, err := ParseStream(two)
	if err != nil || m.Method != INVITE {
		t.Fatal(err)
	}
	m2, n2, err := ParseStream(two[n:])
	// The trailing LF of the last body byte count stays unread: that one is a keep-alive for the next read.
	if err != nil || m2.Method != INVITE || n+n2 != len(two)-1 {
		t.Fatalf("second message: %v %d+%d of %d", err, n, n2, len(two))
	}
	if _, _, err := ParseStream([]byte(sampleInvite[:len(sampleInvite)-3])); err != ErrIncomplete {
		t.Fatalf("partial body: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, bad := range []string{
		"", "HELLO\r\n\r\n", "INVITE sip:x SIP/3.0\r\n\r\n", "SIP/2.0 99 Low\r\n\r\n",
		"INVITE sip:x SIP/2.0\r\nNoColon\r\n\r\n", "INVITE sip:x SIP/2.0\r\nContent-Length: 50\r\n\r\nshort",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestURI(t *testing.T) {
	cases := map[string]string{
		"sip:alice@atlanta.com":                      "sip:alice@atlanta.com",
		"sip:alice:secret@atlanta.com;transport=tcp": "sip:alice:secret@atlanta.com;transport=tcp",
		"sips:1212@[2001:db8::1]:5061;lr":            "sips:1212@[2001:db8::1]:5061;lr",
		"sip:+1-212-555-1212;phone=yes@gw.com:5060":  "sip:+1-212-555-1212;phone=yes@gw.com:5060",
		"tel:+12125551212;phone-context=example":     "tel:+12125551212;phone-context=example",
		"sip:10.0.0.1:5070":                          "sip:10.0.0.1:5070",
	}
	for in, want := range cases {
		u, err := ParseURI(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("%s: got %s", in, u)
		}
	}
	u, _ := ParseURI("sips:x@h")
	if u.Transport() != "tls" {
		t.Error("sips defaults to tls")
	}
	if _, err := ParseURI("http://x"); err == nil {
		t.Error("accepted http URI")
	}
}

func TestVia(t *testing.T) {
	v, err := ParseVia("SIP/2.0/UDP 192.0.2.1:5060;branch=z9hG4bK1;rport")
	if err != nil {
		t.Fatal(err)
	}
	if v.Transport != "UDP" || v.Host != "192.0.2.1" || v.Port != 5060 || v.Branch() != "z9hG4bK1" {
		t.Fatalf("%+v", v)
	}
	if _, ok := v.Params.Get("rport"); !ok {
		t.Fatal("rport flag lost")
	}
	if v.String() != "SIP/2.0/UDP 192.0.2.1:5060;branch=z9hG4bK1;rport" {
		t.Fatal(v.String())
	}
}

func TestContactStarAndList(t *testing.T) {
	m, _ := Parse([]byte("REGISTER sip:x SIP/2.0\r\nContact: <sip:a@h;x=1,2>;expires=60, \"B, C\" <sip:b@h>\r\n\r\n"))
	cs := m.GetAll("Contact")
	if len(cs) != 2 {
		t.Fatalf("contacts split wrongly: %q", cs)
	}
	a, _ := ParseAddress(cs[0])
	if e, _ := a.Params.Get("expires"); e != "60" {
		t.Fatalf("expires param: %+v", a)
	}
	star, _ := ParseAddress("*")
	if !star.Star {
		t.Fatal("star contact")
	}
}

// RFC 2617 section 3.5 worked example.
func TestDigestRFC2617(t *testing.T) {
	got, err := DigestResponse("MD5", "Mufasa", "testrealm@host.com", "Circle Of Life", "GET", "/dir/index.html",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093", "auth", "00000001", "0a4f113b")
	if err != nil {
		t.Fatal(err)
	}
	if got != "6629fae49393a05397450978507c4ef1" {
		t.Fatalf("got %s", got)
	}
}

func TestAuthenticator(t *testing.T) {
	a := NewAuthenticator("bitvoice")
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	ch, err := ParseChallenge(a.Challenge(false))
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(u string) (string, bool) { return "pw", u == "100" }
	cr, err := Answer(ch, "100", "pw", REGISTER, "sip:bitvoice")
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := a.Verify(cr.String(), REGISTER, lookup); r != AuthOK {
		t.Fatalf("good credentials: %v", r)
	}
	bad, _ := Answer(ch, "100", "wrong", REGISTER, "sip:bitvoice")
	if r, _ := a.Verify(bad.String(), REGISTER, lookup); r != AuthBad {
		t.Fatalf("wrong password: %v", r)
	}
	nouser, _ := Answer(ch, "999", "pw", REGISTER, "sip:bitvoice")
	if r, _ := a.Verify(nouser.String(), REGISTER, lookup); r != AuthBad {
		t.Fatalf("unknown user: %v", r)
	}
	now = now.Add(10 * time.Minute)
	if r, _ := a.Verify(cr.String(), REGISTER, lookup); r != AuthStale {
		t.Fatalf("expired nonce: %v", r)
	}
	forged := *cr
	forged.Nonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if r, _ := a.Verify(forged.String(), REGISTER, lookup); r != AuthStale {
		t.Fatalf("unknown nonce should be stale so phones retry: %v", r)
	}
	if r, _ := a.Verify("", REGISTER, lookup); r != AuthMissing {
		t.Fatal("missing")
	}
}

func newTestRequest(method string, to Peer) *Message {
	m := &Message{IsRequest: true, Method: method, RequestURI: "sip:test@" + to.Addr.String()}
	m.Set("From", "<sip:a@test>;tag="+NewTag())
	m.Set("To", "<sip:b@test>")
	m.Set("Call-ID", NewCallID())
	m.Set("CSeq", "1 "+method)
	return m
}

func startStack(t *testing.T, h func(*ServerTx, *Incoming)) *Stack {
	t.Helper()
	s := NewStack(h)
	s.T1 = 20 * time.Millisecond
	s.T2 = 80 * time.Millisecond
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

func peerOf(s *Stack, transport string) Peer {
	return Peer{Transport: transport, Addr: netip.MustParseAddrPort("127.0.0.1:" + itoa(s.Port()))}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTransactionsOverUDPAndTCP(t *testing.T) {
	for _, transport := range []string{"UDP", "TCP"} {
		t.Run(transport, func(t *testing.T) {
			acked := make(chan bool, 1)
			server := startStack(t, func(tx *ServerTx, in *Incoming) {
				switch in.Msg.Method {
				case OPTIONS:
					tx.Respond(NewResponse(in.Msg, 200, ""))
				case INVITE:
					tx.Respond(NewResponse(in.Msg, 180, ""))
					r := NewResponse(in.Msg, 486, "")
					r.Set("To", in.Msg.Get("To")+";tag=x")
					tx.Respond(r)
					go func() {
						select {
						case <-tx.acked:
							acked <- true
						case <-time.After(2 * time.Second):
							acked <- false
						}
					}()
				}
			})
			client := startStack(t, nil)
			to := peerOf(server, transport)

			tx := client.Request(newTestRequest(OPTIONS, to), to)
			var last *Message
			for r := range tx.Responses {
				last = r
			}
			if last == nil || last.StatusCode != 200 {
				t.Fatalf("OPTIONS: %v", last)
			}

			inv := client.Request(newTestRequest(INVITE, to), to)
			var codes []int
			for r := range inv.Responses {
				codes = append(codes, r.StatusCode)
			}
			if len(codes) != 2 || codes[0] != 180 || codes[1] != 486 {
				t.Fatalf("INVITE responses %v", codes)
			}
			if !<-acked {
				t.Fatal("non-2xx final was not acknowledged by the transaction layer")
			}
		})
	}
}

func TestTimeoutGives408(t *testing.T) {
	client := startStack(t, nil)
	client.TxExpire = 150 * time.Millisecond
	// Nothing listens here.
	to := Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:9")}
	tx := client.Request(newTestRequest(OPTIONS, to), to)
	var last *Message
	for r := range tx.Responses {
		last = r
	}
	if last == nil || last.StatusCode != 408 {
		t.Fatalf("want synthetic 408, got %v", last)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(sampleInvite))
	f.Add([]byte("SIP/2.0 200 OK\r\nVia: SIP/2.0/UDP h;branch=z9hG4bK1\r\nContent-Length: 0\r\n\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			return
		}
		// Whatever parses must write and parse again.
		if _, err := Parse(m.Bytes()); err != nil {
			t.Fatalf("re-parse of written message failed: %v\n%q", err, m.Bytes())
		}
		for _, h := range m.GetAll("Via") {
			_, _ = ParseVia(h)
		}
		for _, h := range []string{"From", "To", "Contact"} {
			_, _ = ParseAddress(m.Get(h))
		}
		_, _ = ParseURI(m.RequestURI)
	})
}
