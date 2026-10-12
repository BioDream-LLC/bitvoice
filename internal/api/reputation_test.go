package api

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestJudgeReputation(t *testing.T) {
	cases := []struct {
		r    NumberReputation
		want string
		why  string
	}{
		{NumberReputation{Calls: 5, Answered: 1, PrevRate: -1}, "few", "too few"},
		{NumberReputation{Calls: 50, Answered: 30, Short: 2, PrevRate: -1}, "good", "60% answered"},
		{NumberReputation{Calls: 50, Answered: 5, PrevRate: -1}, "watch", "spam likely"},
		{NumberReputation{Calls: 50, Answered: 20, Short: 10, PrevRate: -1}, "watch", "within 10 seconds"},
		{NumberReputation{Calls: 50, Answered: 12, PrevRate: 60}, "watch", "fell from 60% to 24%"},
		{NumberReputation{Calls: 3, Rejected: 1, PrevRate: -1}, "labelled", "608"},
		{NumberReputation{Calls: 400, Answered: 200, PeakDay: 300, PrevRate: -1}, "watch", "300 calls a day"},
	}
	for i, c := range cases {
		judgeReputation(&c.r)
		if c.r.Status != c.want || !strings.Contains(strings.Join(c.r.Why, " "), c.why) {
			t.Errorf("%d: %s %q, want %s with %q", i, c.r.Status, c.r.Why, c.want, c.why)
		}
	}
}

func TestReputationReport(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "brand", rev, func(c *config.Config) error {
		c.System.Brands = []config.Brand{{Number: "+1 212 555 0100", Name: "Acme Dental", Watch: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	for i := range 30 {
		r := store.CDR{ID: "o" + string(rune('a'+i)), Start: now, End: now.Add(time.Minute), Direction: "outbound", CallerID: "+12125550100", Disposition: "no-answer"}
		if i < 4 { // 4 of 30 answered, and two of those hung up at once
			r.Answer, r.Billsec, r.Disposition = now, 40, "answered"
			if i < 2 {
				r.Billsec = 3
			}
		}
		if i == 29 {
			r.Code = 608
		}
		if err := s.Store.AddCDR(r); err != nil {
			t.Fatal(err)
		}
	}
	_, out := admin.do("GET", "/api/reputation?days=7", nil)
	ns := out["numbers"].([]any)
	if len(ns) != 1 {
		t.Fatalf("numbers %v", out)
	}
	n := ns[0].(map[string]any)
	if n["status"] != "labelled" || n["brand"] != "Acme Dental" || n["calls"].(float64) != 30 || n["rejected"].(float64) != 1 {
		t.Fatalf("number %v", n)
	}
	if p := s.reputationProblems(); len(p) != 1 {
		t.Fatalf("health %v", p)
	}
	bad := config.Default()
	bad.System.Brands = []config.Brand{{Number: "12"}, {Number: "+12125550100", Logo: "http://x/logo.png", Name: strings.Repeat("x", 40)}}
	err := bad.Validate()
	for _, f := range []string{"brands[0].number", "brands[1].logo", "brands[1].name"} {
		if err == nil || !strings.Contains(err.Error(), f) {
			t.Errorf("validation missing %s: %v", f, err)
		}
	}
}
