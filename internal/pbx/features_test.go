package pbx

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

func TestRingGroupFirstAnswerWins(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	done := make(chan int, 1)
	go func() {
		_, code := a.dial("600")
		done <- code
	}()
	rb := b.waitRing(2 * time.Second)
	rc := c.waitRing(2 * time.Second)
	if rb == nil || rc == nil {
		t.Fatal("both members should ring")
	}
	c.answer(rc)
	if code := <-done; code != 200 {
		t.Fatalf("caller got %d", code)
	}
	// 101 is cancelled: its INVITE ends with 487 (sent by the phone in reply to CANCEL).
	deadline := time.Now().Add(15 * time.Second)
	for len(e.Calls()) != 1 || len(e.Calls()[0].Legs) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("losing fork not dropped: %+v", e.Calls())
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.Hangup(e.Calls()[0].ID, "test")
	if r := rec.last(t); r.AnsweredBy != "102 Ext 102" {
		t.Fatalf("answered by %q", r.AnsweredBy)
	}
}

func TestNoAnswerTimesOut(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].RingSeconds = 5
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	start := time.Now()
	_, code := ps[0].dial("101")
	if code != 480 {
		t.Fatalf("unanswered call: %d", code)
	}
	if d := time.Since(start); d < 4*time.Second || d > 8*time.Second {
		t.Fatalf("rang for %v, want about 5s", d)
	}
}

func answeredCall(t *testing.T, caller, callee *phone, number string) (*dialog, *dialog) {
	t.Helper()
	type res struct {
		d    *dialog
		code int
	}
	ch := make(chan res, 1)
	go func() {
		d, code := caller.dial(number)
		ch <- res{d, code}
	}()
	ring := callee.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatalf("%s did not ring", callee.user)
	}
	cd := callee.answer(ring)
	r := <-ch
	if r.code != 200 {
		t.Fatalf("call %s -> %s: %d", caller.user, number, r.code)
	}
	return r.d, cd
}

func TestHoldPlaysToneAndResumes(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	ad, _ := answeredCall(t, a, b, "101")

	hold := ad.request(sip.INVITE)
	hold.Set("Content-Type", "application/sdp")
	hold.Body = a.offer("sendonly")
	r := a.send(hold)
	if r == nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), "a=recvonly") {
		t.Fatalf("hold answer: %v", r)
	}
	b.drainRTP()
	if !b.gotRTP(time.Second) {
		t.Fatal("held party hears no hold tone")
	}
	if e.Calls()[0].State != "held" {
		t.Fatalf("state %s", e.Calls()[0].State)
	}
	resume := ad.request(sip.INVITE)
	resume.Set("Content-Type", "application/sdp")
	resume.Body = a.offer("sendrecv")
	if r := a.send(resume); r == nil || !strings.Contains(string(r.Body), "a=sendrecv") {
		t.Fatalf("resume answer: %v", r)
	}
	if e.Calls()[0].State != "answered" {
		t.Fatalf("state after resume %s", e.Calls()[0].State)
	}
}

func TestBlindTransfer(t *testing.T) {
	cfg := testConfig()
	cfg.System.AI.Enabled, cfg.System.AI.Briefings = true, true
	briefed := make(chan BriefRequest, 1)
	e, _ := startEngine(t, cfg, func(e *Engine) { e.Brief = func(b BriefRequest) { briefed <- b } })
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	ad, bd := answeredCall(t, a, b, "101")
	var sa, sb uint16 = 1, 1
	ad.speak(10, &sa, 0) // what was said before the transfer is kept for the briefing
	bd.speak(10, &sb, 0)
	time.Sleep(100 * time.Millisecond)

	// 101 transfers 100 to 102.
	refer := bd.request(sip.REFER)
	refer.Set("Refer-To", "<sip:102@127.0.0.1>")
	refer.Set("Referred-By", "<sip:101@127.0.0.1>")
	if r := b.send(refer); r == nil || r.StatusCode != 202 {
		t.Fatalf("REFER: %v", r)
	}
	ring := c.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("transfer target did not ring")
	}
	select {
	case br := <-briefed:
		if br.Target != "102" || br.From != "100" || len(br.Caller) == 0 || len(br.Callee) == 0 {
			t.Fatalf("briefing request %+v (caller %d, callee %d samples)", br.Target, len(br.Caller), len(br.Callee))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no briefing asked for")
	}
	cd := c.answer(ring)
	// The transferor is told it worked and is released.
	gotOK := false
	deadline := time.After(15 * time.Second)
	for !gotOK {
		select {
		case n := <-b.notifies:
			gotOK = strings.HasPrefix(n, "SIP/2.0 200")
		case <-deadline:
			t.Fatal("no final NOTIFY")
		}
	}
	select {
	case <-b.byes:
	case <-time.After(15 * time.Second):
		t.Fatal("transferor not released")
	}
	// 102 and 100 now talk.
	a.drainRTP()
	cd.sendRTP(3)
	if !a.gotRTP(time.Second) {
		t.Fatal("no media from the new party to the caller")
	}
	calls := e.Calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0].ToName, "102") {
		t.Fatalf("calls after transfer %+v", calls)
	}
}

func TestEmergencyNotifies(t *testing.T) {
	e, rec := startEngine(t, testConfig())
	ps := registered(t, e, "100", "102")
	sub := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(sub)
	// No trunk is configured, so the call cannot leave, but on-site staff must still be told.
	if _, code := ps[0].dial("911"); code != 503 {
		t.Fatalf("911 without route: %d", code)
	}
	select {
	case m := <-ps[1].messages:
		if !strings.Contains(m, "EMERGENCY") || !strings.Contains(m, "100") {
			t.Fatalf("message %q", m)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("notification phone got no message")
	}
	found := false
	for !found {
		select {
		case ev := <-sub.C:
			found = ev.Type == "emergency.call"
		case <-time.After(15 * time.Second):
			t.Fatal("no emergency event")
		}
	}
	if r := rec.last(t); r.Direction != "emergency" {
		t.Fatalf("CDR direction %q", r.Direction)
	}
}
