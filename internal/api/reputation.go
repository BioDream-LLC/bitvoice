package api

import (
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// NumberReputation is how one outgoing number is being received, judged from our own calls: a number
// that carriers or phones label "spam likely" gets answered far less, and called people hang up fast.
type NumberReputation struct {
	Number     string  `json:"number"`
	Brand      string  `json:"brand,omitempty"`
	Calls      int     `json:"calls"`
	Answered   int     `json:"answered"`
	AnswerRate float64 `json:"answerRate"` // percent
	Short      int     `json:"short"`      // answered, then hung up within 10 seconds
	ShortRate  float64 `json:"shortRate"`  // percent of answered
	Unwanted   int     `json:"unwanted"`   // refused with 607 (the person marked it unwanted)
	Rejected   int     `json:"rejected"`   // refused with 608 (a carrier's analytics blocked it)
	PeakDay    int     `json:"peakDay"`    // most calls in one day
	PrevRate   float64 `json:"prevRate"`   // answer rate the period before, -1 when too few calls
	// Status is good, watch, labelled or few; Why says it in plain words.
	Status string   `json:"status"`
	Why    []string `json:"why"`
	Watch  bool     `json:"watch"`
}

type repCache struct {
	mu  sync.Mutex
	at  time.Time
	rev int
	bad []NumberReputation
}

const (
	repMinCalls = 20  // fewer calls than this say too little
	repBusyDay  = 150 // carriers treat more calls a day from one number as a robocaller
)

func judgeReputation(r *NumberReputation) {
	if r.Calls > 0 {
		r.AnswerRate = pct(r.Answered, r.Calls)
	}
	if r.Answered > 0 {
		r.ShortRate = pct(r.Short, r.Answered)
	}
	r.Status, r.Why = "good", []string{}
	switch {
	case r.Rejected > 0:
		r.Status = "labelled"
		r.Why = append(r.Why, plural(r.Rejected, "call was", "calls were")+" refused by a carrier's spam filter (608): the number is labelled. Register it with the carriers' free redress sites and slow down.")
	case r.Unwanted >= 3:
		r.Status = "labelled"
		r.Why = append(r.Why, plural(r.Unwanted, "person", "people")+" marked calls from this number unwanted (607).")
	}
	if r.Calls < repMinCalls && r.Status == "good" {
		r.Status = "few"
		r.Why = append(r.Why, "Only "+plural(r.Calls, "call", "calls")+": too few to judge.")
		return
	}
	watch := func(s string) {
		if r.Status == "good" {
			r.Status = "watch"
		}
		r.Why = append(r.Why, s)
	}
	if r.Calls >= repMinCalls && r.AnswerRate < 20 {
		watch("Only " + ftoa(r.AnswerRate) + "% of calls are answered: phones may be showing it as spam likely.")
	}
	if r.Answered >= 10 && r.ShortRate >= 40 {
		watch(ftoa(r.ShortRate) + "% of answered calls end within 10 seconds: people hang up as soon as they hear it.")
	}
	if r.PrevRate >= 0 && r.Calls >= repMinCalls && r.PrevRate >= 20 && r.AnswerRate < r.PrevRate/2 {
		watch("Answers fell from " + ftoa(r.PrevRate) + "% to " + ftoa(r.AnswerRate) + "%: the number may have been labelled recently.")
	}
	if r.PeakDay > repBusyDay {
		watch("Up to " + strconv.Itoa(r.PeakDay) + " calls a day from this one number: carriers flag heavy callers. Spread calls over more numbers.")
	}
	if r.Status == "good" {
		r.Why = append(r.Why, ftoa(r.AnswerRate)+"% answered; nothing suggests it is labelled.")
	}
}

func pct(a, b int) float64  { return float64(int(float64(a)*1000/float64(b)+0.5)) / 10 }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// reputation judges every outgoing number over the last days, and the same span before for the trend.
func (s *Server) reputation(cfg *config.Config, days int) ([]NumberReputation, error) {
	now := time.Now()
	from := now.AddDate(0, 0, -days)
	rs, err := s.Store.CDRs(store.CDRQuery{Since: from.AddDate(0, 0, -days), Limit: 100000})
	if err != nil {
		return nil, err
	}
	type acc struct {
		cur, prev NumberReputation
		perDay    map[string]int
	}
	by := map[string]*acc{}
	get := func(n string) *acc {
		a := by[n]
		if a == nil {
			a = &acc{cur: NumberReputation{Number: n}, perDay: map[string]int{}}
			by[n] = a
		}
		return a
	}
	for _, b := range cfg.System.Brands {
		a := get(b.Number)
		a.cur.Brand, a.cur.Watch = b.Name, b.Watch
	}
	for _, r := range rs {
		if r.Direction != "outbound" || r.CallerID == "" {
			continue
		}
		n := r.CallerID
		if b := cfg.System.BrandFor(n); b != nil {
			n = b.Number
		}
		a := get(n)
		t := &a.cur
		if r.Start.Before(from) {
			t = &a.prev
		} else {
			a.perDay[r.Start.Format("2006-01-02")]++
		}
		t.Calls++
		if !r.Answer.IsZero() {
			t.Answered++
			if r.Billsec < 10 {
				t.Short++
			}
		}
		switch r.Code {
		case 607:
			t.Unwanted++
		case 608:
			t.Rejected++
		}
	}
	out := []NumberReputation{}
	for _, a := range by {
		r := a.cur
		for _, n := range a.perDay {
			r.PeakDay = max(r.PeakDay, n)
		}
		r.PrevRate = -1
		if a.prev.Calls >= repMinCalls {
			r.PrevRate = pct(a.prev.Answered, a.prev.Calls)
		}
		judgeReputation(&r)
		out = append(out, r)
	}
	rank := map[string]int{"labelled": 0, "watch": 1, "good": 2, "few": 3}
	slices.SortFunc(out, func(x, y NumberReputation) int {
		if d := rank[x.Status] - rank[y.Status]; d != 0 {
			return d
		}
		return y.Calls - x.Calls
	})
	return out, nil
}

// reputationProblems lists the watched numbers that look labelled, for Health.
func (s *Server) reputationProblems() []NumberReputation {
	cfg := s.config()
	watched := false
	for _, b := range cfg.System.Brands {
		watched = watched || b.Watch
	}
	if !watched {
		return nil
	}
	_, rev := s.Store.Config()
	s.rep.mu.Lock()
	defer s.rep.mu.Unlock()
	if time.Since(s.rep.at) < 10*time.Minute && s.rep.rev == rev {
		return s.rep.bad
	}
	rs, err := s.reputation(&cfg, 7)
	if err != nil {
		return nil
	}
	var bad []NumberReputation
	for _, r := range rs {
		if r.Watch && (r.Status == "labelled" || r.Status == "watch") {
			bad = append(bad, r)
		}
	}
	s.rep.at, s.rep.rev, s.rep.bad = time.Now(), rev, bad
	return bad
}

func (s *Server) reputationRoutes() {
	s.handle("GET /api/reputation", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days < 1 || days > 90 {
			days = 30
		}
		cfg := s.config()
		rs, err := s.reputation(&cfg, days)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"days": days, "numbers": rs})
	})
}
