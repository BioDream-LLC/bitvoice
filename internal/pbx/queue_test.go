package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func queueConfig(strategy string) config.Config {
	c := testConfig()
	c.Queues = []config.Queue{{Number: "700", Name: "Support", Agents: []string{"101", "102"}, Strategy: strategy, RingSeconds: 5}}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func TestQueueConnectsAgent(t *testing.T) {
	e, rec := startEngine(t, queueConfig("longest-idle"))
	ps := registered(t, e, "100", "101", "102")
	d, code := ps[0].dial("700")
	if code != 200 {
		t.Fatalf("queue answered %d", code)
	}
	if !ps[0].gotRTP(2 * time.Second) {
		t.Fatal("no queue tone")
	}
	// Longest idle with nobody used yet: the first agent in the list rings, alone.
	ring := ps[1].waitRing(3 * time.Second)
	if ring == nil {
		t.Fatal("agent 101 not rung")
	}
	if ps[2].waitRing(200*time.Millisecond) != nil {
		t.Fatal("agent 102 rung too under longest-idle")
	}
	if qs := e.Queues(); len(qs[0].Waiting) != 1 || qs[0].Agents[0].State != "ringing" {
		t.Fatalf("status %+v", qs)
	}
	agent := ps[1].answer(ring)
	time.Sleep(100 * time.Millisecond)
	ps[0].drainRTP()
	agent.sendRTP(5)
	if !ps[0].gotRTP(time.Second) {
		t.Fatal("caller does not hear the agent")
	}
	if qs := e.Queues(); len(qs[0].Waiting) != 0 || qs[0].Agents[0].State != "busy" || qs[0].Totals.Answered != 1 {
		t.Fatalf("status after answer %+v", qs)
	}
	ps[0].send(d.request("BYE"))
	ps[1].waitBye(t, 2*time.Second)
	if r := rec.last(t); r.AnsweredBy == "" || r.Disposition != "answered" {
		t.Fatalf("record %+v", r)
	}

	// Next caller: 101 was just used, so 102 (idle longer) rings.
	d2, _ := ps[0].dial("700")
	if ps[2].waitRing(3*time.Second) == nil {
		t.Fatal("longest-idle did not pick 102")
	}
	// The caller gives up while the agent rings: the ringing stops and the call counts as abandoned.
	ps[0].send(d2.request("BYE"))
	waitFor(t, "the call is not counted as abandoned", func() bool {
		qs := e.Queues()
		return qs[0].Totals.Abandoned == 1 && len(qs[0].Waiting) == 0
	})
}

func TestQueueRingAllAndTimeout(t *testing.T) {
	cfg := queueConfig("ring-all")
	cfg.Queues[0].MaxWaitSeconds = 1
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	_, code := ps[0].dial("700")
	if code != 200 {
		t.Fatal(code)
	}
	if ps[1].waitRing(2*time.Second) == nil || ps[2].waitRing(2*time.Second) == nil {
		t.Fatal("ring-all did not ring every agent")
	}
	// Nobody answers; past the wait limit the caller is told goodbye (no timeout destination).
	ps[0].waitBye(t, 8*time.Second)
	if qs := e.Queues(); qs[0].Totals.TimedOut != 1 {
		t.Fatalf("%+v", qs[0].Totals)
	}
}

// A schedule closed right now sends the call to its closed destination (the echo test here, so the
// result is visible as an answer), and open sends it to the open one (an unregistered extension: 480).
func TestScheduleRouting(t *testing.T) {
	cfg := testConfig()
	now := time.Now().In(cfg.System.Location())
	all := []int{0, 1, 2, 3, 4, 5, 6}
	open := config.Schedule{ID: "open", Name: "Always", Hours: []config.OpenHours{{Days: all, Start: "00:00", End: "24:00"}},
		Open: config.Destination{Type: "extension", Target: "102"}, Closed: config.Destination{Type: "echo"}}
	closed := open
	closed.ID, closed.Name = "shut", "Holiday"
	closed.Holidays = []config.Holiday{{Date: now.Format("2006-01-02")}}
	cfg.Schedules = []config.Schedule{open, closed}
	cfg.InboundRoutes = nil
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	if p := e.resolve(&cfg, config.Destination{Type: "schedule", Target: "shut"}, 0); p.local != "echo" {
		t.Fatalf("closed: %+v", p)
	}
	if p := e.resolve(&cfg, config.Destination{Type: "schedule", Target: "open"}, 0); p.reject != 480 || p.toName != "Ext 102" {
		t.Fatalf("open: %+v", p)
	}
}

// A paused agent is skipped; *46 from the phone pauses and resumes; *45 logs out.
func TestAgentPresence(t *testing.T) {
	e, _ := startEngine(t, queueConfig("longest-idle"))
	ps := registered(t, e, "100", "101", "102")
	if d, _ := ps[1].dial("*46"); d == nil {
		t.Fatal("pause code refused")
	}
	ps[1].waitBye(t, 3*time.Second)
	// The pause call is over once its BYE is handled: until then 101 is busy on it.
	for deadline := time.Now().Add(15 * time.Second); e.Queues()[0].Agents[0].State != "paused"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("101 not paused: %+v", e.Queues()[0].Agents)
		}
	}
	go ps[0].dial("700")
	if ps[2].waitRing(3*time.Second) == nil {
		t.Fatal("the available agent was not rung")
	}
	if ps[1].waitRing(200*time.Millisecond) != nil {
		t.Fatal("the paused agent was rung")
	}
	e.SetAgent("102", true, false, "")
	if qs := e.Queues(); qs[0].Agents[1].State != "logged-out" {
		t.Fatalf("102 not logged out: %+v", qs[0].Agents)
	}
	e.SetAgent("101", false, false, "")
	if qs := e.Queues(); qs[0].Agents[0].State == "paused" || len(e.Agents()) != 1 {
		t.Fatalf("after resume %+v %+v", qs[0].Agents, e.Agents())
	}
}

