package api

import (
	"fmt"
	"strings"
	"testing"
)

// The API refuses an AI receptionist whose way to a person comes back to an assistant, whichever
// change closes the loop: the receptionist itself, or a menu edited afterwards.
func TestReceptionistLoopRefusedByAPI(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, out := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	code, out := c.do("POST", "/api/receptionists", map[string]any{"name": "Ava", "person": map[string]any{"type": "extension", "target": "100"}})
	if code != 201 {
		t.Fatalf("receptionist: %d %v", code, out)
	}
	ai := out["item"].(map[string]any)["id"].(string)
	// A receptionist pointing at itself.
	code, out = c.do("PUT", "/api/receptionists/"+ai, map[string]any{"name": "Ava", "person": map[string]any{"type": "receptionist", "target": ai}})
	if code != 422 && code != 400 {
		t.Fatalf("self loop accepted: %d %v", code, out)
	}
	// A menu whose no-key timeout sends callers back to it, then the receptionist pointed at the menu.
	code, out = c.do("POST", "/api/menus", map[string]any{"name": "Main", "timeoutSeconds": 5, "repeats": 1,
		"timeout": map[string]any{"type": "receptionist", "target": ai}})
	if code != 201 {
		t.Fatalf("menu: %d %v", code, out)
	}
	menu := out["item"].(map[string]any)["id"].(string)
	code, out = c.do("PUT", "/api/receptionists/"+ai, map[string]any{"name": "Ava", "person": map[string]any{"type": "menu", "target": menu}})
	if code < 400 || !strings.Contains(strings.ToLower(toString(out)), "come back to an ai receptionist") {
		t.Fatalf("loop through a menu accepted: %d %v", code, out)
	}
}

func toString(m map[string]any) string { return fmt.Sprint(m) }
