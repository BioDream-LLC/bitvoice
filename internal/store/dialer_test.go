package store

import (
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
