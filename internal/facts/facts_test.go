package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type call struct {
	Name  string `json:"name"`
	Lines []Line `json:"lines"`
	Facts []struct {
		Kind     string `json:"kind"`
		Value    string `json:"value"`
		Reported bool   `json:"reported"`
	} `json:"facts"`
}

// score runs a set: facts expected and found, expected in all, and facts found that no one
// expected (each, for the report).
func score(t *testing.T, set string) (found, expected, extra int, misses []string) {
	b, err := os.ReadFile("testdata/" + set + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var calls []call
	if err := json.Unmarshal(b, &calls); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		got := Extract(c.Lines)
		used := make([]bool, len(got))
		for _, w := range c.Facts {
			expected++
			ok := false
			for i, g := range got {
				if !used[i] && g.Kind == w.Kind && g.Value == w.Value && (!w.Reported || g.Reported) {
					used[i], ok = true, true
					break
				}
			}
			if ok {
				found++
			} else {
				misses = append(misses, fmt.Sprintf("%s: missed %s=%q", c.Name, w.Kind, w.Value))
			}
		}
		for i, g := range got {
			if !used[i] {
				extra++
				misses = append(misses, fmt.Sprintf("%s: extra %s=%q (%q)", c.Name, g.Kind, g.Value, g.Quote))
			}
		}
	}
	return
}

// The published coverage: the rules were written from the training set; the hold-out and sealed
// sets were written before any rule and scored cold. The floors stop a change from quietly
// making things worse; raising one means a better result, never a rule written for that set.
func TestCoverage(t *testing.T) {
	floors := map[string]struct{ found, maxExtra int }{
		"training": {41, 0}, // 41 of 41: the rules were written from it
		"holdout":  {40, 0}, // cold 27 of 40, then 40 of 40 with rules written from its misses
		"sealed":   {23, 3}, // cold 23 of 43, and the same after; no rule is ever written from it
	}
	for _, set := range []string{"training", "holdout", "sealed"} {
		found, expected, extra, misses := score(t, set)
		t.Logf("%s: %d of %d facts (%d%%), %d facts no one expected", set, found, expected, 100*found/expected, extra)
		if os.Getenv("FACTS_SHOW") == set {
			t.Logf("  %s", strings.Join(misses, "\n  "))
		}
		if f := floors[set]; found < f.found || extra > f.maxExtra {
			t.Errorf("%s: %d found (floor %d), %d extra (at most %d)", set, found, f.found, extra, f.maxExtra)
		}
	}
}

func TestSummary(t *testing.T) {
	got := Summary(Extract([]Line{
		{Side: "callee", Text: "911, what is the address of your emergency?"},
		{Side: "caller", Text: "It's 418 Maple Avenue, there's a fire in my kitchen."},
		{Side: "callee", Text: "Is anyone hurt?"},
		{Side: "caller", Text: "No, nobody's hurt."},
		{Side: "caller", Text: "Call me on 540-555-0142."},
	}))
	want := "Fire at 418 Maple Avenue. Nobody hurt. Hazards: fire. Callback 540-555-0142."
	if got != want {
		t.Fatalf("%q", got)
	}
	if Summary(nil) != "" {
		t.Fatal("a summary of nothing")
	}
	// A neighbour's words are marked as reported.
	if got := Summary(Extract([]Line{{Side: "caller", Text: "27 Oak Street. My neighbour says her husband is not breathing."}})); !strings.Contains(got, "not breathing (reported)") || !strings.Contains(got, "Caller: neighbour") {
		t.Fatalf("%q", got)
	}
}
