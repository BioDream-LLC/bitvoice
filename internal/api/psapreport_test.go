package api

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A layout as a state form might ask: a row a day and a total, calls, answered within 10 and 20
// seconds (as counts and shares), abandoned, the 90th percentile and TTY calls.
func TestReportLayout(t *testing.T) {
	cfg := config.Default()
	cfg.System.Timezone = "UTC"
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	call := func(d int, wait float64, ext string, via string) store.CDR {
		c := store.CDR{Emergency: true, Start: day.AddDate(0, 0, d).Add(9 * time.Hour), Duration: 60, AnsweredExt: ext}
		if ext != "" {
			c.Taken = c.Start.Add(time.Duration(wait * float64(time.Second)))
		}
		if via != "" {
			c.Text = []store.TextLine{{Via: via}}
		}
		return c
	}
	cdrs := []store.CDR{call(0, 4, "101", ""), call(0, 12, "102", ""), call(0, 25, "101", "tty"), call(0, 0, "", ""), call(1, 8, "101", "msrp"),
		{Start: day.Add(time.Hour), Duration: 30}} // an ordinary call: not counted
	rl := config.ReportLayout{Name: "State form 911-A", Rows: "day", Total: true, Columns: []config.ReportColumn{
		{Kind: "period", Label: "Date"}, {Kind: "calls"}, {Kind: "withinCount", Seconds: 10}, {Kind: "withinPct", Seconds: 20, Label: "% in 20s"},
		{Kind: "abandoned"}, {Kind: "abandonedPct"}, {Kind: "p90Answer"}, {Kind: "ttyCalls"}, {Kind: "msrpCalls"}, {Kind: "overCount", Seconds: 20}}}
	r := layReport(cfg, rl, cdrs, day, day.AddDate(0, 0, 2))
	want := [][]string{
		{"2026-10-01", "4", "1", "66.7", "1", "25.0", "25.0", "1", "0", "1"},
		{"2026-10-02", "1", "1", "100.0", "0", "0.0", "8.0", "0", "1", "0"},
		{"total", "5", "2", "75.0", "1", "20.0", "25.0", "1", "1", "1"},
	}
	if strings.Join(r.Header, "|") != "Date|calls|answered within 10 s|% in 20s|abandoned|abandoned (%)|90% answered within (s)|TTY calls|text to 911 calls|answered after more than 20 s" {
		t.Fatalf("header %q", r.Header)
	}
	for i, row := range want {
		if strings.Join(r.Rows[i], "|") != strings.Join(row, "|") {
			t.Errorf("row %d: %q, want %q", i, r.Rows[i], row)
		}
	}
	// By call-taker, and by hour.
	r = layReport(cfg, config.ReportLayout{Rows: "taker", Columns: []config.ReportColumn{{Kind: "period"}, {Kind: "answered"}, {Kind: "avgAnswer"}}}, cdrs, day, day.AddDate(0, 0, 2))
	if len(r.Rows) != 3 || strings.Join(r.Rows[0], "|") != "not answered|0|" || strings.Join(r.Rows[1], "|") != "101|3|12.3" {
		t.Fatalf("by taker %q", r.Rows)
	}
	r = layReport(cfg, config.ReportLayout{Rows: "hour", Columns: []config.ReportColumn{{Kind: "period"}, {Kind: "calls"}}}, cdrs, day, day.AddDate(0, 0, 2))
	if len(r.Rows) != 24 || r.Rows[9][1] != "5" {
		t.Fatalf("by hour %q", r.Rows[9])
	}
}
