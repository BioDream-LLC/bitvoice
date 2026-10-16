package api

import (
	"encoding/csv"
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A PSAP's own report layouts: the rows and columns its state's or county's form asks for, run
// over any dates as a table, a spreadsheet or a page to print.

// LaidOut is a report as laid out: its heading row and its rows, each cell as text.
type LaidOut struct {
	Name   string     `json:"name"`
	From   time.Time  `json:"from"`
	To     time.Time  `json:"to"`
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

func columnLabel(c config.ReportColumn) string {
	if c.Label != "" {
		return c.Label
	}
	s := strconv.Itoa(c.Seconds)
	return map[string]string{"period": "period", "calls": "calls", "answered": "answered", "abandoned": "abandoned", "abandonedPct": "abandoned (%)",
		"withinPct": "answered within " + s + " s (%)", "withinCount": "answered within " + s + " s", "overCount": "answered after more than " + s + " s",
		"avgAnswer": "average to answer (s)", "longestAnswer": "longest to answer (s)", "p90Answer": "90% answered within (s)", "avgLength": "average call length (s)",
		"textCalls": "text calls", "ttyCalls": "TTY calls", "rttCalls": "real-time text calls", "msrpCalls": "text to 911 calls"}[c.Kind]
}

// answerSeconds is how long a 911 call waited for a person, and whether one answered.
func answerSeconds(c store.CDR) (float64, bool) {
	a := c.Taken
	if a.IsZero() && c.AnsweredExt != "" {
		a = c.Answer
	}
	if a.IsZero() {
		return 0, false
	}
	return a.Sub(c.Start).Seconds(), true
}

func textVia(c store.CDR, via string) bool {
	for _, l := range c.Text {
		if via == "" || l.Via == via {
			return true
		}
	}
	return false
}

func cell(kind string, secs int, calls []store.CDR) string {
	var waits []float64
	n, answered, length := 0, 0, 0
	for _, c := range calls {
		n++
		length += c.Duration
		if w, ok := answerSeconds(c); ok {
			answered++
			waits = append(waits, w)
		}
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	pct := func(a, b int) string {
		if b == 0 {
			return ""
		}
		return f(100 * float64(a) / float64(b))
	}
	count := func(ok func(store.CDR) bool) string {
		k := 0
		for _, c := range calls {
			if ok(c) {
				k++
			}
		}
		return strconv.Itoa(k)
	}
	within := 0
	for _, w := range waits {
		if w <= float64(secs) {
			within++
		}
	}
	switch kind {
	case "calls":
		return strconv.Itoa(n)
	case "answered":
		return strconv.Itoa(answered)
	case "abandoned":
		return strconv.Itoa(n - answered)
	case "abandonedPct":
		return pct(n-answered, n)
	case "withinPct":
		return pct(within, answered)
	case "withinCount":
		return strconv.Itoa(within)
	case "overCount":
		return strconv.Itoa(answered - within)
	case "avgAnswer":
		if answered == 0 {
			return ""
		}
		sum := 0.0
		for _, w := range waits {
			sum += w
		}
		return f(sum / float64(answered))
	case "longestAnswer":
		if answered == 0 {
			return ""
		}
		m := 0.0
		for _, w := range waits {
			m = math.Max(m, w)
		}
		return f(m)
	case "p90Answer":
		if answered == 0 {
			return ""
		}
		sort.Float64s(waits)
		return f(waits[int(math.Ceil(0.9*float64(len(waits))))-1]) // the nearest-rank 90th percentile
	case "avgLength":
		if n == 0 {
			return ""
		}
		return f(float64(length) / float64(n))
	case "textCalls":
		return count(func(c store.CDR) bool { return textVia(c, "") })
	case "ttyCalls":
		return count(func(c store.CDR) bool { return textVia(c, "tty") })
	case "rttCalls":
		return count(func(c store.CDR) bool { return textVia(c, "rtt") })
	case "msrpCalls":
		return count(func(c store.CDR) bool { return textVia(c, "msrp") || textVia(c, "sip") })
	}
	return ""
}

// layReport runs a layout over the 911 calls from one day to another.
func layReport(cfg config.Config, rl config.ReportLayout, cdrs []store.CDR, from, to time.Time) LaidOut {
	loc := cfg.System.Location()
	out := LaidOut{Name: rl.Name, From: from, To: to}
	for _, c := range rl.Columns {
		out.Header = append(out.Header, columnLabel(c))
	}
	var calls []store.CDR
	for _, c := range cdrs {
		if c.Emergency && !c.Start.Before(from) && c.Start.Before(to) {
			calls = append(calls, c)
		}
	}
	type group struct {
		label string
		calls []store.CDR
	}
	var groups []group
	add := func(label string, keep func(store.CDR) bool) {
		g := group{label: label}
		for _, c := range calls {
			if keep(c) {
				g.calls = append(g.calls, c)
			}
		}
		groups = append(groups, g)
	}
	switch rl.Rows {
	case "day":
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			next := d.AddDate(0, 0, 1)
			add(d.In(loc).Format("2006-01-02"), func(c store.CDR) bool { return !c.Start.Before(d) && c.Start.Before(next) })
		}
	case "week":
		start := from
		for start.Weekday() != time.Monday {
			start = start.AddDate(0, 0, -1)
		}
		for d := start; d.Before(to); d = d.AddDate(0, 0, 7) {
			next := d.AddDate(0, 0, 7)
			add("week of "+d.In(loc).Format("2006-01-02"), func(c store.CDR) bool { return !c.Start.Before(d) && c.Start.Before(next) })
		}
	case "month":
		for d := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, loc); d.Before(to); d = d.AddDate(0, 1, 0) {
			next := d.AddDate(0, 1, 0)
			add(d.Format("2006-01"), func(c store.CDR) bool { return !c.Start.Before(d) && c.Start.Before(next) })
		}
	case "hour":
		for h := 0; h < 24; h++ {
			add(fmt.Sprintf("%02d:00", h), func(c store.CDR) bool { return c.Start.In(loc).Hour() == h })
		}
	case "taker":
		seen := map[string]bool{}
		var exts []string
		for _, c := range calls {
			if !seen[c.AnsweredExt] {
				seen[c.AnsweredExt] = true
				exts = append(exts, c.AnsweredExt)
			}
		}
		sort.Strings(exts)
		for _, x := range exts {
			label := x
			if label == "" {
				label = "not answered"
			} else if e := cfg.FindExtension(x); e != nil && e.Name != "" {
				label = x + " " + e.Name
			}
			add(label, func(c store.CDR) bool { return c.AnsweredExt == x })
		}
	default: // total
		add(from.In(loc).Format("2006-01-02")+" to "+to.AddDate(0, 0, -1).In(loc).Format("2006-01-02"), func(store.CDR) bool { return true })
	}
	if rl.Total && rl.Rows != "total" {
		groups = append(groups, group{label: "total", calls: calls})
	}
	for _, g := range groups {
		row := make([]string, len(rl.Columns))
		for i, c := range rl.Columns {
			if c.Kind == "period" {
				row[i] = g.label
			} else {
				row[i] = cell(c.Kind, c.Seconds, g.calls)
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

func writeLaidOut(w http.ResponseWriter, cfg config.Config, r LaidOut, format string) {
	name := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			return c
		}
		return '-'
	}, r.Name) + "-" + r.From.Format("2006-01-02") + "-to-" + r.To.AddDate(0, 0, -1).Format("2006-01-02")
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
		cw := csv.NewWriter(w)
		cw.Write(r.Header)
		for _, row := range r.Rows {
			safe := make([]string, len(row))
			for i, v := range row {
				safe[i] = csvSafe(v)
			}
			cw.Write(safe)
		}
		cw.Flush()
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		var b strings.Builder
		fmt.Fprintf(&b, `<!doctype html><html lang="en"><meta charset="utf-8"><title>%s</title><style>body{font:13px system-ui,sans-serif;margin:24px}table{border-collapse:collapse}th,td{border:1px solid #999;padding:4px 8px;text-align:right}th:first-child,td:first-child{text-align:left}tr:last-child td{font-weight:%s}</style>`,
			html.EscapeString(r.Name), map[bool]string{true: "bold", false: "normal"}[len(r.Rows) > 0 && r.Rows[len(r.Rows)-1][0] == "total"])
		fmt.Fprintf(&b, "<h1>%s</h1><p>%s, %s to %s. 911 calls: answer times count from the call's arrival to a call-taker's answer.</p><table><thead><tr>",
			html.EscapeString(r.Name), html.EscapeString(cfg.System.Name), r.From.Format("2006-01-02"), r.To.AddDate(0, 0, -1).Format("2006-01-02"))
		for _, h := range r.Header {
			fmt.Fprintf(&b, "<th>%s</th>", html.EscapeString(h))
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range r.Rows {
			b.WriteString("<tr>")
			for _, v := range row {
				fmt.Fprintf(&b, "<td>%s</td>", html.EscapeString(v))
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table></html>")
		w.Write([]byte(b.String()))
	default:
		writeJSON(w, 200, r)
	}
}

func (s *Server) reportLayoutRoutes() {
	s.handle("GET /api/psap/layouts/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg := s.config()
		rl := cfg.FindReportLayout(r.PathValue("id"))
		if rl == nil {
			writeError(w, 404, "no such report layout")
			return
		}
		loc := cfg.System.Location()
		day := func(k string, def time.Time) time.Time {
			if t, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get(k), loc); err == nil {
				return t
			}
			return def
		}
		today := time.Now().In(loc)
		from := day("from", time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc))
		to := day("to", time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)).AddDate(0, 0, 1)
		if !to.After(from) || to.Sub(from) > 400*24*time.Hour {
			writeError(w, 400, "a window of one day to 400 days")
			return
		}
		cdrs, err := s.Store.CDRs(store.CDRQuery{Since: from, Until: to, Limit: 500000})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeLaidOut(w, cfg, layReport(cfg, *rl, cdrs, from, to), r.URL.Query().Get("format"))
	})
}
