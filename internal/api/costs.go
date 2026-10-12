package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// CostLine is what one department, person, trunk or day spent.
type CostLine struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Calls   int     `json:"calls"`
	Minutes float64 `json:"minutes"`
	Cost    float64 `json:"cost"`
}

// BudgetUse is a budget and what its month has spent so far.
type BudgetUse struct {
	config.Budget
	Spent   float64 `json:"spent"`
	Percent float64 `json:"percent"`
}

// CostReport is a month's call costs.
type CostReport struct {
	Month       string      `json:"month"`
	Currency    string      `json:"currency"`
	Total       float64     `json:"total"`
	Calls       int         `json:"calls"`    // calls that cost something
	Unpriced    int         `json:"unpriced"` // answered calls over a trunk with no rate for them
	Departments []CostLine  `json:"departments"`
	People      []CostLine  `json:"people"`
	Trunks      []CostLine  `json:"trunks"`
	Days        []CostLine  `json:"days"`
	Top         []store.CDR `json:"top"`
	Budgets     []BudgetUse `json:"budgets"`
}

func currency(cfg config.Config) string {
	if cfg.System.Costs.Currency != "" {
		return cfg.System.Costs.Currency
	}
	return "USD"
}

func money(cur string, v float64) string { return fmt.Sprintf("%s %.2f", cur, v) }

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// costReport sums a month of call records.
func costReport(cfg config.Config, month string, rs []store.CDR) CostReport {
	rep := CostReport{Month: month, Currency: currency(cfg), Departments: []CostLine{}, People: []CostLine{}, Trunks: []CostLine{}, Days: []CostLine{}, Top: []store.CDR{}, Budgets: []BudgetUse{}}
	loc := cfg.System.Location()
	maps := [4]map[string]*CostLine{{}, {}, {}, {}}
	add := func(i int, key, name string, r store.CDR) {
		l, ok := maps[i][key]
		if !ok {
			l = &CostLine{Key: key, Name: name}
			maps[i][key] = l
		}
		l.Calls++
		l.Minutes += float64(r.Billsec) / 60
		l.Cost += r.Cost
	}
	byDept := map[string]float64{}
	for _, r := range rs {
		if r.Cost <= 0 {
			if r.Billsec > 0 && r.Direction == "outbound" && r.Rate == "" {
				rep.Unpriced++
			}
			continue
		}
		rep.Calls++
		rep.Total += r.Cost
		byDept[strings.ToLower(r.Department)] += r.Cost
		dept := r.Department
		if dept == "" {
			dept = "No department"
		}
		add(0, strings.ToLower(r.Department), dept, r)
		who, name := r.Ext, r.FromName
		if who == "" {
			who, name = "in", "Calls in"
		} else if x := cfg.FindExtension(who); x != nil {
			name = x.Number + " " + x.Name
		}
		add(1, who, name, r)
		add(2, r.Trunk, r.Trunk, r)
		d := r.Start.In(loc).Format("2006-01-02")
		add(3, d, d, r)
		rep.Top = append(rep.Top, r)
	}
	for i, dst := range []*[]CostLine{&rep.Departments, &rep.People, &rep.Trunks, &rep.Days} {
		for _, l := range maps[i] {
			l.Cost, l.Minutes = round4(l.Cost), math.Round(l.Minutes*10)/10
			*dst = append(*dst, *l)
		}
		if i == 3 {
			slices.SortFunc(*dst, func(a, b CostLine) int { return strings.Compare(a.Key, b.Key) })
		} else {
			slices.SortFunc(*dst, func(a, b CostLine) int { return cmpFloat(b.Cost, a.Cost) })
		}
	}
	slices.SortFunc(rep.Top, func(a, b store.CDR) int { return cmpFloat(b.Cost, a.Cost) })
	rep.Top = rep.Top[:min(10, len(rep.Top))]
	rep.Total = round4(rep.Total)
	for _, b := range cfg.System.Costs.Budgets {
		spent := rep.Total
		if b.Department != "" {
			spent = byDept[strings.ToLower(b.Department)]
		}
		rep.Budgets = append(rep.Budgets, BudgetUse{Budget: b, Spent: round4(spent), Percent: math.Round(1000*spent/b.Monthly) / 10})
	}
	return rep
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// costWatch keeps this month's spend and the last hour's, to raise budget and runaway-spend alerts.
type costWatch struct {
	mu        sync.Mutex
	month     string
	spent     map[string]float64 // by lower-case department; "" key is the whole month
	sent      map[string]bool    // budget id + threshold, this month
	hour      []timedCost
	hourAlert time.Time
	last      string // the last alert, for Health for a day
	lastAt    time.Time
}

type timedCost struct {
	at   time.Time
	cost float64
}

// RunCosts watches call costs as calls end and alerts on budgets (80% and 100%) and on outgoing
// spend over the hourly limit.
func (s *Server) RunCosts(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	s.costs.reset(s, time.Now().UTC().Format("2006-01"), true)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if r, ok := ev.Data.(store.CDR); ok && ev.Type == "call.logged" && r.Cost > 0 {
				for _, a := range s.costs.add(s, r) {
					s.costAlert(a)
				}
			}
		}
	}
}

