package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// slowHost answers like a call: each step takes a little while, and the caller may hang up.
type slowHost struct {
	Simulator
	hungUpAfter int // steps before the caller hangs up (0: never)
	n           int
	transferred string
}

func (h *slowHost) tick() error {
	h.n++
	time.Sleep(2 * time.Millisecond)
	if h.hungUpAfter > 0 && h.n >= h.hungUpAfter {
		return ErrCallEnded
	}
	return nil
}
func (h *slowHost) Play(n string) (string, error) { return "", h.tick() }
func (h *slowHost) Say(t string) (string, error)  { return "", h.tick() }
func (h *slowHost) Collect(o CollectOptions) (string, error) {
	if err := h.tick(); err != nil {
		return "", err
	}
	return h.Simulator.Collect(o)
}
func (h *slowHost) Transfer(d Destination) error { h.transferred = d.String(); return nil }

const menuJS = `
log("caller " + call.from)
play("welcome")
for (let i = 0; i < 3; i++) {
  const k = collect({ max: 1, timeout: 5, say: "Press 1 for sales or 2 for support" })
  if (k === "1") return { type: "ringgroup", target: "600" }
  if (k === "2") {
    if (dial("101", 20)) return
    record({ box: "101" })
    hangup()
  }
  say("Sorry, I did not get that")
}
transfer("100")
`

const menuLua = `
log("caller " .. call.from)
play("welcome")
for i = 1, 3 do
  local k = collect{ max = 1, timeout = 5, say = "Press 1 for sales or 2 for support" }
  if k == "1" then return { type = "ringgroup", target = "600" } end
  if k == "2" then
    if dial("101", 20) then return end
    record{ box = "101" }
    hangup()
  end
  say("Sorry, I did not get that")
end
transfer("100")
`

func TestCallScriptPaths(t *testing.T) {
	for lang, src := range map[string]string{"js": menuJS, "lua": menuLua} {
		t.Run(lang, func(t *testing.T) {
			c := Call{From: "2125550100", To: "800"}
			// 1: the script returns where the call goes.
			out, err := RunCall(lang, src, c, &Simulator{Keys: []string{"1"}}, 0)
			if err != nil || out.Ended || out.Next == nil || out.Next.String() != "ringgroup 600" || out.Log[0] != "caller 2125550100" {
				t.Fatalf("1: %+v %v", out, err)
			}
			// 2, nobody answers: a message, then the script hangs up.
			out, err = RunCall(lang, src, c, &Simulator{Keys: []string{"2"}}, 0)
			if err != nil || !out.Ended || out.Next != nil || strings.Join(out.Steps, "|") !=
				`play welcome|collect up to 1 keys|dial 101|record into mailbox 101|hang up` {
				t.Fatalf("2: %+v %v", out, err)
			}
			// 2, answered: the call is someone's; the script stops there, nothing wrong.
			out, err = RunCall(lang, src, c, &Simulator{Keys: []string{"2"}, Answer: true}, 0)
			if err != nil || !out.Ended || len(out.Steps) != 3 {
				t.Fatalf("2 answered: %+v %v", out, err)
			}
			// Nothing pressed three times: handed to 100.
			h := &slowHost{}
			out, err = RunCall(lang, src, c, h, 0)
			if err != nil || !out.Ended || h.transferred != "100" {
				t.Fatalf("no key: %+v %v %q", out, err, h.transferred)
			}
			// The caller hangs up in the middle: the script stops at once, and that is no failure.
			h = &slowHost{hungUpAfter: 3}
			out, err = RunCall(lang, src, c, h, 0)
			if err != nil || !out.Ended || h.n != 3 {
				t.Fatalf("hung up: %+v %v steps %d", out, err, h.n)
			}
		})
	}
}

func TestCallScriptCannotCarryOnAfterTheCallEnds(t *testing.T) {
	// A script that catches the error and loops on must still stop when the caller has gone.
	js := `while (true) { try { play("x") } catch (e) { log("caught " + e) } }`
	lua := `while true do pcall(play, "x") end`
	for lang, src := range map[string]string{"js": js, "lua": lua} {
		h := &slowHost{hungUpAfter: 2}
		start := time.Now()
		out, err := RunCall(lang, src, Call{}, h, time.Minute)
		if err != nil || !out.Ended || h.n > 3 || time.Since(start) > 2*time.Second {
			t.Fatalf("%s: %+v %v after %d steps", lang, out, err, h.n)
		}
	}
}

func TestCallScriptLimits(t *testing.T) {
	for lang, src := range map[string]string{"js": `play("a"); while (true) {}`, "lua": `play("a") while true do end`} {
		out, err := RunCall(lang, src, Call{}, &Simulator{}, time.Minute)
		if !errors.Is(err, ErrTimeout) || out.Ended {
			t.Fatalf("%s busy loop: %+v %v", lang, out, err)
		}
	}
	for lang, src := range map[string]string{"js": `while (true) { wait(0) }`, "lua": `while true do wait(0) end`} {
		_, err := RunCall(lang, src, Call{}, &Simulator{}, time.Minute)
		if !errors.Is(err, ErrTooManySteps) {
			t.Fatalf("%s endless steps: %v", lang, err)
		}
	}
	// The whole run: a script waiting on the caller is stopped at its owner's limit.
	h := &waitHost{}
	_, err := RunCall("js", `while (true) { wait(1) }`, Call{}, h, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ran longer than it may") {
		t.Fatalf("overrun: %v", err)
	}
	// Mistakes are errors with line numbers: the call goes to the fallback.
	if _, err := RunCall("js", "play(\"a\")\nnope()", Call{}, &Simulator{}, 0); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("js error: %v", err)
	}
	if _, err := RunCall("js", `dial()`, Call{}, &Simulator{}, 0); err == nil || !strings.Contains(err.Error(), "needs a destination") {
		t.Fatalf("dial(): %v", err)
	}
	// A destination that is not there is an error the script may catch.
	sim := &Simulator{Exists: func(d Destination) bool { return d.Target != "999" }}
	out, err := RunCall("js", `try { transfer("999") } catch (e) { log("no 999") ; transfer("100") }`, Call{}, sim, 0)
	if err != nil || !out.Ended || out.Log[0] != "no 999" {
		t.Fatalf("missing destination: %+v %v", out, err)
	}
	// No file, network or module access in either language.
	for lang, src := range map[string]string{"js": `require("fs")`, "lua": `require("os")`} {
		if _, err := RunCall(lang, src, Call{}, &Simulator{}, 0); err == nil {
			t.Fatalf("%s: require worked", lang)
		}
	}
}

// waitHost really waits, as a call does.
type waitHost struct{ Simulator }

func (h *waitHost) Wait(s float64) (string, error) {
	time.Sleep(time.Duration(s * float64(time.Second)))
	return "", nil
}
