package api

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestTimeline(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	now := time.Now().UTC()
	s.Store.AddCDR(store.CDR{ID: "c1", Start: now.Add(-3 * time.Hour), Direction: "inbound", From: "+12125550199", To: "+12125550100", AnsweredBy: "Front desk", Disposition: "answered", Billsec: 95})
	s.Store.AddCDR(store.CDR{ID: "c2", Start: now.Add(-2 * time.Hour), Direction: "outbound", From: "100", FromName: "Front desk", To: "2125550199", Disposition: "no-answer"})
	s.Store.AddCDR(store.CDR{ID: "c3", Start: now.Add(-time.Hour), Direction: "inbound", From: "+13125550000", Disposition: "answered"}) // someone else
	// The text, the callback and the note are stamped with the time they are added: apart by more than
	// the clock's step (about a millisecond on Windows), so their newest-first order is defined.
	s.Store.AddText(store.Text{Direction: "in", Our: "+12125550100", Other: "+12125550199", Body: "Can you call me back?", Status: "received"})
	time.Sleep(20 * time.Millisecond)
	s.Store.AddCallback(store.Callback{Name: "Lee", Number: "+1 212 555 0199", Reason: "invoice", When: "after 3"})
	time.Sleep(20 * time.Millisecond)
	if code, _ := admin.do("POST", "/api/timeline/notes", map[string]any{"number": "+12125550199", "text": ""}); code != 422 {
		t.Fatal("empty note")
	}
	if code, _ := admin.do("POST", "/api/timeline/notes", map[string]any{"number": "+12125550199", "text": "Prefers mornings."}); code != 201 {
		t.Fatal("note")
	}
	code, tl := admin.do("GET", "/api/timeline?number=2125550199", nil)
	if code != 200 {
		t.Fatalf("timeline %d", code)
	}
	items := tl["items"].([]any)
	var kinds, titles []string
	for _, x := range items {
		m := x.(map[string]any)
		kinds = append(kinds, m["kind"].(string))
		titles = append(titles, m["title"].(string))
	}
	got := strings.Join(kinds, ",")
	if len(items) != 5 || !strings.HasPrefix(got, "note,") || !strings.Contains(got, "text") || !strings.Contains(got, "callback") || strings.Count(got, "call,")+strings.Count(got, ",call") < 2 {
		t.Fatalf("timeline %s: %v", got, titles)
	}
	if !strings.Contains(strings.Join(titles, "|"), "Called; answered by Front desk (1 min 35 s)") || !strings.Contains(strings.Join(titles, "|"), "Front desk called them: no answer") {
		t.Fatalf("titles %v", titles)
	}
	if code, _ := admin.do("GET", "/api/timeline?number=1", nil); code != 400 {
		t.Fatal("no number")
	}
}
