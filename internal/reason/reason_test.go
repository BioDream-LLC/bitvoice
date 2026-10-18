package reason

import (
	"strings"
	"testing"
)

// Every rulebook BitVoice ships is decided in every combination of its facts: never two choices at once, every
// conclusion, action and choice reachable.
func TestBooksCheckEverySituation(t *testing.T) {
	for _, b := range Books {
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		rep := b.Check()
		if rep.Situations != 1<<len(b.Facts()) {
			t.Errorf("%s: %d situations", b.ID, rep.Situations)
		}
		for _, p := range rep.Problems {
			t.Errorf("%s: %s %s in %v", b.ID, p.Kind, p.Says, p.Situation)
		}
	}
}

func facts(yes []string, no ...string) map[string]Truth {
	m := map[string]Truth{}
	for _, f := range yes {
		m[f] = Yes
	}
	for _, f := range no {
		m[f] = No
	}
	return m
}

func allNo(b *Book, yes ...string) map[string]Truth {
	m := map[string]Truth{}
	for _, f := range b.Facts() {
		m[f] = No
	}
	for _, f := range yes {
		m[f] = Yes
	}
	return m
}

func TestTrunkJudge(t *testing.T) {
	cases := []struct {
		yes  []string
		want string
	}{
		{nil, "use"},
		{[]string{"keepalive_failed", "other_way_out"}, "last"},
		{[]string{"keepalive_failed", "other_way_out", "call_ok_recently"}, "use"}, // calls get through: not trusted
		{[]string{"calls_failing", "other_way_out"}, "last"},                       // alive but refusing calls
		{[]string{"keepalive_failed", "calls_failing", "all_failing"}, "use"},      // our own link
		{[]string{"keepalive_failed"}, "use"},                                      // no other way out
	}
	for _, c := range cases {
		v := Trunk.Decide(allNo(Trunk, c.yes...))
		if v.Action != c.want {
			t.Errorf("%v: %s, want %s (%v)", c.yes, v.Action, c.want, v.Reasons)
		}
	}
	v := Trunk.Decide(allNo(Trunk, "keepalive_failed", "other_way_out", "call_ok_recently"))
	if !strings.Contains(strings.Join(v.Reasons, " "), "a failing keep-alive alone is not trusted") {
		t.Errorf("reasons %v", v.Reasons)
	}
}

func TestFraudJudge(t *testing.T) {
	cases := []struct {
		yes  []string
		want string
	}{
		{nil, "allow"},
		{[]string{"international"}, "allow"},
		{[]string{"international", "new_country"}, "alert"},
		{[]string{"international", "new_country", "after_hours"}, "challenge"},
		{[]string{"international", "new_country", "after_hours", "approved"}, "allow"},
		{[]string{"international", "burst", "approved"}, "block"}, // a stolen password looks approved
		{[]string{"international", "burst", "emergency"}, "allow"},
		{[]string{"international", "new_country", "attack_seen"}, "block"},
		{[]string{"international", "new_country", "allowed_country"}, "allow"},
	}
	for _, c := range cases {
		v := Fraud.Decide(allNo(Fraud, c.yes...))
		if v.Action != c.want {
			t.Errorf("%v: %s, want %s (%v)", c.yes, v.Action, c.want, v.Reasons)
		}
	}
}

// A fact nobody measured that could change the answer makes the judge cautious, and says which.
func TestUnknownIsNotSafety(t *testing.T) {
	m := allNo(Fraud, "international", "new_country")
	delete(m, "after_hours")
	v := Fraud.Decide(m)
	if v.Action != "challenge" || v.How != "cautious" || len(v.Unknown) == 0 || v.Unknown[0] != "after_hours" {
		t.Fatalf("%+v", v)
	}
	// Measured absent, the same fact is listed as what the answer relied on.
	v = Fraud.Decide(allNo(Fraud, "international", "new_country"))
	if v.Action != "alert" || !contains(v.Assumed, "after_hours") {
		t.Fatalf("%+v", v)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestQualityJudge(t *testing.T) {
	cases := []struct {
		yes  []string
		want string
	}{
		{nil, "ok"},
		{[]string{"loss_high"}, "here"},
		{[]string{"loss_high", "other_side_clean", "everywhere_lossy"}, "here"},
		{[]string{"loss_high", "everywhere_lossy"}, "shared"},
		{[]string{"loss_high", "other_side_lossy"}, "shared"},
		{[]string{"jitter_high", "poor_many"}, "jitter"},
		{[]string{"loss_high", "few_calls"}, "idle"},
	}
	for _, c := range cases {
		if v := Quality.Decide(allNo(Quality, c.yes...)); v.Action != c.want {
			t.Errorf("%v: %s, want %s (%v)", c.yes, v.Action, c.want, v.Reasons)
		}
	}
}

func TestLocationJudge(t *testing.T) {
	cases := []struct {
		yes  []string
		want string
	}{
		{nil, "new"},
		{[]string{"old_measured", "old_precise"}, "old"},
		{[]string{"old_measured", "old_precise", "old_stale"}, "new"},
		{[]string{"old_measured", "old_precise", "new_measured", "new_precise"}, "new"},
		{[]string{"far_apart"}, "both"},
	}
	for _, c := range cases {
		if v := Location.Decide(allNo(Location, c.yes...)); v.Action != c.want {
			t.Errorf("%v: %s, want %s (%v)", c.yes, v.Action, c.want, v.Reasons)
		}
	}
}

// A rulebook that gives two answers at once is caught by Check, with the situation that shows it.
func TestCheckFindsConflicts(t *testing.T) {
	b := &Book{ID: "bad", Rules: []Rule{
		{Name: "a", Kind: Fact, Text: "a"}, {Name: "b", Kind: Fact, Text: "b"},
		{Name: "x", Kind: Action, Does: "go", Text: "x", Needs: []string{"a"}},
		{Name: "y", Kind: Action, Does: "stop", Text: "y", Needs: []string{"b"}},
	}, Choices: []Choice{{Does: "go"}, {Does: "stop"}}, Cautious: "stop", Default: "go"}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	rep := b.Check()
	if len(rep.Problems) != 1 || rep.Problems[0].Kind != "conflict" || strings.Join(rep.Problems[0].Situation, ",") != "a,b" {
		t.Fatalf("%+v", rep)
	}
	if v := b.Decide(facts([]string{"a", "b"})); v.Action != "stop" || v.How != "cautious" {
		t.Fatalf("%+v", v)
	}
}
