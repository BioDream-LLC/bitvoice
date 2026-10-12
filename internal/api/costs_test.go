package api

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestCostReportAndAlerts(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	cfg, rev := s.Store.Config()
	costs := config.Costs{Currency: "EUR", HourlyLimit: 5, Budgets: []config.Budget{{ID: "all", Monthly: 10}, {ID: "sales", Department: "Sales", Monthly: 4}}}
	if _, _, err := s.Store.Update("test", "costs", rev, func(c *config.Config) error { c.System.Costs = costs; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = cfg
	now := time.Now().UTC()
	month := now.Format("2006-01")
	calls := []store.CDR{
		{ID: "a", Start: now, End: now, Direction: "outbound", Ext: "100", Department: "Sales", Trunk: "Carrier", Billsec: 600, Cost: 3},
		{ID: "b", Start: now, End: now, Direction: "outbound", Ext: "101", Trunk: "Carrier", Billsec: 60, Cost: 0.5},
		{ID: "c", Start: now, End: now, Direction: "outbound", Ext: "100", Billsec: 30},
	}
	for _, c := range calls {
		if err := s.Store.AddCDR(c); err != nil {
			t.Fatal(err)
		}
	}
	s.costs.reset(s, month, true)
	// Sales passes 80% (3 of 4 is 75%, so not yet), then 100% with the next call.
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	next := store.CDR{ID: "d", Start: now, End: now, Direction: "outbound", Ext: "100", Department: "Sales", Trunk: "Carrier", Billsec: 120, Cost: 1.5}
	if err := s.Store.AddCDR(next); err != nil {
		t.Fatal(err)
	}
	as := s.costs.add(s, next)
	if len(as) != 1 || as[0].Level != "fail" || !strings.Contains(as[0].Title, "Sales call spending is over budget") || !strings.Contains(as[0].Message, "EUR 4.50 of the EUR 4.00") {
		t.Fatalf("alerts %+v", as)
	}
	// It is not repeated, and the hourly alarm fires once outgoing spend in the hour passes 5.
	big := store.CDR{ID: "e", Start: now, End: now, Direction: "outbound", Ext: "101", Trunk: "Carrier", Billsec: 60, Cost: 5.5}
	s.Store.AddCDR(big)
	as = s.costs.add(s, big)
	titles := ""
	for _, a := range as {
		titles += a.Title + "|"
	}
	if !strings.Contains(titles, "Total call spending is over budget") || !strings.Contains(titles, "costing more than usual") || strings.Contains(titles, "Sales") {
		t.Fatalf("second alerts %s", titles)
	}
	if s.costProblem() == "" {
		t.Fatal("health")
	}
	code, rep := admin.do("GET", "/api/costs?month="+month, nil)
	if code != 200 || rep["currency"] != "EUR" || rep["total"].(float64) != 10.5 || rep["calls"].(float64) != 4 || rep["unpriced"].(float64) != 1 {
		t.Fatalf("report %d %v", code, rep)
	}
	deps := rep["departments"].([]any)
	if deps[0].(map[string]any)["name"] != "No department" || deps[0].(map[string]any)["cost"].(float64) != 6 {
		t.Fatalf("departments %v", deps)
	}
	bs := rep["budgets"].([]any)
	if bs[1].(map[string]any)["percent"].(float64) != 112.5 {
		t.Fatalf("budgets %v", bs)
	}
	if code, _ := admin.do("GET", "/api/costs?month=2026-1x", nil); code != 400 {
		t.Fatal("bad month")
	}
	code, p := admin.do("POST", "/api/costs/price", map[string]any{"trunk": map[string]any{"rates": []any{map[string]any{"prefix": "44", "name": "UK", "perMinute": 0.1}}}, "number": "+44 20", "seconds": 90})
	if code != 200 || p["cost"].(float64) != 0.2 {
		t.Fatalf("price %d %v", code, p)
	}
}
