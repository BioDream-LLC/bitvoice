package pbx

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// carrier is a fake SIP provider: it demands digest authentication for REGISTER and INVITE like real
// carriers do, answers calls, and can place calls in to BitVoice.
type carrier struct {
	stack      *sip.Stack
	auth       *sip.Authenticator
	registered atomic.Bool
	answered   chan string // dialled numbers
	invites    chan *sip.Message
}

func newCarrier(t *testing.T) *carrier {
	c := &carrier{auth: sip.NewAuthenticator("carrier.test"), answered: make(chan string, 4), invites: make(chan *sip.Message, 4)}
	c.stack = sip.NewStack(c.handle)
	c.stack.Host = "127.0.0.1"
	c.stack.T1, c.stack.T2 = 50*time.Millisecond, 200*time.Millisecond
	if err := c.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.stack.Serve(ctx)
	t.Cleanup(func() { cancel(); c.stack.Close() })
	return c
}

func (c *carrier) handle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	hdr := "Authorization"
	if m.Method == sip.INVITE {
		hdr = "Proxy-Authorization"
	}
	switch m.Method {
	case sip.REGISTER, sip.INVITE:
		res, _ := c.auth.Verify(m.Get(hdr), m.Method, func(u string) (string, bool) { return "trunkpass", u == "acct" })
		if res != sip.AuthOK {
			code, ch := 401, "WWW-Authenticate"
			if m.Method == sip.INVITE {
				code, ch = 407, "Proxy-Authenticate"
			}
			r := sip.NewResponse(m, code, "")
			r.Set("To", m.Get("To")+";tag=c1")
			r.Set(ch, c.auth.Challenge(res == sip.AuthStale))
			tx.Respond(r)
			return
		}
		if m.Method == sip.INVITE {
			select {
			case c.invites <- m:
			default:
			}
		}
		r := sip.NewResponse(m, 200, "")
		r.Set("To", m.Get("To")+";tag=c1")
		r.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
		if m.Method == sip.REGISTER {
			c.registered.Store(true)
			r.Set("Expires", "120")
		} else {
			r.Set("Content-Type", "application/sdp")
			r.Body = sdp.New("127.0.0.1", 40000, 9, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
			c.answered <- userOf(m.RequestURI)
		}
		tx.Respond(r)
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

func trunkConfig(port int) config.Config {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: port,
		Username: "acct", Password: "trunkpass", Register: true}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "US", Patterns: []string{"NXXNXXXXXX", "911"}, Prepend: "1", Trunks: []string{"t1"}}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100",
		Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return cfg
}

func waitTrunk(t *testing.T, e *Engine, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ts := e.TrunkStatus(); len(ts) == 1 && ts[0].State == state {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("trunk never %s: %+v", state, e.TrunkStatus())
}

func TestTrunkRegistersAndCallsOut(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].Rates = []config.Rate{{Prefix: "1", Name: "North America", PerMinute: 0.02}, {Prefix: "1212", Name: "New York", PerMinute: 0.6}}
	cfg.Extensions[0].Department = "Sales"
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	if !c.registered.Load() {
		t.Fatal("carrier saw no authenticated REGISTER")
	}
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("outbound call: %d", code)
	}
	select {
	case n := <-c.answered:
		if n != "12125551212" {
			t.Fatalf("carrier got %q, want the route's prepended 1", n)
		}
	case <-time.After(time.Second):
		t.Fatal("carrier never answered")
	}
	time.Sleep(1100 * time.Millisecond) // a second of talk, billed as a whole minute
	a.send(d.request(sip.BYE))
	if r := rec.last(t); r.Direction != "outbound" || r.Trunk != "Carrier" || r.Ext != "100" || r.Department != "Sales" || r.Rate != "New York" || r.Cost != 0.6 {
		t.Fatalf("CDR %+v", r)
	}
}

func TestTrunkCallsIn(t *testing.T) {
	c := newCarrier(t)
	e, _ := startEngine(t, trunkConfig(c.stack.Port()))
	e.NameFor = func(n string) string {
		if n == "3125559999" {
			return "Acme Plumbing"
		}
		return ""
	}
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]

	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	tx := c.stack.Request(inv, pbx)
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("DID did not reach extension 101")
	}
	if from := userOf(ring.Req.Get("From")); from != "3125559999" {
		t.Fatalf("caller ID shown as %q", from)
	}
	if !strings.HasPrefix(ring.Req.Get("From"), `"Acme Plumbing"`) {
		t.Fatalf("the directory's name was not shown: %s", ring.Req.Get("From"))
	}
	b.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("carrier got %v", r)
	}
	if calls := e.Calls(); len(calls) != 1 || calls[0].Direction != "inbound" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestTrunkChannelLimit(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].MaxChannels = 1
	e, _ := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("first call: %d", code)
	}
	<-c.answered
	if n := e.trunkChannels(cfg.Trunks[0].ID); n != 1 {
		t.Fatalf("channels in use: %d", n)
	}
	b := registered(t, e, "101")[0]
	if _, code := b.dial("2125551313"); code != 503 {
		t.Fatalf("second call over the limit: %d, want 503", code)
	}
	a.send(d.request(sip.BYE))
}

