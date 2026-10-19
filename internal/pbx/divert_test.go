package pbx

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second) // returns as soon as cond holds; slow machines need the room
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestForwardAllCode(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	// *72 then the number in one go.
	if _, code := ps[0].dial("*72102"); code != 200 {
		t.Fatalf("*72102 answered %d", code)
	}
	waitFor(t, "forwarding not set", func() bool { return e.DivertOf("100").All == "102" })
	go ps[1].dial("100")
	if ring := ps[2].waitRing(2 * time.Second); ring == nil {
		t.Fatal("a call to 100 did not ring 102, where it is forwarded")
	}
	select {
	case <-ps[0].incoming:
		t.Fatal("100 rang although it forwards everything")
	case <-time.After(200 * time.Millisecond):
	}
	if _, code := ps[0].dial("*73"); code != 200 {
		t.Fatalf("*73 answered %d", code)
	}
	waitFor(t, "forwarding not cleared", func() bool { return e.DivertOf("100").All == "" })
}

func TestForwardAskedForNumber(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	d, code := ps[0].dial("*72")
	if code != 200 {
		t.Fatalf("*72 answered %d", code)
	}
	// The tone that asks for the number (after a moment of silence): from then on the code listens.
	if !receivedLoud(ps[0].rtp, 2*time.Second) {
		t.Fatal("no tone asking for the number")
	}
	var seq uint16 = 1
	d.press(&seq, "101#")
	waitFor(t, "forwarding entered on the keypad not set", func() bool { return e.DivertOf("100").All == "101" })
}

func TestForwardRefusesOutsideWithoutPermission(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[0].Outbound = false
	e, _ := startEngine(t, cfg)
	if err := e.SetDivert(Divert{Ext: "100", All: "2125550199"}); err != ErrDivertOut {
		t.Fatalf("forward outside without permission: %v", err)
	}
	if err := e.SetDivert(Divert{Ext: "100", All: "100"}); err != ErrDivertSelf {
		t.Fatalf("forward to itself: %v", err)
	}
	if err := e.SetDivert(Divert{Ext: "100", All: "101"}); err != nil {
		t.Fatal(err)
	}
}

func TestDNDCodes(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].Voicemail.Enabled = false
	cfg.Extensions[1].NoAnswer = &config.Destination{Type: "extension", Target: "102"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var saved DivertState
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.SaveDiverts = func(s DivertState) error { mu.Lock(); saved = s; mu.Unlock(); return nil }
	})
	ps := registered(t, e, "100", "101", "102")
	if _, code := ps[1].dial("*76"); code != 200 {
		t.Fatalf("*76 answered %d", code)
	}
	waitFor(t, "do not disturb not on", func() bool { return e.DND("101") })
	waitFor(t, "do not disturb not saved", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(saved.Away) == 1 && saved.Away[0].Ext == "101"
	})
	go ps[0].dial("101")
	if ring := ps[2].waitRing(2 * time.Second); ring == nil {
		t.Fatal("do not disturb did not send the call to the no-answer destination")
	}
	if _, code := ps[1].dial("*79"); code != 200 {
		t.Fatalf("*79 answered %d", code)
	}
	waitFor(t, "do not disturb still on", func() bool { return !e.DND("101") })
}

