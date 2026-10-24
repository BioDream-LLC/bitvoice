package pbx

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/agi"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
)

func agiConfig(address string, eagi bool) config.Config {
	c := testConfig()
	c.System.Compat.AGI = []config.AGIScript{{Name: "ivr", Address: address, Number: "860", EAGI: eagi, Args: []string{"sales"},
		Fallback: config.Destination{Type: "extension", Target: "100"}}}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func agiRun(t *testing.T, sub *events.Sub) AGIRun {
	t.Helper()
	end := time.After(patient(15 * time.Second))
	for {
		select {
		case ev := <-sub.C:
			if r, ok := ev.Data.(AGIRun); ok {
				return r
			}
		case <-end:
			t.Fatal("no AGI run")
		}
	}
}

// fastAGI is a FastAGI server for the tests: it runs script on the first connection.
func fastAGI(t *testing.T, script func(env map[string]string, cmd func(string) string)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		env := map[string]string{}
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			k, v, _ := strings.Cut(line, ": ")
			env[k] = v
		}
		script(env, func(cmd string) string {
			c.SetDeadline(time.Now().Add(patient(20 * time.Second)))
			if _, err := c.Write([]byte(cmd + "\n")); err != nil {
				return ""
			}
			line, _ := r.ReadString('\n')
			return strings.TrimSpace(line)
		})
	}()
	return ln.Addr().String()
}

func agiEngine(t *testing.T, cfg config.Config) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "agi-bin")
	os.MkdirAll(bin, 0o755)
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.AGI = &AGIHost{Library: &agi.Library{Dir: filepath.Join(dir, "agi")}, DB: &agi.DB{Path: filepath.Join(dir, "agi", "db.json")},
			Programs: bin, Version: "BitVoice test"}
	})
	return e, bin
}

// A FastAGI script runs the call: it hears the caller's keys, and the number it chooses rings.
func TestAGIFastAGIRunsTheCall(t *testing.T) {
	answers := make(chan []string, 1)
	var env map[string]string
	addr := fastAGI(t, func(e map[string]string, cmd func(string) string) {
		env = e
		var got []string
		got = append(got, cmd("ANSWER"))
		got = append(got, cmd(`STREAM FILE beep "12"`))
		got = append(got, cmd("GET DATA beep 3000 2"))
		got = append(got, cmd("DATABASE PUT test caller ${CALLERID(num)}"))
		got = append(got, cmd("SET EXTENSION 101"))
		answers <- got
	})
	e, _ := agiEngine(t, agiConfig("agi://"+addr+"/ivr?lang=en", false))
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	d, code := ps[0].dial("860")
	if code != 200 {
		t.Fatalf("the AGI script answered %d", code)
	}
	prompted(t, ps[0]) // the first beep
	var seq uint16 = 1
	d.press(&seq, "1")
	prompted(t, ps[0]) // the GET DATA beep
	d.press(&seq, "42")
	if ring := ps[1].waitRing(patient(10 * time.Second)); ring == nil {
		t.Fatal("the number the script chose did not ring")
	}
	got := <-answers
	want := []string{"200 result=0", "200 result=49 endpos=", "200 result=42", "200 result=1", "200 result=0"}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("answer %d: %q, want %q (all %q)", i, got[i], want[i], got)
		}
	}
	if env["agi_network"] != "yes" || env["agi_network_script"] != "ivr?lang=en" || env["agi_callerid"] != "100" ||
		env["agi_extension"] != "860" || env["agi_arg_1"] != "sales" || env["agi_version"] != "BitVoice test" {
		t.Fatalf("variables %v", env)
	}
	run := agiRun(t, runs)
	if run.Decision != "to 101" || run.Commands != 5 || run.Error != "" {
		t.Fatalf("run %+v", run)
	}
	if v, _ := e.AGI.DB.Get("test", "caller"); v != "${CALLERID(num)}" {
		// AGI does not expand variables in DATABASE PUT, as in Asterisk.
		t.Fatalf("database %q", v)
	}
}

