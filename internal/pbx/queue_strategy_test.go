package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Strategies, penalties and weights choose among available agents (registered, not busy).
func TestQueueStrategiesAndPenalties(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions = append(cfg.Extensions, config.Extension{Number: "103", Name: "Ext 103", Secret: testSecret, Enabled: true, RingSeconds: 5})
	cfg.Queues = []config.Queue{
		{Number: "700", Name: "Support", Agents: []string{"101", "102", "103"}, Strategy: "linear", RingSeconds: 5},
		{Number: "701", Name: "VIP line", Agents: []string{"101"}, Strategy: "linear", RingSeconds: 5, Weight: 10},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	registered(t, e, "101", "102", "103")
	pick := func(mut func(q *config.Queue)) []string {
		c := *e.Config()
		c.Queues = append([]config.Queue(nil), c.Queues...)
		mut(&c.Queues[0])
		return e.pickAgents(&c, &c.Queues[0], e.queue("700"))
	}
	if got := pick(func(q *config.Queue) {}); len(got) != 1 || got[0] != "101" {
		t.Fatalf("linear picked %v, want 101", got)
	}
	// Penalties: 101 and 102 rank below 103, so only 103 is offered, under any strategy.
	for _, s := range []string{"linear", "ring-all", "longest-idle", "round-robin", "random", "fewest-calls"} {
		got := pick(func(q *config.Queue) { q.Strategy = s; q.Penalties = map[string]int{"101": 5, "102": 5} })
		if len(got) != 1 || got[0] != "103" {
			t.Fatalf("%s with penalties picked %v, want 103", s, got)
		}
	}
	// ring-all rings the whole lowest tier.
	if got := pick(func(q *config.Queue) { q.Strategy = "ring-all"; q.Penalties = map[string]int{"103": 1} }); len(got) != 2 {
		t.Fatalf("ring-all tier %v", got)
	}
	// fewest-calls: the agent who took fewest calls today.
	q := e.queue("700")
	q.mu.Lock()
	q.rollDay()
	q.taken["101"], q.taken["102"], q.taken["103"] = 4, 1, 2
	q.mu.Unlock()
	if got := pick(func(q *config.Queue) { q.Strategy = "fewest-calls" }); got[0] != "102" {
		t.Fatalf("fewest-calls picked %v", got)
	}
	// random and weighted-random spread calls; weighted favours the low penalty.
	seen := map[string]int{}
	for range 300 {
		seen[pick(func(q *config.Queue) { q.Strategy = "random" })[0]]++
	}
	if len(seen) != 3 {
		t.Fatalf("random reached %v", seen)
	}
	seen = map[string]int{}
	for range 2000 {
		seen[pick(func(q *config.Queue) {
			q.Strategy = "weighted-random"
			q.Penalties = map[string]int{"102": 100, "103": 100}
		})[0]]++
	}
	if seen["101"] < 1500 || seen["102"] == 0 {
		t.Fatalf("weighted-random spread %v", seen)
	}
	// Weight: with a caller waiting in the heavier queue 701, its agent 101 is left to it.
	vq := e.queue("701")
	vq.mu.Lock()
	vq.waiting = append(vq.waiting, &Call{})
	vq.mu.Unlock()
	if got := pick(func(q *config.Queue) {}); got[0] != "102" {
		t.Fatalf("weight: picked %v, want 102", got)
	}
	vq.mu.Lock()
	vq.waiting = nil
	vq.mu.Unlock()
}

func TestQueueExpectedWait(t *testing.T) {
	q := &queueState{}
	if q.expectedWait() != 0 {
		t.Fatal("no history should give 0")
	}
	q.waitSum, q.waitN = 150*time.Second, 2 // 75 s average
	if q.expectedWait() != 2 {
		t.Fatalf("got %d minutes", q.expectedWait())
	}
}

func TestQueueStrategyValidation(t *testing.T) {
	c := queueConfig("linear")
	c.Queues[0].Strategy = "rrmemory"
	if c.Validate() == nil {
		t.Fatal("unknown strategy accepted")
	}
	c = queueConfig("linear")
	c.Queues[0].Penalties = map[string]int{"101": 200}
	if c.Validate() == nil {
		t.Fatal("penalty 200 accepted")
	}
	c = queueConfig("linear")
	c.Queues[0].Penalties = map[string]int{"555": 3}
	if err := c.Validate(); err != nil || len(c.Queues[0].Penalties) != 0 {
		t.Fatalf("penalty of a non-agent kept: %v %v", err, c.Queues[0].Penalties)
	}
}
