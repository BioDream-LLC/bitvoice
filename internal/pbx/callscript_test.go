package pbx

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/store/storetest"
)

// The call script of these tests: a beep invites two keys; 42 rings 101 (who answers), anything
// else leaves a message for 100, and nothing at all hands the call to the fallback (101).
const testCallScript = `
play("beep")
const k = collect({ max: 2, timeout: 3, interDigit: 2 })
log("pressed " + k)
if (k === "42") { if (dial("101", 10)) return }
if (k !== "") { record({ box: "100", seconds: 3, beep: true }); hangup() }
`

func callScriptConfig(src string) config.Config {
	c := testConfig()
	c.FindExtension("100").Voicemail.Enabled = true
	c.Scripts = []config.Script{{ID: "cs", Name: "Front door", Language: "js", Mode: "call", Number: "850", Source: src,
		Fallback: config.Destination{Type: "extension", Target: "101"}}}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func TestCallScriptRingsSomeone(t *testing.T) {
	e, rec := startEngine(t, callScriptConfig(testCallScript))
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	d, code := ps[0].dial("850")
	if code != 200 {
		t.Fatalf("the script answered %d", code)
	}
	prompted(t, ps[0])
	var seq uint16 = 1
	d.press(&seq, "42")
	ring := ps[1].waitRing(patient(5 * time.Second))
	if ring == nil {
		t.Fatal("pressing 42 did not ring 101")
	}
	agent := ps[1].answer(ring)
	waitFor(t, "the caller to hear 101", func() bool {
		agent.sendRTP(5)
		return ps[0].gotRTP(200 * time.Millisecond)
	})
	run := scriptRun(t, runs)
	if run.Error != "" || run.Decision != "ended: answered at 101" || run.Log[0] != "pressed 42" ||
		strings.Join(run.Steps, "|") != "play beep|collect up to 2 keys|dial 101" {
		t.Fatalf("run %+v", run)
	}
	ps[0].send(d.request("BYE"))
	if r := rec.last(t); r.Disposition != "answered" {
		t.Fatalf("%+v", r)
	}
}

func TestCallScriptRecordsAMessage(t *testing.T) {
	st, err := storetest.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, callScriptConfig(testCallScript), func(e *Engine) { e.Mail = st })
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	d, code := ps[0].dial("850")
	if code != 200 {
		t.Fatalf("%d", code)
	}
	prompted(t, ps[0])
	var seq uint16 = 1
	d.press(&seq, "7")
	prompted(t, ps[0]) // the beep before the recording
	ps[0].drainRTP()
	d.speak(100, &seq, 0) // two seconds of speech
	ps[0].waitBye(t, patient(10*time.Second))
	run := scriptRun(t, runs)
	if run.Decision != "ended: the script hung up" || strings.Join(run.Steps, "|") != "play beep|collect up to 2 keys|record into mailbox 100|hang up" {
		t.Fatalf("run %+v", run)
	}
	waitFor(t, "the message", func() bool {
		vs, _ := st.Voicemails("100")
		return len(vs) == 1 && vs[0].Seconds >= 1
	})
}

func TestCallScriptFallsBack(t *testing.T) {
	// Pressing nothing: the script ends without a word, and its fallback (101) rings.
	e, _ := startEngine(t, callScriptConfig(testCallScript))
	ps := registered(t, e, "100", "101")
	if _, code := ps[0].dial("850"); code != 200 {
		t.Fatalf("%d", code)
	}
	if ps[1].waitRing(patient(8*time.Second)) == nil {
		t.Fatal("the fallback did not ring")
	}
	// A broken script: the same.
	e2, _ := startEngine(t, callScriptConfig(`play("beep"); while (true) {}`))
	ps2 := registered(t, e2, "100", "101")
	runs := e2.Bus.Subscribe()
	defer e2.Bus.Unsubscribe(runs)
	if _, code := ps2[0].dial("850"); code != 200 {
		t.Fatalf("%d", code)
	}
	if ps2[1].waitRing(patient(5*time.Second)) == nil {
		t.Fatal("the fallback did not ring after the script was stopped")
	}
	if run := scriptRun(t, runs); !strings.Contains(run.Error, "took too long") || !strings.HasPrefix(run.Decision, "fallback") {
		t.Fatalf("run %+v", run)
	}
}

// scriptRun waits for the next call script's outcome on the bus.
func scriptRun(t *testing.T, sub *events.Sub) CallScriptRun {
	t.Helper()
	end := time.After(patient(10 * time.Second))
	for {
		select {
		case ev := <-sub.C:
			if r, ok := ev.Data.(CallScriptRun); ok {
				return r
			}
		case <-end:
			t.Fatal("no call script run")
		}
	}
}