// Emergency mode: an allowed extension dials the code and every incoming call goes to the chosen place
// (here 102) instead of its route (101).
func TestEmergencyMode(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.System.EmergencyMode = config.EmergencyMode{Code: "*55", Allowed: []string{"100"},
		Destination: config.Destination{Type: "extension", Target: "102"}}
	var mu sync.Mutex
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.SetEmergencyMode = func(on bool, by string) error {
			mu.Lock()
			defer mu.Unlock()
			next := *e.Config()
			next.System.EmergencyMode.On = on
			e.Apply(next)
			return nil
		}
	})
	waitTrunk(t, e, "registered")
	ps := registered(t, e, "100", "101", "102")
	if _, code := ps[1].dial("*55"); code != 403 {
		t.Fatalf("a extension not allowed got %d", code)
	}
	d, code := ps[0].dial("*55")
	if d == nil {
		t.Fatalf("switch refused: %d", code)
	}
	ps[0].waitBye(t, 3*time.Second)
	if !e.Config().System.EmergencyMode.On {
		t.Fatal("not switched on")
	}
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in2")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40004, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	c.stack.Request(inv, pbx)
	if ring := ps[2].waitRing(2 * time.Second); ring == nil {
		t.Fatal("the call did not go to the emergency destination")
	}
	if ps[1].waitRing(200*time.Millisecond) != nil {
		t.Fatal("the normal route still rang")
	}
}

type fakeRemote struct{ addr config.Address }

func (f fakeRemote) RemoteAddress(string) (config.Address, time.Time, bool) {
	return f.addr, time.Now(), f.addr.Street != ""
}

// A 911 call carries the caller's civic address (PIDF-LO): the site's when the phone is on its network,
// the remote worker's confirmed address when not.
func TestEmergencyLocationIsSent(t *testing.T) {
	for _, tc := range []struct {
		name, network, want, method string
		remote                      config.Address
	}{
		{"on site", "127.0.0.0/8", "<ca:RD>Main Street</ca:RD>", "Derived", config.Address{}},
		{"remote", "10.0.0.0/8", "<ca:RD>Elm Road</ca:RD>", "Manual", config.Address{Country: "US", City: "Albany", Street: "Elm Road", Number: "7"}},
	} {
		c := newCarrier(t)
		cfg := trunkConfig(c.stack.Port())
		cfg.System.SendLocation = true
		cfg.Sites = []config.Site{{ID: "hq", Name: "HQ", Country: "US", State: "NY", City: "New York", Street: "Main Street", Number: "12", PostalCode: "10001", Networks: []string{tc.network}}}
		cfg.Extensions[0].Site, cfg.Extensions[0].Floor, cfg.Extensions[0].Room = "hq", "2", "210 & B"
		e, _ := startEngine(t, cfg, func(e *Engine) { e.Remote = fakeRemote{tc.remote} })
		waitTrunk(t, e, "registered")
		a := registered(t, e, "100")[0]
		go a.dial("911")
		var inv *sip.Message
		select {
		case inv = <-c.invites:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no INVITE reached the provider", tc.name)
		}
		body := string(inv.Body)
		if !strings.HasPrefix(inv.Get("Content-Type"), "multipart/mixed") || !strings.HasPrefix(inv.Get("Geolocation"), "<cid:") ||
			!strings.Contains(body, "application/sdp") || !strings.Contains(body, tc.want) || !strings.Contains(body, "<gp:method>"+tc.method) {
			t.Fatalf("%s: %s\n%s", tc.name, inv.Get("Content-Type"), body)
		}
		if tc.name == "on site" && !strings.Contains(body, "<ca:ROOM>210 &amp; B</ca:ROOM>") {
			t.Fatalf("room not escaped: %s", body)
		}
	}
}

// A branded number goes out with its name, and on a trunk that takes rich call data, its logo and
// reason (RFC 9796); the number presented is kept on the call record.
func TestBrandedCallOut(t *testing.T) {
	for _, rcd := range []bool{true, false} {
		c := newCarrier(t)
		cfg := trunkConfig(c.stack.Port())
		cfg.Trunks[0].CallerID, cfg.Trunks[0].RichCallData = "+12125550100", rcd
		cfg.System.Brands = []config.Brand{{Number: "12125550100", Name: "Acme Dental", Logo: "https://acme.example/logo.png", Reason: `Your "appointment"`}}
		e, _ := startEngine(t, cfg)
		waitTrunk(t, e, "registered")
		a := registered(t, e, "100")[0]
		go a.dial("2125550199")
		var inv *sip.Message
		select {
		case inv = <-c.invites:
		case <-time.After(3 * time.Second):
			t.Fatal("no INVITE reached the provider")
		}
		if !strings.HasPrefix(inv.Get("From"), `"Acme Dental" <sip:+12125550100@`) {
			t.Fatalf("From %s", inv.Get("From"))
		}
		ci := inv.Get("Call-Info")
		if rcd && ci != `<https://acme.example/logo.png>;purpose=icon;call-reason="Your 'appointment'"` || !rcd && ci != "" {
			t.Fatalf("rcd %v: Call-Info %q", rcd, ci)
		}
	}
}