// reset reads a month's spend from the call records; quiet marks budgets already past a threshold
// as alerted, so a restart does not repeat old alerts.
func (w *costWatch) reset(s *Server, month string, quiet bool) {
	spent := map[string]float64{}
	_ = s.Store.MonthCDRs(month, func(r store.CDR) {
		spent[""] += r.Cost
		spent[strings.ToLower(r.Department)+"\x00"] += r.Cost
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	w.month, w.spent, w.sent = month, spent, map[string]bool{}
	if quiet {
		for _, b := range s.config().System.Costs.Budgets {
			got := w.spentOn(b)
			for _, th := range []float64{80, 100} {
				if got >= b.Monthly*th/100 {
					w.sent[b.ID+fmt.Sprint(th)] = true
				}
			}
		}
	}
}

func (w *costWatch) spentOn(b config.Budget) float64 {
	if b.Department == "" {
		return w.spent[""]
	}
	return w.spent[strings.ToLower(b.Department)+"\x00"]
}

type costAlertMsg struct {
	Level   string `json:"level"` // warn or fail
	Title   string `json:"title"`
	Message string `json:"message"`
	Email   string `json:"-"`
}

func (w *costWatch) add(s *Server, r store.CDR) []costAlertMsg {
	if m := r.End.UTC().Format("2006-01"); m != w.month {
		w.reset(s, m, false)
		return nil // reset read this call already
	}
	cfg := s.config()
	cur := currency(cfg)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spent[""] += r.Cost
	w.spent[strings.ToLower(r.Department)+"\x00"] += r.Cost
	var out []costAlertMsg
	for _, b := range cfg.System.Costs.Budgets {
		got := w.spentOn(b)
		what := "Total call spending"
		if b.Department != "" {
			what = b.Department + " call spending"
		}
		for _, th := range []float64{100, 80} {
			k := b.ID + fmt.Sprint(th)
			if got < b.Monthly*th/100 || w.sent[k] {
				continue
			}
			w.sent[k], w.sent[b.ID+"80"] = true, true
			a := costAlertMsg{Level: "warn", Title: what + " has reached 80% of its budget",
				Message: fmt.Sprintf("%s is %s of the %s budget for %s.", what, money(cur, got), money(cur, b.Monthly), w.month), Email: b.Email}
			if th == 100 {
				a.Level, a.Title = "fail", what+" is over budget"
			}
			out = append(out, a)
			break
		}
	}
	if lim := cfg.System.Costs.HourlyLimit; lim > 0 && r.Direction != "inbound" {
		now := time.Now()
		w.hour = append(w.hour, timedCost{now, r.Cost})
		var sum float64
		keep := w.hour[:0]
		for _, x := range w.hour {
			if now.Sub(x.at) < time.Hour {
				keep = append(keep, x)
				sum += x.cost
			}
		}
		w.hour = keep
		if sum > lim && now.Sub(w.hourAlert) > time.Hour {
			w.hourAlert = now
			out = append(out, costAlertMsg{Level: "fail", Title: "Outgoing calls are costing more than usual",
				Message: fmt.Sprintf("Calls out cost %s in the last hour, over the %s alarm. If nobody here is making them, this may be toll fraud: check Call history, then disable the extension or trunk.", money(cur, sum), money(cur, lim))})
		}
	}
	for _, a := range out {
		w.last, w.lastAt = a.Title+": "+a.Message, time.Now()
	}
	return out
}

func (s *Server) costAlert(a costAlertMsg) {
	s.Bus.Publish("cost.alert", a)
	cfg := s.config()
	if cfg.System.Email.Host == "" {
		return
	}
	to := []string{}
	for _, e := range []string{a.Email, cfg.System.Costs.AlertEmail} {
		if e != "" && !slices.Contains(to, e) {
			to = append(to, e)
		}
	}
	for _, addr := range to {
		msg, err := buildMail(cfg.System.Email.From, addr, a.Title, a.Message+"\n\nOpen Costs in BitVoice for the details.\n", "", "", nil)
		if err == nil {
			err = sendMail(cfg.System.Email, addr, msg)
		}
		if err != nil {
			s.Bus.Publish("email.failed", map[string]string{"cost": a.Title, "error": err.Error()})
		}
	}
}

func (s *Server) costProblem() string {
	s.costs.mu.Lock()
	defer s.costs.mu.Unlock()
	if time.Since(s.costs.lastAt) > 24*time.Hour {
		return ""
	}
	return s.costs.last
}

func (s *Server) costRoutes() {
	s.handle("GET /api/costs", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		month := r.URL.Query().Get("month")
		if month == "" {
			month = time.Now().UTC().Format("2006-01")
		}
		if _, err := time.Parse("2006-01", month); err != nil {
			writeError(w, 400, "month must be YYYY-MM")
			return
		}
		var rs []store.CDR
		if err := s.Store.MonthCDRs(month, func(c store.CDR) { rs = append(rs, c) }); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, costReport(s.config(), month, rs))
	})
	// What a call would cost: for checking rates as they are typed.
	s.handle("POST /api/costs/price", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Trunk   config.Trunk `json:"trunk"`
			Number  string       `json:"number"`
			Seconds int          `json:"seconds"`
		}
		if err := readJSON(r, &in); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		rate, ok := in.Trunk.RateFor(in.Number)
		if !ok {
			writeJSON(w, 200, map[string]any{"found": false})
			return
		}
		writeJSON(w, 200, map[string]any{"found": true, "rate": rate, "cost": rate.Charge(max(in.Seconds, 1))})
	})
}
