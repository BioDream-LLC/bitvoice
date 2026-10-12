package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegalHoldsAndRetention(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -100).UTC()
	for _, r := range []CDR{
		{ID: "a", Start: old, From: "100", To: "5551234", Recording: "x/a"},
		{ID: "b", Start: old, From: "101", To: "102", Recording: "x/b"},
		{ID: "c", Start: time.Now().UTC(), From: "101", To: "102"},
	} {
		if err := s.AddCDR(r); err != nil {
			t.Fatal(err)
		}
	}
	// Two old recordings, by the names their call records give.
	for _, name := range []string{"a", "b"} {
		recName, _ := s.AddRecording(name, old, []byte("RIFF"))
		p := filepath.Join(s.dir, "recordings", recName+".wav")
		_ = os.Chtimes(p, old, old)
		s.rewriteRecordingName(t, name, recName)
	}

	h, err := s.PutHold(Hold{ID: "h1", Name: "Smith v Acme", Numbers: []string{"5551234"}, By: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if n := s.PruneCDRs(30); n != 1 {
		t.Fatalf("pruned %d call records, want 1 (the held one stays)", n)
	}
	recs, _ := s.CDRs(CDRQuery{})
	if len(recs) != 2 || !s.CDRHeld(recs[len(recs)-1]) {
		t.Fatalf("records left %+v", recs)
	}
	if n := s.PruneRecordings(30); n != 1 {
		t.Fatalf("pruned %d recordings, want 1", n)
	}
	held := recs[len(recs)-1].Recording
	if err := s.DeleteRecording(held); !errors.Is(err, ErrHeld) {
		t.Fatalf("deleting a held recording: %v", err)
	}
	if err := s.ReleaseHold(h.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if n := s.PruneCDRs(30); n != 1 {
		t.Fatalf("after release pruned %d", n)
	}
	if hs := s.Holds(); len(hs) != 1 || hs[0].Released.IsZero() || hs[0].FreedBy != "admin" {
		t.Fatalf("holds %+v", hs)
	}
	if _, err := s.PutHold(Hold{ID: "h1", Name: "changed"}); err == nil {
		t.Fatal("a released hold was changed")
	}
}

// rewriteRecordingName points a test call record at the recording name the store chose.
func (s *Store) rewriteRecordingName(t *testing.T, id, name string) {
	t.Helper()
	var r CDR
	_ = s.cdrRows(`SELECT body FROM cdrs WHERE id = ?`, []any{id}, func(c CDR) bool { r = c; return false })
	r.Recording = name
	if err := s.AddCDR(r); err != nil {
		t.Fatal(err)
	}
}
