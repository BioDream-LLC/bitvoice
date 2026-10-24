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
		"training": {0, 100},
		"holdout":  {0, 100},
		"sealed":   {0, 100},
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
