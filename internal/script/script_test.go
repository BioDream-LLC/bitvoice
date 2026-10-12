package script

import (
	"strings"
	"testing"
	"time"
)

var call = Call{From: "2125550100", To: "100", Direction: "inbound", Weekday: 2, Hour: 20}

func TestJavaScript(t *testing.T) {
	d, err := Route("js", `log("from " + call.from); if (call.hour >= 18) return {type: "voicemail", target: "100"}`, call)
	if err != nil || d.Type != "voicemail" || d.Target != "100" || len(d.Log) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	if d, err := Route("js", `if (call.hour < 9) return {type: "hangup"}`, call); err != nil || d.Type != "" {
		t.Fatalf("no opinion: %+v %v", d, err)
	}
}

func TestLua(t *testing.T) {
	d, err := Route("lua", `if string.sub(call.from, 1, 3) == "212" then return {type="ringgroup", target="600"} end`, call)
	if err != nil || d.Type != "ringgroup" || d.Target != "600" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestSandboxAndLimits(t *testing.T) {
	for lang, src := range map[string]string{
		"js":  `while (true) {}`,
		"lua": `while true do end`,
	} {
		start := time.Now()
		_, err := Route(lang, src, call)
		if err != ErrTimeout || time.Since(start) > time.Second {
			t.Fatalf("%s: endless loop gave %v after %v", lang, err, time.Since(start))
		}
	}
	for lang, src := range map[string]string{
		"js":  `return require("fs")`,
		"lua": `return dofile("/etc/passwd")`,
	} {
		if _, err := Route(lang, src, call); err == nil {
			t.Fatalf("%s: sandbox let %q run", lang, src)
		}
	}
	if _, err := Route("lua", `return io.open("/etc/passwd")`, call); err == nil {
		t.Fatal("lua io reachable")
	}
	if err := Check("js", "return {"); err == nil || !strings.Contains(err.Error(), "") {
		t.Fatal("syntax error not reported")
	}
	if err := Check("lua", "return {type=}"); err == nil {
		t.Fatal("lua syntax error not reported")
	}
}

func TestErrorLinesAreTheScripts(t *testing.T) {
	err := Check("js", "let a = 1\nreturn {{ bad")
	if err == nil || !strings.Contains(err.Error(), "Line 2:") {
		t.Fatalf("compile error: %v", err)
	}
	_, err = Route("js", "let a = 1\nlet b = null\nreturn b.x", Call{})
	if err == nil || !strings.Contains(err.Error(), "(line 3)") {
		t.Fatalf("runtime error: %v", err)
	}
}
