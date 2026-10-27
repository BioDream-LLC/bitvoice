package pbx

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/loadgen"
)

// The call generator places real calls: 10 to the echo test, five at a time, more than the
// per-extension limit allows a phone, and every one is answered with its audio coming back.
func TestCallGenerator(t *testing.T) {
	cfg := testConfig()
	cfg.Security.MaxCallsPerExtension = 2
	e, rec := startEngine(t, cfg)
	e.SetLoadToken("tok")
	pbx := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))
	run, err := loadgen.Start(loadgen.Plan{From: "100", FromSecret: testSecret, To: cfg.System.EchoNumber, Calls: 10,
		Rate: 20, Concurrent: 5, Hold: 300 * time.Millisecond, PBX: pbx, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("run never finished")
	}
	r := run.Snapshot()
	if r.Placed != 10 || r.Answered != 10 || r.Failed != 0 || r.Received == 0 || r.LossPct > 20 || r.MOS < 3.5 {
		t.Fatalf("result %+v", r)
	}
	// Each call's record keeps how the caller's side sounded, for quality by site.
	q := rec.nth(t, 10).Quality
	if len(q) != 1 || q[0].Ext != "100" || q[0].Packets == 0 || q[0].MOS == 0 || q[0].Codec == "" {
		t.Fatalf("quality %+v", q)
	}
	// Without the token the extension limit applies.
	run, _ = loadgen.Start(loadgen.Plan{From: "100", FromSecret: testSecret, To: cfg.System.EchoNumber, Calls: 4,
		Rate: 50, Concurrent: 4, Hold: 500 * time.Millisecond, PBX: pbx})
	<-run.Done()
	if r := run.Snapshot(); r.Failures["486 Too Many Calls"] == 0 {
		t.Fatalf("limit not applied: %+v", r)
	}
}

// Calls to an extension that the generator itself answers.
func TestCallGeneratorAnswers(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	e.SetLoadToken("tok")
	pbx := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))
	run, err := loadgen.Start(loadgen.Plan{From: "100", FromSecret: testSecret, To: "101", AnswerAs: "101", AnswerSecret: testSecret,
		Calls: 3, Rate: 10, Concurrent: 3, Hold: 300 * time.Millisecond, PBX: pbx, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	<-run.Done()
	r := run.Snapshot()
	// Each call's jitter and longest gap as the sound came back, and the runtime's pauses in the run.
	if r.JitterMS.Max <= 0 || r.JitterMS.Max > 200 || r.GapMS.P50 < 15 || r.GapMS.Max > 1000 {
		t.Fatalf("jitter %+v, gaps %+v", r.JitterMS, r.GapMS)
	}
	t.Logf("jitter %+v ms, longest gaps %+v ms, GC pauses %+v", r.JitterMS, r.GapMS, r.Pauses)
	if r.Answered != 3 || r.Received == 0 {
		t.Fatalf("result %+v", r)
	}
}

// Stopping a run hangs up every call at once and leaves none behind in BitVoice.
func TestCallGeneratorStop(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	e.SetLoadToken("tok")
	pbx := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))
	run, err := loadgen.Start(loadgen.Plan{From: "100", FromSecret: testSecret, To: "*43", Calls: 50, Rate: 20,
		Concurrent: 4, Hold: 30 * time.Second, PBX: pbx, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	run.Stop()
	select {
	case <-run.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("still running: %+v", run.Snapshot())
	}
	waitFor(t, "calls left in BitVoice", func() bool { return len(e.Calls()) == 0 })
}
