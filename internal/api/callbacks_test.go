package api

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// A receptionist's callback appears in the list; staff book, close, reopen and delete them, and bad
// input is refused field by field.
func TestCallbacks(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	if err := s.BookCallback(store.Callback{Receptionist: "Front", Name: "Ana", Number: "+12125550123", When: "today", Reason: "a quote"}); err != nil {
		t.Fatal(err)
	}
	code, out := admin.do("GET", "/api/callbacks", nil)
	items, _ := out["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["status"] != "open" {
		t.Fatalf("list %d %v", code, out)
	}
	id := items[0].(map[string]any)["id"].(string)
	if code, out := admin.do("POST", "/api/callbacks", map[string]any{"name": "Bo", "number": "call me"}); code != 422 || !hasProblem(out, "number") {
		t.Fatalf("bad number accepted: %d %v", code, out)
	}
	if code, _ := admin.do("POST", "/api/callbacks", map[string]any{"name": "Bo", "number": "+442071234567"}); code != 201 {
		t.Fatalf("book %d", code)
	}
	if code, out := admin.do("PUT", "/api/callbacks/"+id, map[string]any{"status": "done", "note": "quoted"}); code != 200 || out["by"] != "owner" || out["closed"] == nil {
		t.Fatalf("done %d %v", code, out)
	}
	if code, out := admin.do("PUT", "/api/callbacks/"+id, map[string]any{"status": "open"}); code != 200 || out["by"] != nil {
		t.Fatalf("reopen %d %v", code, out)
	}
	if code, out := admin.do("PUT", "/api/callbacks/"+id, map[string]any{"status": "maybe"}); code != 422 || !hasProblem(out, "status") {
		t.Fatalf("bad status %d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/callbacks/"+id+"/call", map[string]any{"extension": "999"}); code != 422 || !hasProblem(out, "extension") {
		t.Fatalf("ring back with no extension %d %v", code, out)
	}
	if code, _ := admin.do("DELETE", "/api/callbacks/"+id, nil); code != 204 {
		t.Fatalf("delete %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/callbacks/"+id, nil); code != 404 {
		t.Fatalf("delete twice %d", code)
	}
}

// hasProblem is true when a 422 answer names field.
func hasProblem(out map[string]any, field string) bool {
	ps, _ := out["problems"].([]any)
	for _, p := range ps {
		if m, _ := p.(map[string]any); m["field"] == field {
			return true
		}
	}
	return false
}
