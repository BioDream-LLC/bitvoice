package pbx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

type memRecorder struct {
	mu    sync.Mutex
	cdrs  []store.CDR
	radio []store.Transmission
}

func (m *memRecorder) AddTransmission(x store.Transmission) error {
	m.mu.Lock()
	m.radio = append(m.radio, x)
	m.mu.Unlock()
	return nil
}

func (m *memRecorder) transmissions() []store.Transmission {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Transmission(nil), m.radio...)
}

func (m *memRecorder) AddCDR(r store.CDR) error {
	m.mu.Lock()
	m.cdrs = append(m.cdrs, r)
	m.mu.Unlock()
	return nil
}

func (m *memRecorder) last(t *testing.T) store.CDR {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.cdrs)
		var r store.CDR
		if n > 0 {
			r = m.cdrs[n-1]
		}
		m.mu.Unlock()
		if n > 0 {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no call record written")
	return store.CDR{}
}

// find waits for a call record that ok accepts, newest first: last() may still be an earlier call's.
func (m *memRecorder) find(t *testing.T, what string, ok func(store.CDR) bool) store.CDR {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for i := len(m.cdrs) - 1; i >= 0; i-- {
			if ok(m.cdrs[i]) {
				r := m.cdrs[i]
				m.mu.Unlock()
				return r
			}
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
	return store.CDR{}
}

// nth waits for the nth call record (1-based).
func (m *memRecorder) nth(t *testing.T, n int) store.CDR {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		if len(m.cdrs) >= n {
			r := m.cdrs[n-1]
			m.mu.Unlock()
			return r
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("call record %d not written", n)
	return store.CDR{}
}

const testSecret = "test-secret-123456"

func testConfig() config.Config {
	c := config.Default()
	c.System.PublicAddress = "127.0.0.1"
	c.System.RTPMin, c.System.RTPMax = 32000, 33000
	for _, n := range []string{"100", "101", "102"} {
		c.Extensions = append(c.Extensions, config.Extension{Number: n, Name: "Ext " + n, Secret: testSecret,
			Enabled: true, Outbound: true, RingSeconds: 5})
	}
	c.RingGroups = []config.RingGroup{{Number: "600", Name: "Sales", Members: []string{"101", "102"}, RingSeconds: 5}}
	c.System.EmergencyNotify = []string{"102"}
	c.Security.TrustedNetworks = nil // tests exercise fail-to-ban from loopback
	c.Security.FailBanThreshold = 3
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

// startEngine starts an engine; setup functions run before it starts (to attach stores).
func startEngine(t *testing.T, cfg config.Config, setup ...func(*Engine)) (*Engine, *memRecorder) {
	t.Helper()
	rec := &memRecorder{}
	e := New(cfg, events.New(), rec, nil)
	for _, f := range setup {
		f(e)
	}
	e.T1, e.T2 = 50*time.Millisecond, 200*time.Millisecond
	if err := e.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return e, rec
}

func registered(t *testing.T, e *Engine, users ...string) []*phone {
	t.Helper()
	var out []*phone
	for _, u := range users {
		p := newPhone(t, e.SIPPort(), u, testSecret)
		if code := p.register(); code != 200 {
			t.Fatalf("register %s: %d", u, code)
		}
		out = append(out, p)
	}
	return out
}

func TestRegisterAndCall(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	if regs := e.Registrations(); len(regs) != 2 {
		t.Fatalf("registrations %+v", regs)
	}

	type result struct {
		d    *dialog
		code int
	}
	done := make(chan result, 1)
	go func() {
		d, code := a.dial("101")
		done <- result{d, code}
	}()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	if calls := e.Calls(); len(calls) != 1 || calls[0].State != "ringing" {
		t.Fatalf("calls while ringing %+v", calls)
	}
	bd := b.answer(ring)
	r := <-done
	if r.code != 200 || r.d == nil {
		t.Fatalf("caller got %d", r.code)
	}
	ad := r.d

	// Media flows both ways through BitVoice.
	ad.sendRTP(3)
	if !b.gotRTP(time.Second) {
		t.Fatal("no media reached the callee")
	}
	bd.sendRTP(3)
	if !a.gotRTP(time.Second) {
		t.Fatal("no media reached the caller")
	}
	if calls := e.Calls(); len(calls) != 1 || calls[0].State != "answered" {
		t.Fatalf("calls after answer %+v", calls)
	}

	// The caller hangs up; the callee gets a BYE.
	if resp := a.send(ad.request("BYE")); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("BYE: %v", resp)
	}
	select {
	case <-b.byes:
	case <-time.After(15 * time.Second):
		t.Fatal("callee never got BYE")
	}
	cdr := rec.last(t)
	if cdr.Disposition != "answered" || cdr.From != "100" || cdr.To != "101" || cdr.Direction != "internal" {
		t.Fatalf("CDR %+v", cdr)
	}
	if len(e.Calls()) != 0 {
		t.Fatal("call still listed")
	}
}

func TestEchoTest(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	d, code := a.dial("*43")
	if code != 200 {
		t.Fatalf("echo answered %d", code)
	}
	d.sendRTP(3)
	if !a.gotRTP(time.Second) {
		t.Fatal("echo returned no media")
	}
	a.send(d.request("BYE"))
}

func TestUnreachableAndUnknown(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	if _, code := a.dial("101"); code != 480 {
		t.Fatalf("unregistered extension: %d", code)
	}
	if c := rec.last(t); c.Disposition != "no-answer" {
		t.Fatalf("CDR %+v", c)
	}
	if _, code := a.dial("555"); code != 404 {
		t.Fatalf("unknown number: %d", code)
	}
}

func TestBadPasswordIsBanned(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	p := newPhone(t, e.SIPPort(), "100", "wrong-password-xx")
	for i := 0; i < 3; i++ {
		if code := p.register(); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if len(e.Banned()) != 1 {
		t.Fatalf("not banned: %+v", e.Banned())
	}
	// Banned addresses get no answer at all.
	good := newPhone(t, e.SIPPort(), "100", testSecret)
	if code := good.register(); code != 408 {
		t.Fatalf("banned address got %d", code)
	}
	e.Unban("127.0.0.1")
	if code := good.register(); code != 200 {
		t.Fatalf("after unban: %d", code)
	}
}

// A phone that prefers G.722 still gets G.711 from BitVoice's own services, which speak nothing else.
func TestLocalAnswerUsesG711(t *testing.T) {
	cfg := testConfig()
	e, _ := startEngine(t, cfg)
	a := registered(t, e, "100")[0]
	s := sdp.New("127.0.0.1", a.rtpPort(), 1, []sdp.Codec{{PT: 9, Name: "G722", ClockRate: 8000}, {PT: 0, Name: "PCMU", ClockRate: 8000}}, 101)
	m := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:*43@127.0.0.1"}
	m.Set("From", "<sip:100@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:*43@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 INVITE")
	m.Set("Contact", a.contact())
	m.Set("Content-Type", "application/sdp")
	m.Body = s.Bytes()
	r := a.send(m)
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("%v", r)
	}
	ans, _ := sdp.Parse(r.Body)
	for _, c := range ans.Audio().Codecs() {
		if c.Name == "G722" {
			t.Fatal("answered with G.722")
		}
	}
}

func TestDisplayNameEscaped(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"Front Desk":     `"Front Desk" `,
		`Say "hi"`:       `"Say \"hi\"" `,
		"a\\b":           `"a\\b" `,
		"Evil\r\nVia: x": `"EvilVia: x" `,
	} {
		if got := display(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// A phone that speaks only G.722 still reaches BitVoice's own services: the answer is G.722, and the
// echo comes back as G.722 the phone can decode.
func TestLocalAnswerG722Only(t *testing.T) {
	cfg := testConfig()
	e, _ := startEngine(t, cfg)
	a := registered(t, e, "100")[0]
	s := sdp.New("127.0.0.1", a.rtpPort(), 1, []sdp.Codec{{PT: 9, Name: "G722", ClockRate: 8000}}, 101)
	m := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:*43@127.0.0.1"}
	m.Set("From", "<sip:100@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:*43@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 INVITE")
	m.Set("Contact", a.contact())
	m.Set("Content-Type", "application/sdp")
	m.Body = s.Bytes()
	r := a.send(m)
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("%v", r)
	}
	ans, _ := sdp.Parse(r.Body)
	if cs := ans.Audio().Codecs(); len(cs) == 0 || cs[0].Name != "G722" {
		t.Fatalf("answer codecs %v", cs)
	}
	to := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ans.Audio().Port}
	enc := media.NewG722Encoder()
	pkt := make([]byte, 12, 172)
	pkt[0], pkt[1] = 0x80, 9
	tone := make([]int16, 320)
	for i := range tone {
		tone[i] = int16(5000 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}
	pkt = append(pkt, enc.Encode(tone)...)
	got := false
	for i := 0; i < 20 && !got; i++ {
		binary.BigEndian.PutUint16(pkt[2:], uint16(i))
		_, _ = a.rtp.WriteToUDP(pkt, to)
		_ = a.rtp.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 2048)
		if n, _, err := a.rtp.ReadFromUDP(buf); err == nil && n == 172 && buf[1]&0x7f == 9 {
			got = true
		}
	}
	if !got {
		t.Fatal("no G.722 echo")
	}
}

// A wideband-only phone calls a G.711-only phone: the callee is offered PCMU, the caller is answered in
// G.722, and BitVoice converts the audio both ways.
func TestG722CallsG711(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.codecs = []sdp.Codec{{PT: 9, Name: "G722", ClockRate: 8000}}
	done := make(chan *dialog, 1)
	go func() { d, _ := a.dial("101"); done <- d }()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 never rang")
	}
	offer, _ := sdp.Parse(ring.Req.Body)
	if cs := offer.Audio().Codecs(); len(cs) == 0 || cs[0].Name != "G722" || !slices.ContainsFunc(cs, func(c sdp.Codec) bool { return c.Name == "PCMU" }) {
		t.Fatalf("callee offered %v", cs)
	}
	bd := b.answer(ring)
	ad := <-done
	if ad == nil {
		t.Fatal("caller not answered")
	}
	recvPT := func(p *phone, d *dialog, send func()) int {
		for i := 0; i < 20; i++ {
			send()
			_ = p.rtp.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			buf := make([]byte, 2048)
			if n, _, err := p.rtp.ReadFromUDP(buf); err == nil && n >= 12 && buf[1]&0x7f != 101 {
				return int(buf[1] & 0x7f)
			}
		}
		return -1
	}
	enc := media.NewG722Encoder()
	seq := uint16(0)
	if pt := recvPT(b, bd, func() {
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, 9
		seq++
		binary.BigEndian.PutUint16(pkt[2:], seq)
		_, _ = a.rtp.WriteToUDPAddrPort(append(pkt, enc.Encode(make([]int16, 320))...), ad.media)
	}); pt != 0 {
		t.Fatalf("callee got payload type %d, want PCMU", pt)
	}
	if pt := recvPT(a, ad, func() { bd.sendRTP(1) }); pt != 9 {
		t.Fatalf("caller got payload type %d, want G.722", pt)
	}
}

// A restart keeps where phones are registered: a call after it reaches a phone that has not registered
// again yet (phones register for up to an hour); an expired or deleted extension's binding is dropped.
func TestRegistrationsKeptOverRestart(t *testing.T) {
	var saved []byte
	keep := func(e *Engine) {
		e.SaveRegistrations = func(b []byte) error { saved = append([]byte(nil), b...); return nil }
	}
	a, _ := startEngine(t, testConfig(), keep)
	p100 := registered(t, a, "100")[0]
	a.Stop() // saves on the way out
	if !strings.Contains(string(saved), `"extension":"100"`) {
		t.Fatalf("not saved: %s", saved)
	}
	cfg := testConfig()
	b, _ := startEngine(t, cfg, keep, func(e *Engine) {
		if n := e.LoadRegistrations(saved); n != 1 {
			t.Fatalf("restored %d registrations", n)
		}
	})
	p100.pbx.Addr = netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(b.SIPPort()))
	p101 := registered(t, b, "101")[0]
	answeredCall(t, p101, p100, "100") // rings the phone registered before the restart
	// Expired, or for an extension that no longer exists: dropped.
	var list []map[string]any
	_ = json.Unmarshal(saved, &list)
	list[0]["expires"] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	old, _ := json.Marshal(list)
	c, _ := startEngine(t, cfg)
	if n := c.LoadRegistrations(old); n != 0 {
		t.Fatalf("restored %d expired registrations", n)
	}
	cfg2 := testConfig()
	cfg2.Extensions = slices.DeleteFunc(cfg2.Extensions, func(x config.Extension) bool { return x.Number == "100" })
	d, _ := startEngine(t, cfg2)
	if n := d.LoadRegistrations(saved); n != 0 {
		t.Fatalf("restored %d registrations of a deleted extension", n)
	}
}
