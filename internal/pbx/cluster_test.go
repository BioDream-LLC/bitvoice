package pbx

import (
	"crypto/tls"
	"errors"
	"github.com/biodream-llc/bitvoice/internal/certs"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// fakeCluster joins engines in one process: each sees the others' phones as the database would show.
type fakeCluster struct {
	mu      sync.Mutex
	engines map[string]*Engine
	owner   string // which engine holds trunks shared one at a time
	forged  bool   // servers publish a certificate that is not theirs
}

type fakeLink struct {
	f  *fakeCluster
	id string
}

func (f *fakeCluster) all() map[string]*Engine {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*Engine{}
	for k, v := range f.engines {
		out[k] = v
	}
	return out
}

func (f *fakeCluster) set(id string, e *Engine) {
	f.mu.Lock()
	f.engines[id] = e
	f.mu.Unlock()
}

func (l fakeLink) Leader() bool { return true }

func (l fakeLink) Self() cluster.Node {
	e := l.f.all()[l.id]
	return cluster.Node{ID: l.id, SIPAddr: "127.0.0.1:" + strconv.Itoa(e.SIPPort()), Self: true, Alive: true}
}

func (l fakeLink) Remote() []cluster.Reg {
	var out []cluster.Reg
	for id, e := range l.f.all() {
		if id == l.id {
			continue
		}
		for _, r := range e.ClusterState().Regs {
			r.Node = id
			out = append(out, r)
		}
	}
	return out
}

func (l fakeLink) Peer(id string) (cluster.Node, bool) {
	e, ok := l.f.all()[id]
	if !ok || id == l.id {
		return cluster.Node{}, false
	}
	n := cluster.Node{ID: id, SIPAddr: "127.0.0.1:" + strconv.Itoa(e.SIPPort()), Alive: true}
	if e.CertPin != nil {
		n.Info.Cert, n.Info.TLSPort = e.CertPin(), e.TLSPort()
		l.f.mu.Lock()
		if l.f.forged {
			n.Info.Cert = strings.Repeat("00", 32)
		}
		l.f.mu.Unlock()
	}
	return n, true
}

func (l fakeLink) RemoteBusy(ext string) bool {
	for id, e := range l.f.all() {
		if id != l.id && strings.Contains(","+strings.Join(e.ClusterState().Info.Busy, ",")+",", ","+ext+",") {
			return true
		}
	}
	return false
}

func (l fakeLink) remoteList(ext string, get func(cluster.Info) []string) bool {
	for id, e := range l.f.all() {
		if id != l.id {
			for _, x := range get(e.ClusterState().Info) {
				if x == ext {
					return true
				}
			}
		}
	}
	return false
}

func (l fakeLink) RemoteRinging(ext string) bool {
	return l.remoteList(ext, func(i cluster.Info) []string { return i.Ringing })
}

func (l fakeLink) ParkedAt(slot string) string {
	for id, e := range l.f.all() {
		if id != l.id && slices.Contains(e.ClusterState().Info.Parked, slot) {
			return id
		}
	}
	return ""
}

func (l fakeLink) TrunkCalls(trunk string) int {
	n := 0
	for id, e := range l.f.all() {
		if id != l.id {
			n += e.ClusterState().Info.Trunks[trunk]
		}
	}
	return n
}

func (l fakeLink) Poke() {}

func (l fakeLink) TrunkOwner(trunk string) (string, bool) {
	l.f.mu.Lock()
	defer l.f.mu.Unlock()
	return l.f.owner, l.f.owner == l.id
}

func (l fakeLink) NodeName(id string) string { return "Server " + strings.ToUpper(id) }

func (l fakeLink) Sign(callID, ext string) string { return l.id + ";" + callID + ";" + ext }

func (l fakeLink) Verify(token, callID, ext string) (cluster.Node, error) {
	p := strings.Split(token, ";")
	if len(p) != 3 || p[1] != callID || p[2] != ext {
		return cluster.Node{}, errors.New("bad token")
	}
	n, ok := l.Peer(p[0])
	if !ok {
		return cluster.Node{}, errors.New("unknown server")
	}
	return n, nil
}

// A phone on one server calls a phone registered with another: it rings there, the call connects
// with sound both ways, the call record is written once (by the caller's server), and the callee
// counts as busy on both. A forged call between servers is refused.
func TestCallBetweenServers(t *testing.T) {
	f := &fakeCluster{engines: map[string]*Engine{}}
	a, recA := startEngine(t, testConfig(), func(e *Engine) { e.Cluster = fakeLink{f, "a"} })
	b, recB := startEngine(t, testConfig(), func(e *Engine) { e.Cluster = fakeLink{f, "b"} })
	f.set("a", a)
	f.set("b", b)

	p100 := registered(t, a, "100")[0]
	p101 := registered(t, b, "101")[0]
	if got := a.reg.contacts("101"); len(got) != 1 || got[0].node != "b" {
		t.Fatalf("server a does not know 101 is on b: %+v", got)
	}
	done := make(chan *dialog, 1)
	go func() { d, _ := p100.dial("101"); done <- d }()
	tx := p101.waitRing(3 * time.Second)
	if tx == nil {
		t.Fatal("101 on server b did not ring")
	}
	callee := p101.answer(tx)
	d := <-done
	if d == nil {
		t.Fatal("the call was not answered")
	}
	d.sendRTP(20)
	callee.sendRTP(20)
	if !p101.gotRTP(2*time.Second) || !p100.gotRTP(2*time.Second) {
		t.Fatal("no sound across the servers")
	}
	if busy, _ := a.busyExtensions(); !busy["101"] {
		t.Fatal("server a does not see 101 busy")
	}
	// 100's call is on server a: server b, which has no phone for 100, still counts it busy.
	if busy, _ := b.busyExtensions(); !busy["100"] {
		t.Fatal("server b does not see 100 busy on server a")
	}
	if n := len(b.Calls()); n != 1 || b.ClusterState().Info.Calls != nil {
		t.Fatalf("server b should hold the passed-on call (%d) but not list it as its own: %+v", n, b.ClusterState().Info.Calls)
	}
	p100.send(d.request(sip.BYE))
	p101.waitBye(t, 2*time.Second) // the hangup reaches the phone on the other server
	for i := 0; i < 40 && recA.count() == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	waitFor(t, "server a wrote no record", func() bool { return recA.count() >= 1 })
	time.Sleep(300 * time.Millisecond) // and no second one
	if n := recA.count(); n != 1 {
		t.Fatalf("server a wrote %d records", n)
	}
	if n := recB.count(); n != 0 {
		t.Fatalf("server b wrote %d records for a call it only passed on", n)
	}

	// A call claiming to come from server a, without a's signature, is refused.
	forger := newPhone(t, b.SIPPort(), "x", "x")
	forger.extra = map[string]string{nodeHeader: "a;wrong;101"}
	if _, code := forger.dial("101"); code != 403 {
		t.Fatalf("forged call: %d", code)
	}
}

func (m *memRecorder) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cdrs)
}