// A VIP caller goes in front of callers already waiting; a returning caller rings the agent who last
// answered them first, when that agent is free.
func TestQueueVIPAndSticky(t *testing.T) {
	cfg := queueConfig("longest-idle")
	cfg.Queues[0].Agents = []string{"103"} // nobody signed in yet, so callers wait
	cfg.Extensions = append(cfg.Extensions, config.Extension{Number: "103", Name: "Ext 103", Secret: testSecret, Enabled: true, RingSeconds: 5})
	cfg.System.VIPs = []config.VIP{{Number: "102", Name: "Big client"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	ps[0].dial("700")
	ps[1].dial("700")
	time.Sleep(300 * time.Millisecond)
	ps[2].dial("700") // the VIP, last in
	waitFor(t, "three callers are not waiting", func() bool { return len(e.Queues()[0].Waiting) == 3 })
	w := e.Queues()[0].Waiting
	if len(w) != 3 || w[0].From != "102" || !w[0].VIP || w[1].From != "100" || w[2].From != "101" {
		t.Fatalf("waiting %+v", w)
	}

	// Sticky: 102 rings first for the caller it answered last time, though 101 has been idle longer.
	cfg2 := queueConfig("longest-idle")
	cfg2.Queues[0].Sticky = true
	// Set before the engine starts: a call reads it on another goroutine.
	e2, _ := startEngine(t, cfg2, func(e *Engine) {
		e.LastAgent = func(number string, since time.Time) string {
			if number == "100" && since.Before(time.Now().AddDate(0, 0, -29)) {
				return "102"
			}
			return ""
		}
	})
	qs := registered(t, e2, "100", "101", "102")
	qs[0].dial("700")
	if qs[2].waitRing(3*time.Second) == nil {
		t.Fatal("the last agent was not rung first")
	}
	if qs[1].waitRing(200*time.Millisecond) != nil {
		t.Fatal("101 rang too")
	}
}