// A server that cannot be reached sends the call to the fallback; a caller who hangs up is told to
// the script as HANGUP.
func TestAGIUnreachableAndHangup(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	e, _ := agiEngine(t, agiConfig("agi://"+dead+"/ivr", false))
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	if _, code := ps[1].dial("860"); code != 200 {
		t.Fatalf("%d", code)
	}
	if ps[0].waitRing(patient(10*time.Second)) == nil {
		t.Fatal("the fallback did not ring")
	}
	if run := agiRun(t, runs); !strings.Contains(run.Error, "could not reach") || !strings.HasPrefix(run.Decision, "fallback") {
		t.Fatalf("run %+v", run)
	}

	heard := make(chan string, 1)
	addr := fastAGI(t, func(_ map[string]string, cmd func(string) string) {
		res := cmd("WAIT FOR DIGIT -1")
		heard <- res + "|" + cmd("NOOP")
	})
	e2, _ := agiEngine(t, agiConfig("agi://"+addr+"/ivr", false))
	ps2 := registered(t, e2, "100", "101")
	runs2 := e2.Bus.Subscribe()
	defer e2.Bus.Unsubscribe(runs2)
	d, code := ps2[0].dial("860")
	if code != 200 {
		t.Fatalf("%d", code)
	}
	time.Sleep(300 * time.Millisecond)
	ps2[0].send(d.request("BYE"))
	select {
	case h := <-heard:
		// The wait ends with -1, then the script hears HANGUP, and NOOP still works.
		if h != "200 result=-1|HANGUP" {
			t.Fatalf("the script heard %q", h)
		}
	case <-time.After(patient(10 * time.Second)):
		t.Fatal("the script never heard the hang-up")
	}
	if run := agiRun(t, runs2); run.Decision != "ended: the call ended" {
		t.Fatalf("run %+v", run)
	}
}

// A program in the AGI programs folder runs with EAGI: it reads the caller's audio on descriptor 3.
func TestAGIProgramWithEAGI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("EAGI needs Linux or macOS")
	}
	e, bin := agiEngine(t, agiConfig("listen.sh", true))
	prog := `#!/bin/sh
while read line; do [ -z "$line" ] && break; done
head -c 3200 <&3 > heard.raw
echo "VERBOSE \"heard $(wc -c < heard.raw | tr -d ' ') bytes\" 1"; read r
echo "SET EXTENSION 101"; read r
`
	if err := os.WriteFile(filepath.Join(bin, "listen.sh"), []byte(prog), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	d, code := ps[0].dial("860")
	if code != 200 {
		t.Fatalf("%d", code)
	}
	var seq uint16 = 1
	d.speak(25, &seq, 0) // half a second of speech: 4,000 samples
	if ps[1].waitRing(patient(10*time.Second)) == nil {
		t.Fatal("the number the program chose did not ring")
	}
	if run := agiRun(t, runs); run.Decision != "to 101" || len(run.Log) != 1 || run.Log[0] != "heard 3200 bytes" {
		t.Fatalf("run %+v", run)
	}
}

// A FastAGI server written with pyst2, a widely used AGI library (~/.bvtest/agi/pyst):
// BITVOICE_TEST_PYST2=127.0.0.1:4574 runs it. It plays a beep, reads digits, keeps them in AstDB and
// sends the call to the extension the caller typed.
func TestAGIWithPyst2(t *testing.T) {
	addr := os.Getenv("BITVOICE_TEST_PYST2")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_PYST2 to the pyst2 FastAGI server's address")
	}
	e, _ := agiEngine(t, agiConfig("agi://"+addr+"/", false))
	ps := registered(t, e, "100", "101")
	runs := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(runs)
	d, code := ps[0].dial("860")
	if code != 200 {
		t.Fatalf("%d", code)
	}
	prompted(t, ps[0]) // STREAM FILE beep, then GET DATA's beep right after it
	// Keys pressed during the first beep would be dropped (it escapes on none): press once both ended,
	// while GET DATA waits for the digits.
	waitFor(t, "the beeps to end", func() bool { return !receivedLoud(ps[0].rtp, 150*time.Millisecond) })
	var seq uint16 = 1
	d.press(&seq, "101")
	if ps[1].waitRing(patient(10*time.Second)) == nil {
		t.Fatal("the extension the caller typed did not ring")
	}
	run := agiRun(t, runs)
	if run.Decision != "to 101" || len(run.Log) != 2 || run.Log[0] != "pyst2 says hello to 100" || run.Log[1] != "picked 101" {
		t.Fatalf("run %+v", run)
	}
	if v, _ := e.AGI.DB.Get("pyst", "last"); v != "101" {
		t.Fatalf("AstDB %q", v)
	}
}
