package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDialerContactsAndDNC(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cs := []DialContact{
		{ID: "1", Campaign: "k", Number: "3125550001", State: "pending"},
		{ID: "2", Campaign: "k", Number: "3125550002", State: "retry", Next: now.Add(time.Hour)},
		{ID: "3", Campaign: "k", Number: "3125550003", State: "retry", Next: now.Add(-time.Minute)},
		{ID: "4", Campaign: "other", Number: "3125550004", State: "pending"},
	}
	if err := s.AddDialContacts(cs); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueDialContacts("k", now, 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("due %v %v", due, err)
	}
	c := due[0]
	c.State, c.Result = "done", "answered"
	if err := s.PutDialContact(c); err != nil {
		t.Fatal(err)
	}
	states, results, err := s.DialContactCounts("k")
	if err != nil || states["done"] != 1 || states["retry"] != 2 || results["answered"] != 1 {
		t.Fatalf("%v %v %v", states, results, err)
	}
	if err := s.AddDNC("test", "+1 (312) 555-0002"); err != nil {
		t.Fatal(err)
	}
	if !s.OnDNC("3125550002") || !s.OnDNC("13125550002") || s.OnDNC("3125550001") {
		t.Fatal("do-not-call lookups")
	}
	list, _ := s.DNC(10)
	if len(list) != 1 || list[0].Number != "3125550002" {
		t.Fatalf("list %+v", list)
	}
	_ = s.RemoveDNC("3125550002")
	if s.OnDNC("3125550002") {
		t.Fatal("not removed")
	}
	c2, _ := s.DialContactByID("k", "2")
	c2.State = "dialling"
	_ = s.PutDialContact(c2)
	_ = s.ResetDialling()
	if c2, _ = s.DialContactByID("k", "2"); c2.State != "retry" {
		t.Fatalf("left dialling: %+v", c2)
	}
	_ = s.DeleteDialContacts("k", "")
	if all, _ := s.DialContacts("k", "", 0, 100); len(all) != 0 {
		t.Fatal("not deleted")
	}
}

// The national registry's file: the FTC's "area code,number" lines (a heading and a bad line
// skipped), imported in batches; a new download replaces the last, and numbers added by hand stay.
func TestImportDNC(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddDNC("pressed 9", "3125559999"); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("AREA CODE,PHONE NUMBER\n")
	for i := 0; i < 12000; i++ {
		fmt.Fprintf(&b, "212,%07d\n", 5550000+i)
	}
	b.WriteString("212,55\n\n")
	n, skipped, err := s.ImportDNC(strings.NewReader(b.String()), "FTC", false)
	if err != nil || n != 12000 || skipped != 1 {
		t.Fatalf("%d %d %v", n, skipped, err)
	}
	if !s.OnDNC("+1 212 555 0001") || !s.OnDNC("3125559999") {
		t.Fatal("numbers missing")
	}
	if total, national := s.DNCCount(); total != 12001 || national != 12000 {
		t.Fatalf("%d %d", total, national)
	}
	// Next month's download: 2125550001 left the registry.
	if _, _, err := s.ImportDNC(strings.NewReader("212,5550000\n212,5550002\n"), "FTC", true); err != nil {
		t.Fatal(err)
	}
	if s.OnDNC("2125550001") || !s.OnDNC("2125550002") || !s.OnDNC("3125559999") {
		t.Fatal("the replace kept or lost the wrong numbers")
	}
	if total, national := s.DNCCount(); total != 3 || national != 2 {
		t.Fatalf("after the replace %d %d", total, national)
	}
}
