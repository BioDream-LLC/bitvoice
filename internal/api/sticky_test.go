package api

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestLastAgent(t *testing.T) {
	s, _ := newServer(t)
	now := time.Now().UTC()
	// One in the records before the first ask, one after.
	s.Store.AddCDR(store.CDR{ID: "a", Start: now.Add(-48 * time.Hour), Answer: now.Add(-48 * time.Hour), Direction: "inbound", From: "+12125550100", AnsweredExt: "101"})
	if got := s.LastAgent("2125550100", now.AddDate(0, 0, -30)); got != "101" {
		t.Fatalf("from the records: %q", got)
	}
	s.noteAnswered(store.CDR{Direction: "inbound", From: "12125550100", Answer: now, AnsweredExt: "102"})
	s.noteAnswered(store.CDR{Direction: "inbound", From: "12125550100", Answer: now.Add(-time.Hour), AnsweredExt: "103"}) // older: ignored
	if got := s.LastAgent("+1 212 555 0100", now.AddDate(0, 0, -30)); got != "102" {
		t.Fatalf("latest: %q", got)
	}
	if got := s.LastAgent("+12125550100", now.Add(time.Minute)); got != "" {
		t.Fatalf("too old: %q", got)
	}
}