func TestForwardBusyAndNoAnswer(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].RingSeconds = 5
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	if err := e.SetDivert(Divert{Ext: "101", NoAnswer: "102"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	go ps[0].dial("101")
	if ring := ps[1].waitRing(2 * time.Second); ring == nil {
		t.Fatal("101 did not ring first")
	}
	if ring := ps[2].waitRing(8 * time.Second); ring == nil {
		t.Fatal("not forwarded to 102 when 101 did not answer")
	}
	if d := time.Since(start); d < 4*time.Second {
		t.Fatalf("forwarded after %v, before 101's ring time", d)
	}
	for _, c := range e.Calls() {
		e.Hangup(c.ID, "test")
	}
	time.Sleep(200 * time.Millisecond)
	ps[0].drainRTP()
	// Busy: 101 is on a call with 100; a call from 102 goes to 100.
	if err := e.SetDivert(Divert{Ext: "101", Busy: "100"}); err != nil {
		t.Fatal(err)
	}
	answeredCall(t, ps[0], ps[1], "101")
	go ps[2].dial("101")
	if ring := ps[0].waitRing(2 * time.Second); ring == nil {
		t.Fatal("a call to busy 101 was not forwarded to 100")
	}
}

func TestFollowMe(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].RingSeconds = 5
	cfg.Extensions[1].FollowMe = config.FollowMe{Enabled: true, Numbers: []string{"102"}, RingSeconds: 5}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	go ps[0].dial("101")
	if ring := ps[1].waitRing(2 * time.Second); ring == nil {
		t.Fatal("101's own phone did not ring first")
	}
	ring := ps[2].waitRing(8 * time.Second)
	if ring == nil {
		t.Fatal("follow-me did not ring 102")
	}
	ps[2].answer(ring)
	waitFor(t, "follow-me call not answered", func() bool {
		cs := e.Calls()
		return len(cs) == 1 && cs[0].State == "answered"
	})
	e.Hangup(e.Calls()[0].ID, "test")
	if r := rec.last(t); r.AnsweredExt != "102" {
		t.Fatalf("answered by %q", r.AnsweredExt)
	}
	// *21 switches it off: the person's own switch wins over the configuration.
	if _, code := ps[1].dial("*21"); code != 200 {
		t.Fatalf("*21 answered %d", code)
	}
	waitFor(t, "follow-me still on", func() bool { return !e.followMeOn(e.Config().FindExtension("101")) })
}

func TestCallPickup(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].PickupGroup = "front"
	cfg.Extensions[2].PickupGroup = "Front"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, rec := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	done := make(chan int, 1)
	go func() {
		_, code := ps[0].dial("101")
		done <- code
	}()
	if ring := ps[1].waitRing(2 * time.Second); ring == nil {
		t.Fatal("101 did not ring")
	}
	if _, code := ps[2].dial("*8"); code != 200 {
		t.Fatalf("*8 answered %d", code)
	}
	if code := <-done; code != 200 {
		t.Fatalf("the caller was not answered by the pickup: %d", code)
	}
	waitFor(t, "picked-up call not connected", func() bool {
		cs := e.Calls()
		return len(cs) == 1 && cs[0].State == "answered"
	})
	ps[2].drainRTP()
	e.Hangup(e.Calls()[0].ID, "test")
	waitFor(t, "no record of the picked-up call", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, r := range rec.cdrs {
			if r.From == "100" && strings.Contains(r.AnsweredBy, "102") {
				return true
			}
		}
		return false
	})
}

func TestDirectedPickupAndNothingToPick(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	if _, code := ps[2].dial("*8"); code != 404 {
		t.Fatalf("*8 with no pickup group: %d", code)
	}
	done := make(chan int, 1)
	go func() {
		_, code := ps[0].dial("101")
		done <- code
	}()
	if ring := ps[1].waitRing(2 * time.Second); ring == nil {
		t.Fatal("101 did not ring")
	}
	if _, code := ps[2].dial("**101"); code != 200 {
		t.Fatalf("**101 answered %d", code)
	}
	if code := <-done; code != 200 {
		t.Fatalf("directed pickup did not answer the caller: %d", code)
	}
}

func TestIntercomAutoAnswers(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[2].NoIntercom = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	go ps[0].dial("*80101")
	ring := ps[1].waitRing(2 * time.Second)
	if ring == nil || !strings.Contains(ring.Req.Get("Call-Info"), "answer-after=0") {
		t.Fatal("intercom did not ring 101 with auto-answer")
	}
	go ps[0].dial("*80102")
	ring = ps[2].waitRing(2 * time.Second)
	if ring == nil || ring.Req.Get("Call-Info") != "" {
		t.Fatal("an extension refusing intercom should ring as usual")
	}
}

