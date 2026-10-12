package config

import (
	"strings"
	"testing"
)

func TestRates(t *testing.T) {
	tr := Trunk{Rates: []Rate{{Prefix: "", PerMinute: 0.01}, {Prefix: "1900", Name: "Premium", PerMinute: 2, Setup: 0.5}, {Prefix: "44", Name: "UK", PerMinute: 0.05, Increment: 6, Minimum: 30}}}
	cases := []struct {
		number  string
		billsec int
		name    string
		cost    float64
	}{
		{"+1 900 555 0100", 61, "Premium", 0.5 + 2*2}, // two whole minutes
		{"+442071234567", 10, "UK", 0.025},            // 30 s minimum
		{"442071234567", 31, "UK", 0.03},              // 36 s in 6 s steps
		{"12125550100", 1, "", 0.01},                  // the catch-all rate
		{"12125550100", 0, "", 0},                     // unanswered
	}
	for _, c := range cases {
		r, ok := tr.RateFor(c.number)
		if !ok || r.Name != c.name || r.Charge(c.billsec) != c.cost {
			t.Errorf("%s %ds: %v %+v costs %v, want %s %v", c.number, c.billsec, ok, r, r.Charge(c.billsec), c.name, c.cost)
		}
	}
	if _, ok := (Trunk{Rates: []Rate{{Prefix: "44"}}}).RateFor("1212"); ok {
		t.Error("no rate should match")
	}
	cfg := Default()
	cfg.Trunks = []Trunk{{ID: "t", Name: "T", Host: "h.example", Rates: []Rate{{Prefix: "+44"}, {Prefix: "1", Increment: -1}, {Prefix: "1"}}}}
	cfg.System.Costs = Costs{Currency: "dollars", Budgets: []Budget{{Monthly: 0}, {Department: "Sales", Monthly: 10}, {Department: "sales", Monthly: 5, Email: "nope"}}}
	err := cfg.Validate()
	for _, f := range []string{"trunks[0].rates[0].prefix", "trunks[0].rates[1].increment", "trunks[0].rates[2].prefix", "system.costs.currency", "system.costs.budgets[0].monthly", "system.costs.budgets[2].department", "system.costs.budgets[2].email"} {
		if err == nil || !strings.Contains(err.Error(), f) {
			t.Errorf("want a problem on %s: %v", f, err)
		}
	}
}