// twoServers starts two engines joined by the fake cluster, which tells each when the other changed.
func twoServers(t *testing.T, cfg config.Config) (a, b *Engine, recA, recB *memRecorder) {
	t.Helper()
	f := &fakeCluster{engines: map[string]*Engine{}}
	a, recA = startEngine(t, cfg, func(e *Engine) { e.Cluster = &pokeLink{fakeLink{f, "a"}} })
	b, recB = startEngine(t, cfg, func(e *Engine) { e.Cluster = &pokeLink{fakeLink{f, "b"}} })
	f.set("a", a)
	f.set("b", b)
	return a, b, recA, recB
}

// pokeLink tells the other engines at once when this one changes, as the database notice does.
type pokeLink struct{ fakeLink }

func (l *pokeLink) Poke() {
	for id, e := range l.f.all() {
		if id != l.id {
			go e.ClusterChanged()
		}
	}
}

// A busy lamp shows calls on another server; a call parked on one server is picked up from another;
// a page reaches phones on both servers with auto-answer; a text reaches a phone on another server.
func TestFeaturesAcrossServers(t *testing.T) {
	cfg := testConfig()
	cfg.PageGroups = []config.PageGroup{{Number: "800", Name: "Everyone", Members: []string{"101", "102"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a, b, _, _ := twoServers(t, cfg)
	pa := registered(t, a, "100", "101")
	pb := registered(t, b, "102")
	p100, p101, p102 := pa[0], pa[1], pb[0]

	// Busy lamp: 102 on server b watches 101 on server a.
	p102.subscribeDialog(t, "101")
	p102.waitNotify(t, `entity="sip:101@`)
	da, db := answeredCall(t, p100, p101, "101")
	p102.waitNotify(t, "<state>confirmed</state>")

	// Parking: 101 parks 100 on server a; 102 on server b picks it up.
	refer := db.request(sip.REFER)
	refer.Set("Refer-To", "<sip:70@127.0.0.1>")
	if r := p101.send(refer); r == nil || r.StatusCode != 202 {
		t.Fatalf("REFER: %v", r)
	}
	p101.waitBye(t, 2*time.Second)
	if len(a.Parked()) != 1 {
		t.Fatalf("not parked on a: %+v", a.Parked())
	}
	pd, code := p102.dial("71")
	if code != 200 {
		t.Fatalf("pickup from the other server: %d", code)
	}
	time.Sleep(150 * time.Millisecond)
	p100.drainRTP()
	pd.sendRTP(10)
	if !p100.gotRTP(2 * time.Second) {
		t.Fatal("no sound after picking up across servers")
	}
	if len(a.Parked()) != 0 {
		t.Fatal("still parked")
	}
	p102.send(pd.request(sip.BYE))
	p100.waitBye(t, 2*time.Second)
	_ = da

	// Text: a message for 102 sent on server a shows on 102's phone on server b.
	a.SendText("102", "Reception", "Your visitor is here")
	select {
	case m := <-p102.messages:
		if !strings.Contains(m, "Your visitor is here") {
			t.Fatalf("message %q", m)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("text did not reach the phone on the other server")
	}

	// Paging from server a reaches 101 on a and 102 on b, both with auto-answer.
	time.Sleep(200 * time.Millisecond)
	for len(p102.incoming) > 0 {
		<-p102.incoming
	}
	page, code := p100.dial("800")
	if code != 200 {
		t.Fatalf("page %d", code)
	}
	for _, p := range []*phone{p101, p102} {
		ring := p.waitRing(3 * time.Second)
		if ring == nil || ring.Req.Get("Call-Info") == "" {
			t.Fatalf("%s not paged with auto-answer: %v", p.user, ring != nil)
		}
		p.answer(ring)
	}
	time.Sleep(200 * time.Millisecond)
	p102.drainRTP()
	page.sendRTP(10)
	if !p102.gotRTP(2 * time.Second) {
		t.Fatal("the phone on the other server did not hear the page")
	}
}

// A trunk held by one server at a time: only the holder registers; another server's calls out go
// through it, carrying the caller's own caller ID, counted once against the channel limit; when the
// holder lets go, the other server registers and calls out itself.
func TestTrunkHeldByOneServer(t *testing.T) {
	carrier := newCarrier(t)
	cfg := trunkConfig(carrier.stack.Port())
	cfg.Trunks[0].Shared, cfg.Trunks[0].MaxChannels = "one", 1
	cfg.FindExtension("101").CallerID = "+12125550199"
	cfg.FindExtension("101").Outbound = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	f := &fakeCluster{engines: map[string]*Engine{}, owner: "a"}
	a, _ := startEngine(t, cfg, func(e *Engine) { e.Cluster = fakeLink{f, "a"} })
	b, recB := startEngine(t, cfg, func(e *Engine) { e.Cluster = fakeLink{f, "b"} })
	f.set("a", a)
	f.set("b", b)
	waitTrunk(t, a, "registered")
	waitTrunk(t, b, "standby")
	if ts := b.TrunkStatus(); ts[0].HeldBy != "Server A" {
		t.Fatalf("b's trunk %+v", ts)
	}
	p101 := registered(t, b, "101")[0]
	done := make(chan int, 1)
	go func() { _, code := p101.dial("2125550123"); done <- code }()
	var inv *sip.Message
	select {
	case inv = <-carrier.invites:
	case <-time.After(15 * time.Second):
		t.Fatal("the call out did not reach the provider")
	}
	if <-done != 200 {
		t.Fatal("call out not answered")
	}
	if !strings.Contains(inv.Get("From"), "+12125550199") {
		t.Fatalf("caller ID lost on the way: %s", inv.Get("From"))
	}
	if n := b.trunkChannels("t1"); n != 1 {
		t.Fatalf("b counts %d channels", n)
	}
	if n := a.localTrunkChannels("t1"); n != 0 {
		t.Fatalf("a counted the passed-on call too: %d", n)
	}
	// The limit is 1, and it is in use: a second call is refused on both servers.
	p102 := registered(t, a, "102")[0]
	if _, code := p102.dial("2125550124"); code < 400 {
		t.Fatalf("second call over the limit: %d", code)
	}
	b.Hangup(b.Calls()[0].ID, "test")
	for i := 0; i < 30 && len(recB.cdrs) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if r := recB.last(t); r.Trunk == "" || r.Direction != "outbound" {
		t.Fatalf("b's call record %+v", r)
	}
	// a lets go; b takes the trunk and registers.
	f.mu.Lock()
	f.owner = "b"
	f.mu.Unlock()
	waitTrunk(t, b, "registered")
}

// follow takes a re-INVITE the phone received: its media and requests go where it says from now on.
func (d *dialog) follow(t *testing.T, server *Engine) {
	t.Helper()
	select {
	case m := <-d.p.reinvite:
		if m.CallID() != d.callID {
			t.Fatalf("re-INVITE for another call")
		}
		d.media = mediaOf(m.Body)
		if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
			d.target = ct.URI.String()
		}
		d.p.pbx.Addr = netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(server.SIPPort()))
	case <-time.After(15 * time.Second):
		t.Fatal("no re-INVITE from the server taking over")
	}
}

// crash stops an engine as a power cut would: no BYE to anyone, sockets and media ports gone.
func crash(e *Engine) {
	for _, c := range e.allCalls() {
		c.mu.Lock()
		legs := []*Leg{c.caller, c.callee}
		c.mu.Unlock()
		for _, l := range legs {
			if l != nil && l.ep != nil {
				l.ep.Close()
			}
		}
	}
	e.cancel()
	e.stack.Close()
}

// A server carrying a call stops; another server takes the call over: both phones are moved to it with
// a re-INVITE, they hear each other again, a hangup reaches the other party through the new server,
// and the call is recorded once, from its first ring.
func TestCallSurvivesServer(t *testing.T) {
	f := &fakeCluster{engines: map[string]*Engine{}}
	a, _ := startEngine(t, testConfig(), func(e *Engine) { e.Cluster = fakeLink{f, "a"} })
	b, recB := startEngine(t, testConfig(), func(e *Engine) { e.Cluster = fakeLink{f, "b"} })
	f.set("a", a)
	f.set("b", b)
	ps := registered(t, a, "100", "101")
	d100, d101 := answeredCall(t, ps[0], ps[1], "101")
	waitFor(t, "the call is not shown", func() bool { return len(a.Calls()) == 1 })
	start := a.Calls()[0].Start
	var snaps [][]byte
	for _, s := range a.CallSnapshots() {
		snaps = append(snaps, s)
	}
	if len(snaps) != 1 {
		t.Fatalf("%d calls saved", len(snaps))
	}
	crash(a)
	if n := b.TakeOver(snaps); n != 1 {
		t.Fatalf("took over %d calls", n)
	}
	d100.follow(t, b)
	d101.follow(t, b)
	ps[0].drainRTP()
	ps[1].drainRTP()
	d100.sendRTP(20)
	d101.sendRTP(20)
	if !ps[1].gotRTP(2*time.Second) || !ps[0].gotRTP(2*time.Second) {
		t.Fatal("the parties do not hear each other through the server that took over")
	}
	ps[0].send(d100.request(sip.BYE))
	ps[1].waitBye(t, 2*time.Second)
	r := recB.last(t)
	if !r.Start.Equal(start.UTC()) || r.Disposition != "answered" || r.From != "100" {
		t.Fatalf("record %+v", r)
	}
	if len(b.Calls()) != 0 {
		t.Fatal("the call is still listed")
	}
}

// Between servers with certificates, calls go over TLS to the published certificate, with SRTP; a
// server presenting another certificate is refused.
func TestCallBetweenServersTLS(t *testing.T) {
	f := &fakeCluster{engines: map[string]*Engine{}}
	withTLS := func(id string) func(*Engine) {
		return func(e *Engine) {
			cert, err := certs.Load(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			h := certs.NewHolder(t.TempDir(), cert)
			e.TLS = &tls.Config{GetCertificate: h.Get}
			e.TLSClient = &tls.Config{GetClientCertificate: h.Client}
			e.CertPin = h.Pin
			e.Cluster = fakeLink{f, id}
		}
	}
	a, _ := startEngine(t, testConfig(), withTLS("a"))
	b, _ := startEngine(t, testConfig(), withTLS("b"))
	f.set("a", a)
	f.set("b", b)
	p100 := registered(t, a, "100")[0]
	p101 := registered(t, b, "101")[0]
	done := make(chan *dialog, 1)
	go func() { d, _ := p100.dial("101"); done <- d }()
	tx := p101.waitRing(3 * time.Second)
	if tx == nil {
		t.Fatal("did not ring over TLS")
	}
	callee := p101.answer(tx)
	d := <-done
	if d == nil {
		t.Fatal("not answered")
	}
	var legs []LegInfo
	for _, c := range a.Calls() {
		legs = append(legs, c.Legs...)
	}
	found := false
	for _, c := range a.allCalls() {
		c.mu.Lock()
		l := c.callee
		c.mu.Unlock()
		if l != nil && l.peer.Transport == "TLS" && l.ep.Secure() {
			found = true
		}
	}
	if !found {
		t.Fatalf("the leg between servers is not TLS with SRTP: %+v", legs)
	}
	d.sendRTP(20)
	callee.sendRTP(20)
	if !p101.gotRTP(2*time.Second) || !p100.gotRTP(2*time.Second) {
		t.Fatal("no sound across servers over SRTP")
	}
	// A server whose certificate does not match what it published is not trusted.
	f.mu.Lock()
	f.forged = true
	f.mu.Unlock()
	p100.send(d.request(sip.BYE))
	p101.waitBye(t, 2*time.Second)
	if _, code := p100.dial("101"); code == 200 {
		t.Fatal("called through a server with the wrong certificate")
	}
}
