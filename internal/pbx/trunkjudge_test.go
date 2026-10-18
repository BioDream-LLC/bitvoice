package pbx

import (
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
)

func judgeEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "a", Name: "Alpha", Enabled: true, Host: "127.0.0.1", Port: 5091, Transport: "udp"},
		{ID: "b", Name: "Beta", Enabled: true, Host: "127.0.0.1", Port: 5092, Transport: "udp"}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"a", "b"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := New(cfg, events.New(), &memRecorder{}, nil)
	for i := range cfg.Trunks {
		st := &trunkState{state: "reachable"}
		st.cfg.Store(&cfg.Trunks[i])
		e.trunks[cfg.Trunks[i].ID] = st
	}
	return e
}

func firstTrunk(e *Engine) string {
	p := e.outbound(e.Config(), nil, "2125551212")
	return p.stages[0].targets[0].trunk.Name
}

// A provider that answers its keep-alive but refuses every call is tried after the others, after two
// judgements in a row; and the call's reasons say so.
func TestTrunkJudgeMovesARefusingTrunk(t *testing.T) {
	e := judgeEngine(t)
	for range 3 {
		e.noteTrunkOutcome("a", 503)
	}
	e.judgeTrunks()
	if firstTrunk(e) != "Alpha" {
		t.Fatal("moved after one judgement")
	}
	e.judgeTrunks()
	if firstTrunk(e) != "Beta" {
		t.Fatalf("Alpha still first: %+v", e.TrunkJudgements())
	}
	p := e.outbound(e.Config(), nil, "2125551212")
	if why := strings.Join(p.why, " "); !strings.Contains(why, "Alpha was tried after the others") || !strings.Contains(why, "refusing calls") {
		t.Fatalf("why %q", why)
	}
	// A call gets through again: back in its place after two judgements.
	e.noteTrunkOutcome("a", 200)
	e.judgeTrunks()
	e.judgeTrunks()
	if firstTrunk(e) != "Alpha" {
		t.Fatalf("not back: %+v", e.TrunkJudgements())
	}
}

// Every trunk failing at once points at this system's own link: nothing is moved.
func TestTrunkJudgeOurLink(t *testing.T) {
	e := judgeEngine(t)
	for _, id := range []string{"a", "b"} {
		e.trunks[id].state = "unreachable"
	}
	e.judgeTrunks()
	e.judgeTrunks()
	j := e.TrunkJudgements()["a"]
	if j.Order != "use" || !strings.Contains(strings.Join(j.Reasons, " "), "this system's own internet link") {
		t.Fatalf("%+v", j)
	}
}

// A failing keep-alive is not trusted while calls still get through.
func TestTrunkJudgeKeepaliveAloneNotTrusted(t *testing.T) {
	e := judgeEngine(t)
	e.trunks["a"].state = "unreachable"
	e.noteTrunkOutcome("a", 486) // the called person was busy: the provider is there
	e.judgeTrunks()
	e.judgeTrunks()
	if j := e.TrunkJudgements()["a"]; j.Order != "use" {
		t.Fatalf("%+v", j)
	}
	if ts := e.TrunkStatus(); ts[0].Judged == nil || ts[0].Judged.Order != "use" {
		t.Fatalf("status %+v", ts[0])
	}
}
