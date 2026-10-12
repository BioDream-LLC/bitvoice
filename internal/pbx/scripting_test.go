package pbx

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestScriptDestination(t *testing.T) {
	cfg := testConfig()
	cfg.Scripts = []config.Script{
		{ID: "vip", Name: "VIPs", Language: "js", Source: `if (call.from === "5550001") return {type: "echo"}`,
			Fallback: config.Destination{Type: "extension", Target: "101"}},
		{ID: "broken", Name: "Broken", Language: "lua", Source: `error("boom")`,
			Fallback: config.Destination{Type: "echo"}},
		{ID: "wrong", Name: "Wrong", Language: "js", Source: `return {type: "extension", target: "999"}`,
			Fallback: config.Destination{Type: "echo"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	vip := scriptCall{from: "5550001"}
	if p := e.resolveFor(&cfg, config.Destination{Type: "script", Target: "vip"}, 0, vip); p.local != "echo" {
		t.Fatalf("vip: %+v", p)
	}
	if p := e.resolveFor(&cfg, config.Destination{Type: "script", Target: "vip"}, 0, scriptCall{from: "1"}); p.toName != "Ext 101" {
		t.Fatalf("no opinion should fall back: %+v", p)
	}
	for _, id := range []string{"broken", "wrong"} {
		if p := e.resolveFor(&cfg, config.Destination{Type: "script", Target: id}, 0, vip); p.local != "echo" {
			t.Fatalf("%s should use its fallback: %+v", id, p)
		}
	}
	bad := cfg
	bad.Scripts = append([]config.Script(nil), cfg.Scripts...)
	bad.Scripts[0].Source = "return {"
	if err := bad.Validate(); err == nil {
		t.Fatal("a script that does not compile was accepted")
	}
}