func TestSpeedDials(t *testing.T) {
	cfg := testConfig()
	cfg.System.SpeedDials = []config.SpeedDial{{Code: "1", Number: "101", Name: "Reception"}, {Code: "2", Number: "102"}}
	cfg.Extensions[0].SpeedDials = []config.SpeedDial{{Code: "2", Number: "101", Name: "My own"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	go ps[0].dial("*01")
	if ps[1].waitRing(2*time.Second) == nil {
		t.Fatal("company speed dial 1 did not ring 101")
	}
	go ps[0].dial("*02")
	if ps[1].waitRing(2*time.Second) == nil {
		t.Fatal("the person's own speed dial 2 should win over the company's")
	}
	if _, code := ps[0].dial("*09"); code != 404 {
		t.Fatalf("an unknown speed dial: %d", code)
	}
}

func TestFeatureCodeValidation(t *testing.T) {
	cfg := testConfig()
	cfg.System.Codes.ForwardAll = "72"
	cfg.System.Codes.Intercom = "x"
	cfg.System.SpeedDials = []config.SpeedDial{{Code: "1", Number: "101"}, {Code: "1", Number: "102"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("bad codes accepted")
	}
	for _, want := range []string{"system.codes.forwardAll", "system.codes.intercom", "system.speedDials[1].code"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no problem reported for %s: %v", want, err)
		}
	}
}

// heard reports whether a phone receives audio louder than silence within the timeout.
func (p *phone) heard(timeout time.Duration) bool {
	_ = p.rtp.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 2048)
	for {
		n, _, err := p.rtp.ReadFromUDP(buf)
		if err != nil {
			return false
		}
		if n < 12+160 || buf[1]&0x7f != 0 {
			continue
		}
		loud := 0
		for _, b := range buf[12:n] {
			if v := media.UlawToLinear(b); v > 2000 || v < -2000 {
				loud++
			}
		}
		if loud > 40 {
			return true
		}
	}
}

func TestConferenceRoom(t *testing.T) {
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "500", Name: "Team", PIN: "1234", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	join := func(p *phone) *dialog {
		d, code := p.dial("500")
		if code != 200 {
			t.Fatalf("%s: room answered %d", p.user, code)
		}
		prompted(t, p)
		var seq uint16 = 1
		d.press(&seq, "1234#")
		return d
	}
	da, db := join(ps[0]), join(ps[1])
	waitFor(t, "two people not in the room", func() bool {
		rs := e.Conferences()
		return len(rs) == 1 && len(rs[0].Members) == 2
	})
	ps[1].drainRTP()
	var seq uint16 = 100
	spoken := make(chan struct{})
	go func() {
		defer close(spoken)
		for range 3 {
			da.speak(10, &seq, 0)
		}
	}()
	if !ps[1].heard(2 * time.Second) {
		t.Fatal("101 does not hear 100 in the room")
	}
	// A wrong PIN three times: refused.
	d, code := ps[2].dial("500")
	if code != 200 {
		t.Fatalf("room answered %d", code)
	}
	time.Sleep(400 * time.Millisecond)
	var s2 uint16 = 1
	for range 3 {
		d.press(&s2, "9999#")
		time.Sleep(900 * time.Millisecond)
	}
	waitFor(t, "a wrong PIN was let in", func() bool {
		rs := e.Conferences()
		return len(rs) == 1 && len(rs[0].Members) == 2
	})
	// Muted from the web interface: nobody hears 100 any more.
	var a string
	for _, m := range e.Conferences()[0].Members {
		if m.Number == "100" {
			a = m.ID
		}
	}
	if err := e.MuteConferenceMember("500", a, true); err != nil {
		t.Fatal(err)
	}
	<-spoken // one speaker at a time: they share the sequence number
	time.Sleep(100 * time.Millisecond)
	ps[1].drainRTP()
	go func() {
		for range 3 {
			da.speak(10, &seq, 0)
		}
	}()
	if ps[1].heard(700 * time.Millisecond) {
		t.Fatal("101 still hears 100 after 100 was muted")
	}
	ps[0].send(da.request("BYE"))
	ps[1].send(db.request("BYE"))
	waitFor(t, "room not closed when everyone left", func() bool { return len(e.Conferences()) == 0 })
}

func TestConferenceWaitsForLeader(t *testing.T) {
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "500", Name: "Briefing", LeaderPIN: "8888", WaitForLeader: true, Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	d, _ := ps[0].dial("500")
	prompted(t, ps[0])
	var seq uint16 = 1
	d.press(&seq, "#") // no member PIN: # alone
	waitFor(t, "member not waiting", func() bool {
		rs := e.Conferences()
		return len(rs) == 1 && rs[0].Waiting == 1
	})
	l, _ := ps[1].dial("500")
	prompted(t, ps[1])
	var s2 uint16 = 1
	l.press(&s2, "8888#")
	waitFor(t, "the leader did not let the member in", func() bool {
		rs := e.Conferences()
		return len(rs) == 1 && rs[0].Waiting == 0 && len(rs[0].Members) == 2
	})
	ps[1].send(l.request("BYE"))
	waitFor(t, "the room did not close when the leader left", func() bool { return len(e.Conferences()) == 0 })
}

func TestDivertsSurviveRestart(t *testing.T) {
	var saved DivertState
	e, _ := startEngine(t, testConfig(), func(e *Engine) { e.SaveDiverts = func(s DivertState) error { saved = s; return nil } })
	if err := e.SetDivert(Divert{Ext: "100", All: "101"}); err != nil {
		t.Fatal(err)
	}
	e.SetDND("102", true)
	e2, _ := startEngine(t, testConfig())
	e2.LoadDiverts(saved)
	if e2.DivertOf("100").All != "101" || !e2.DND("102") {
		t.Fatalf("not restored: %+v %v", e2.DivertOf("100"), e2.DND("102"))
	}
}

// Follow-me to an outside number with confirmation: the call is put through only when whoever
// answers there presses 1.
func TestFollowMeConfirm(t *testing.T) {
	// The far end: a server whose number 2125550199 rings its extension 100.
	fcfg := testConfig()
	fcfg.Trunks = []config.Trunk{{ID: "in", Name: "Carrier", Enabled: true, Host: "127.0.0.1", AllowFrom: []string{"127.0.0.1"}}}
	fcfg.InboundRoutes = []config.InboundRoute{{Name: "Mobile", DID: "+12125550199", Destination: config.Destination{Type: "extension", Target: "100"}}}
	if err := fcfg.Validate(); err != nil {
		t.Fatal(err)
	}
	far, _ := startEngine(t, fcfg)
	mobile := registered(t, far, "100")[0]

	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "out", Name: "Line", Enabled: true, Host: "127.0.0.1", Port: far.SIPPort()}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Prepend: "+1", Trunks: []string{"out"}}}
	cfg.Extensions[1].RingSeconds = 5
	cfg.Extensions[1].FollowMe = config.FollowMe{Enabled: true, Numbers: []string{"2125550199"}, RingSeconds: 20, Confirm: true}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	done := make(chan int, 1)
	go func() {
		_, code := ps[0].dial("101")
		done <- code
	}()
	if ps[1].waitRing(2*time.Second) == nil {
		t.Fatal("101's own phone did not ring")
	}
	ring := mobile.waitRing(8 * time.Second)
	if ring == nil {
		t.Fatal("follow-me did not call the outside number")
	}
	md := mobile.answer(ring)
	select {
	case code := <-done:
		t.Fatalf("put through before 1 was pressed: %d", code)
	case <-time.After(800 * time.Millisecond):
	}
	var seq uint16 = 1
	md.press(&seq, "1")
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("caller got %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("pressing 1 did not put the call through")
	}
}

func TestHDConferenceRoom(t *testing.T) {
	cfg := testConfig()
	cfg.Conferences = []config.Conference{{Number: "500", Name: "HD", Quiet: true, HD: true}, {Number: "501", Name: "Plain", Quiet: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	ps[0].dial("500")
	ps[1].dial("501")
	waitFor(t, "rooms not open", func() bool { return len(e.Conferences()) == 2 })
	rs := e.Conferences()
	if !rs[0].HD || rs[1].HD {
		t.Fatalf("HD flags %+v %+v", rs[0], rs[1])
	}
}
