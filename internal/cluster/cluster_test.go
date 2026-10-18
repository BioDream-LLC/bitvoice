package cluster

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/db"
	"github.com/biodream-llc/bitvoice/internal/dbtest"
)

func open(t *testing.T) *db.DB {
	spec := dbtest.Spec(t)
	if spec == "" {
		t.Skip("set BITVOICE_TEST_POSTGRES: servers share a cluster only through PostgreSQL")
	}
	d, err := db.Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestTwoServers(t *testing.T) {
	ctx := context.Background()
	d := open(t)
	a, err := New(d, t.TempDir(), "1", "10.0.0.1:5060")
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(d, t.TempDir(), "1", "10.0.0.2:5060")
	if err != nil {
		t.Fatal(err)
	}
	if string(a.secret) != string(b.secret) || len(a.secret) != 64 {
		t.Fatal("the servers do not share one secret")
	}
	exp := time.Now().Add(time.Hour)
	a.beat(ctx, State{Regs: []Reg{{Ext: "100", Contact: "sip:100@10.0.1.5", Expires: exp}}, Info: Info{Busy: []string{"100"}}})
	b.beat(ctx, State{Regs: []Reg{{Ext: "101", Contact: "sip:101@10.0.1.6", Expires: exp}}})
	a.beat(ctx, State{Regs: []Reg{{Ext: "100", Contact: "sip:100@10.0.1.5", Expires: exp}}, Info: Info{Busy: []string{"100"}}})
	if r := a.Remote(); len(r) != 1 || r[0].Ext != "101" || r[0].Node != b.Self().ID {
		t.Fatalf("a sees %+v", r)
	}
	if r := b.Remote(); len(r) != 1 || r[0].Ext != "100" {
		t.Fatalf("b sees %+v", r)
	}
	if !b.RemoteBusy("100") || a.RemoteBusy("100") {
		t.Fatal("busy is not shared the right way round")
	}
	if ns := a.Nodes(); len(ns) != 2 || !ns[0].Self || !ns[1].Alive {
		t.Fatalf("nodes %+v", ns)
	}
	// A signed call verifies on the other server, for that call and extension only.
	tok := a.Sign("call-1", "101")
	if n, err := b.Verify(tok, "call-1", "101"); err != nil || n.ID != a.Self().ID {
		t.Fatalf("verify: %v", err)
	}
	if _, err := b.Verify(tok, "call-2", "101"); err == nil {
		t.Fatal("a token for another call was accepted")
	}
	parts := strings.Split(tok, ";")
	parts[1] = "1000"
	if _, err := b.Verify(strings.Join(parts, ";"), "call-1", "101"); err == nil {
		t.Fatal("an old token was accepted")
	}
	// A server that stops is gone at once for the others; its phones with it.
	a.leave()
	b.beat(ctx, State{})
	if len(b.Remote()) != 0 {
		t.Fatalf("a's phones still listed: %+v", b.Remote())
	}
	if _, ok := b.Peer(a.Self().ID); ok {
		t.Fatal("a still counts as alive")
	}
	if _, err := b.Verify(a.Sign("call-3", "101"), "call-3", "101"); err == nil {
		t.Fatal("a call from a server that left was accepted")
	}
	if err := b.Forget(a.Self().ID); err != nil || len(b.Nodes()) != 2 { // the list refreshes at the next beat
		t.Fatalf("forget: %v", err)
	}
	b.beat(ctx, State{})
	if len(b.Nodes()) != 1 {
		t.Fatalf("forgotten server still listed: %+v", b.Nodes())
	}
	// The identity stays across restarts.
	a.SetSelf(a.Self().Name, "10.0.0.11:5060", "")
	if _, err := a.SetSelf(a.Self().Name, "10.0.0.11:5060", "10.0.0.11"); err == nil {
		t.Fatal("a private address was taken as the public one")
	}
	if _, err := a.SetSelf(a.Self().Name, "10.0.0.11:5060", "203.0.113.7:5060"); err == nil {
		t.Fatal("a public address with a port was taken")
	}
	if _, err := a.SetSelf(a.Self().Name, "10.0.0.11:5060", "pbx2.example.com"); err != nil {
		t.Fatal(err)
	}
	a2, _ := New(d, a.dir, "1", "10.9.9.9:5060")
	if a2.Self().ID != a.Self().ID || a2.Self().SIPAddr != "10.0.0.11:5060" {
		t.Fatalf("identity %+v", a2.Self())
	}
}

// A trunk shared one server at a time is held by one server; when it leaves, another takes it.
// A notice from one server wakes the other at once.
func TestTrunkLease(t *testing.T) {
	ctx := context.Background()
	d := open(t)
	a, _ := New(d, t.TempDir(), "1", "10.0.0.1:5060")
	b, _ := New(d, t.TempDir(), "1", "10.0.0.2:5060")
	st := State{OneTrunks: []string{"t1"}}
	a.beat(ctx, st)
	b.beat(ctx, st)
	a.beat(ctx, st)
	if node, mine := a.TrunkOwner("t1"); !mine || node != a.Self().ID {
		t.Fatalf("a does not hold t1: %s %v", node, mine)
	}
	if node, mine := b.TrunkOwner("t1"); mine || node != a.Self().ID {
		t.Fatalf("b should see a holding t1: %s %v", node, mine)
	}
	a.leave()
	b.beat(ctx, st)
	if _, mine := b.TrunkOwner("t1"); !mine {
		t.Fatal("b did not take t1 when a left")
	}

	// Listen and notify: b hears a's poke without waiting for its next beat.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changed := make(chan struct{}, 4)
	b.OnChange = func() { changed <- struct{}{} }
	a2, _ := New(d, a.dir, "1", "10.0.0.1:5060")
	go b.Run(cctx, func() State { return State{} })
	time.Sleep(300 * time.Millisecond)
	for len(changed) > 0 {
		<-changed
	}
	var mu sync.Mutex
	regs := []Reg{}
	go a2.Run(cctx, func() State {
		mu.Lock()
		defer mu.Unlock()
		return State{Regs: regs}
	})
	time.Sleep(2500 * time.Millisecond) // past a beat, so only the poke can explain what follows
	for len(changed) > 0 {
		<-changed
	}
	mu.Lock()
	regs = []Reg{{Ext: "100", Contact: "sip:100@10.0.1.5", Expires: time.Now().Add(time.Hour)}}
	mu.Unlock()
	start := time.Now()
	a2.Poke()
	select {
	case <-changed:
	case <-time.After(1500 * time.Millisecond): // well under the 2 s beat
		t.Fatal("b did not hear a's change at once")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("b heard after %v", d)
	}
}

// A stopped server's calls are claimed by one running server, which carries them on; if the stopped
// server was only cut off and comes back, it lets go of them; a graceful stop hands calls over.
func TestCallsTakenOver(t *testing.T) {
	ctx := context.Background()
	d := open(t)
	a, _ := New(d, t.TempDir(), "1", "10.0.0.1:5060")
	b, _ := New(d, t.TempDir(), "1", "10.0.0.2:5060")
	var mu sync.Mutex
	var took [][]byte
	var letGo []string
	tookCh := make(chan struct{}, 10)
	b.TakeOver = func(calls [][]byte) int {
		mu.Lock()
		took = append(took, calls...)
		mu.Unlock()
		tookCh <- struct{}{}
		return len(calls)
	}
	waitTook := func() {
		select {
		case <-tookCh:
		case <-time.After(2 * time.Second):
		}
	}
	a.LetGo = func(ids []string) { letGo = append(letGo, ids...) }
	calls := map[string][]byte{"c1": []byte(`{"id":"c1"}`)}
	a.beat(ctx, State{Calls: calls})
	b.beat(ctx, State{})
	if len(took) != 0 {
		t.Fatal("took over a running server's call")
	}
	// a goes silent (cut off): after Gone, b carries its call on.
	_, _ = d.Exec(ctx, `UPDATE nodes SET seen_ms = ? WHERE id = ?`, time.Now().Add(-Gone-time.Second).UnixMilli(), a.Self().ID)
	b.beat(ctx, State{})
	waitTook() // carried on in the background, off the beat
	mu.Lock()
	defer mu.Unlock()
	if len(took) != 1 || string(took[0]) != `{"id":"c1"}` {
		t.Fatalf("b took %q", took)
	}
	bCalls := map[string][]byte{"c1": []byte(`{"id":"c1","by":"b"}`)} // b carries it now
	b.beat(ctx, State{Calls: bCalls})                                 // only once
	if len(took) != 1 {
		t.Fatal("taken over twice")
	}
	// a comes back still holding c1: it lets go, and does not overwrite b's copy.
	a.beat(ctx, State{Calls: map[string][]byte{"c1": []byte(`{"id":"c1","stale":true}`)}})
	if len(letGo) != 1 || letGo[0] != "c1" {
		t.Fatalf("a let go of %v", letGo)
	}
	var node string
	_ = d.QueryRow(ctx, `SELECT node FROM live_calls WHERE id = 'c1'`).Scan(&node)
	if node != b.Self().ID {
		t.Fatalf("c1 held by %s", node)
	}
	// b's call ends: its record goes.
	b.beat(ctx, State{})
	var n int
	_ = d.QueryRow(ctx, `SELECT COUNT(*) FROM live_calls`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d calls left after the call ended", n)
	}
	// A graceful stop of b hands its calls to a, which takes them at its next beat.
	tookA := make(chan [][]byte, 1)
	a.TakeOver = func(calls [][]byte) int { tookA <- calls; return len(calls) }
	a.beat(ctx, State{})
	b.beat(ctx, State{})
	if got := b.HandOver(map[string][]byte{"c2": []byte(`{"id":"c2"}`)}); len(got) != 1 {
		t.Fatal("nothing handed over though a is running")
	}
	a.beat(ctx, State{})
	select {
	case got := <-tookA:
		if len(got) != 1 {
			t.Fatalf("a took %q", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a did not take the handed-over call")
	}
}
