package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// KnownCaller says why BitVoice knows a caller's number, or "" when it does not: in the contacts,
// called from here in the last year, or past the robot test in the last 90 days. Screening leaves
// known callers alone (the always-allowed list is checked by the engine).
func (s *Server) KnownCaller(number string) string {
	if c := s.directoryContact(number); c != nil {
		return "in the contacts as " + c.Name
	}
	rows, err := s.Store.CDRsWith(config.NumberForms(number), time.Now().AddDate(-1, 0, 0), 300)
	if err != nil {
		s.Log.Warn("could not read call history for screening", "err", err)
		return ""
	}
	for _, r := range rows {
		switch {
		case r.Direction == "outbound" && config.SameNumber(r.To, number):
			return "called from here on " + r.Start.Format("2 January 2006")
		case r.Direction == "inbound" && r.Screened == "passed" && time.Since(r.Start) < 90*24*time.Hour && config.SameNumber(r.From, number):
			return "passed the robot test on " + r.Start.Format("2 January 2006")
		}
	}
	return ""
}

var errAlreadyBlocked = errors.New("already blocked")

// BlockNumber puts a number on the block list for the block-last-caller code: pbx.ErrAlwaysAllowed when
// it is always allowed, nil when it was blocked already.
func (s *Server) BlockNumber(number, by, note string) error {
	n := listNumber(number)
	if len(strings.TrimPrefix(n, "+")) < 3 {
		return errors.New("not a number that can be blocked")
	}
	_, _, err := s.Store.Update(by, "Blocked "+n, 0, func(c *config.Config) error {
		sc := &c.System.Screening
		for _, a := range sc.Allowed {
			if a.Matches(n) {
				return pbx.ErrAlwaysAllowed
			}
		}
		for _, b := range sc.Blocked {
			if b.Matches(n) {
				return errAlreadyBlocked
			}
		}
		sc.Blocked = append(sc.Blocked, config.ScreenNumber{Number: n, Note: note, Added: time.Now().UTC(), By: by})
		return nil
	})
	if errors.Is(err, errAlreadyBlocked) {
		return nil
	}
	if err != nil {
		return err
	}
	s.Store.Audit(store.AuditEntry{Actor: by, Action: "screening.blocked", Detail: n + " (" + note + ")"})
	if s.Bus != nil {
		s.Bus.Publish("audit.added", map[string]string{"action": "screening.blocked"})
	}
	return nil
}

// listNumber writes a caller's number as the block list keeps it: + (when sent) and digits.
func listNumber(n string) string {
	n, _, _ = strings.Cut(strings.TrimSpace(n), ";")
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	if strings.HasPrefix(n, "+") {
		return "+" + d
	}
	return d
}

// Suggestion is a number the call history says may be spam, for a person to block or dismiss.
type Suggestion struct {
	Number string    `json:"number"`
	Name   string    `json:"name,omitempty"`
	Calls  int       `json:"calls"`
	Short  int       `json:"short"`  // ended within 10 seconds, answered or not
	Failed int       `json:"failed"` // failed the robot test
	Last   time.Time `json:"last"`
	Why    string    `json:"why"`
}

// suggestions reads the last 30 days of calls from outside for numbers that call again and again and
// are got rid of at once: three calls or more, at least three in four of them over within 10 seconds,
// or two robot tests failed, and never a real conversation (a person talking 30 seconds or more).
// Known, blocked, always-allowed and dismissed numbers are left out. Nothing is blocked by this.
func (s *Server) suggestions(cfg config.Config) ([]Suggestion, error) {
	rows, err := s.Store.CDRs(store.CDRQuery{Since: time.Now().AddDate(0, 0, -30), Direction: "inbound", Limit: 100000})
	if err != nil {
		return nil, err
	}
	type tally struct {
		Suggestion
		long bool
	}
	by := map[string]*tally{}
	for _, r := range rows {
		if r.Trunk == "" || r.Disposition == "blocked" || r.From == "" || r.From == "web" || !hasDigits(r.From) {
			continue
		}
		d := listNumber(r.From)
		key := strings.TrimPrefix(d, "+")
		if len(key) > 10 {
			key = key[len(key)-10:]
		}
		t := by[key]
		if t == nil {
			t = &tally{Suggestion: Suggestion{Number: d, Name: r.FromName, Last: r.Start}}
			by[key] = t
		}
		t.Calls++
		answered := !r.Answer.IsZero()
		switch {
		case r.Screened == "failed":
			t.Failed++
			t.Short++
		case answered && r.AnsweredExt != "" && r.Billsec >= 30:
			t.long = true
		case answered && r.Billsec < 10, !answered && r.Duration < 10:
			t.Short++
		}
	}
	sc := cfg.System.Screening
	listed := func(list []config.ScreenNumber, n string) bool {
		for _, x := range list {
			if x.Matches(n) {
				return true
			}
		}
		return false
	}
	out := []Suggestion{}
	for _, t := range by {
		if t.long || !(t.Failed >= 2 || t.Calls >= 3 && t.Short >= 3 && t.Short*4 >= t.Calls*3) {
			continue
		}
		if listed(sc.Blocked, t.Number) || listed(sc.Allowed, t.Number) || listed(sc.Dismissed, t.Number) || s.KnownCaller(t.Number) != "" {
			continue
		}
		why := strconv.Itoa(t.Calls) + " calls in 30 days"
		if t.Short > 0 {
			why += ", " + strconv.Itoa(t.Short) + " of them over within 10 seconds"
		}
		if t.Failed > 0 {
			why += "; failed the robot test " + times(t.Failed)
		}
		t.Why = why + "."
		out = append(out, t.Suggestion)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Short+out[i].Failed, out[j].Short+out[j].Failed; a != b {
			return a > b
		}
		return out[i].Last.After(out[j].Last)
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func hasDigits(n string) bool { return strings.ContainsAny(n, "0123456789") }

func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return strconv.Itoa(n) + " times"
}

func (s *Server) screeningRoutes() {
	// Numbers the call history says may be spam, to block or dismiss on the Settings page.
	s.handle("GET /api/screening/suggestions", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out, err := s.suggestions(s.config())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	// Ask the spam database about a number now, as saved (each ask may be charged by the service).
	s.handle("POST /api/screening/reputation/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var body struct {
			Number string `json:"number"`
		}
		if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Number) == "" {
			writeError(w, 400, "enter a number to look up")
			return
		}
		cfg := s.config()
		spam, said, err := s.Engine.TestReputation(r.Context(), &cfg, body.Number)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "spam": spam, "said": said})
	})
}
