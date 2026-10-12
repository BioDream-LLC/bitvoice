package pbx

import (
	"context"
	"crypto/tls"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/certs"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// fakeTeams stands in for Microsoft's SIP proxy: TLS only, and it insists on a client certificate.
type fakeTeams struct {
	stack   *sip.Stack
	got     chan *sip.Message
	gotCert chan bool
}

func newFakeTeams(t *testing.T) *fakeTeams {
	f := &fakeTeams{got: make(chan *sip.Message, 16), gotCert: make(chan bool, 16)}
	f.stack = sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		if tx == nil {
			return
		}
		f.got <- in.Msg
		switch in.Msg.Method {
		case sip.OPTIONS:
			tx.Respond(sip.NewResponse(in.Msg, 200, ""))
		case sip.INVITE:
			tx.Respond(sip.NewResponse(in.Msg, 486, ""))
		}
	})
	f.stack.T1, f.stack.T2 = 50*time.Millisecond, 200*time.Millisecond
	cert, err := certs.Load(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAnyClientCert,
		VerifyConnection: func(cs tls.ConnectionState) error { f.gotCert <- len(cs.PeerCertificates) > 0; return nil }}
	if err := f.stack.ListenTLS("127.0.0.1:0", cfg); err != nil {
		t.Fatal(err)
	}
	f.stack.TLSClient = &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{cert}}
	ctx, cancel := context.WithCancel(context.Background())
	f.stack.Serve(ctx)
	t.Cleanup(func() { cancel(); f.stack.Close() })
	return f
}

func (f *fakeTeams) next(t *testing.T, method string) *sip.Message {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-f.got:
			if m.Method == method {
				return m
			}
		case <-deadline:
			t.Fatalf("Teams got no %s", method)
		}
	}
}

// BitVoice as a Teams Direct Routing SBC: OPTIONS and calls out carry the SBC's name in Contact and
// From, the X-MS-SBC header and user=phone; the client certificate is presented; a Teams user can dial
// an extension; and calls out try Microsoft's three proxies in order.
func TestTeamsDirectRouting(t *testing.T) {
	ms := newFakeTeams(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "teams", Name: "Teams", Enabled: true, Teams: true, SBCName: "sbc.example.com",
		Host: "127.0.0.1", Port: ms.stack.TLSPort(), Transport: "tls", SecureMedia: true, AllowFrom: []string{"127.0.0.1"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Teams users", Patterns: []string{"+1212555XXXX"}, Trunks: []string{"teams"}}}
	cfg.FindExtension("100").Outbound = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	own, err := certs.Load(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.TLS = &tls.Config{Certificates: []tls.Certificate{own}}
		e.TLSClient = &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{own}}
	})
	opt := ms.next(t, sip.OPTIONS)
	if !<-ms.gotCert {
		t.Fatal("BitVoice presented no client certificate")
	}
	wantContact := "@sbc.example.com:" + strconv.Itoa(e.stack.TLSPort()) + ";transport=tls>"
	if !strings.Contains(opt.Get("Contact"), wantContact) || opt.Get("X-MS-SBC") == "" || !strings.Contains(opt.Get("From"), "@sbc.example.com>") {
		t.Fatalf("OPTIONS Contact %q From %q X-MS-SBC %q", opt.Get("Contact"), opt.Get("From"), opt.Get("X-MS-SBC"))
	}
	waitTrunk(t, e, "reachable")

	// A call out to a Teams user.
	ps := registered(t, e, "100", "101")
	go ps[0].dial("+12125550142")
	inv := ms.next(t, sip.INVITE)
	if !strings.Contains(inv.RequestURI, "+12125550142@") || !strings.Contains(inv.RequestURI, ";user=phone") ||
		!strings.Contains(inv.Get("From"), "@sbc.example.com>") || !strings.Contains(inv.Get("Contact"), wantContact) ||
		inv.Get("X-MS-SBC") == "" || !strings.Contains(string(inv.Body), "a=crypto:") {
		t.Fatalf("INVITE %s\nFrom %s\nContact %s\n%s", inv.RequestURI, inv.Get("From"), inv.Get("Contact"), inv.Body)
	}

	// A Teams user dials extension 101.
	in := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:101@sbc.example.com:" + strconv.Itoa(e.stack.TLSPort()) + ";transport=tls"}
	in.Set("From", "<sip:+12125550142@sip.pstnhub.microsoft.com>;tag=ms1")
	in.Set("To", "<sip:101@sbc.example.com>")
	in.Set("Call-ID", "teams-in-1")
	in.Set("CSeq", "1 INVITE")
	in.Set("Contact", "<sip:sip.pstnhub.microsoft.com:5061;transport=tls>")
	in.Set("Content-Type", "application/sdp")
	in.Body = []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/SAVP 0\r\na=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkw\r\na=rtpmap:0 PCMU/8000\r\n")
	tx := ms.stack.Request(in, sip.Peer{Transport: "TLS", Addr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(e.stack.TLSPort()))})
	codes := make(chan string, 8)
	go func() {
		for r := range tx.Responses {
			codes <- strconv.Itoa(r.StatusCode) + " " + r.Get("Contact")
		}
		close(codes)
	}()
	if ps[1].waitRing(3*time.Second) == nil {
		var got []string
		for c := range codes {
			got = append(got, c)
		}
		t.Fatalf("the Teams user's call did not ring extension 101: %v", got)
	}

	// Calls out through Microsoft's own proxy name try all three, in order.
	cfg2 := *e.Config()
	cfg2.Trunks = slices.Clone(cfg2.Trunks)
	cfg2.Trunks[0].Host = "sip.pstnhub.microsoft.com"
	p := e.outbound(&cfg2, cfg2.FindExtension("100"), "+12125550142")
	if len(p.stages) != 3 || p.stages[1].targets[0].trunk.Host != "sip2.pstnhub.microsoft.com" || p.stages[2].targets[0].trunk.Host != "sip3.pstnhub.microsoft.com" {
		t.Fatalf("stages %+v", p.stages)
	}

	// What Microsoft requires is checked when saving.
	bad := testConfig()
	bad.Trunks = []config.Trunk{{ID: "x", Name: "T", Enabled: true, Teams: true, SBCName: "contoso.onmicrosoft.com", Transport: "udp"}}
	err = bad.Validate()
	for _, f := range []string{"trunks[0].transport", "trunks[0].secureMedia", "trunks[0].sbcName"} {
		if err == nil || !strings.Contains(err.Error(), f) {
			t.Errorf("validation missing %s: %v", f, err)
		}
	}
}
