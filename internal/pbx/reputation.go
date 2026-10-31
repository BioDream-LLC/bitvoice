package pbx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// reputations keeps the spam database's answers, so a caller who rings again is not paid for twice.
type reputations struct {
	mu sync.Mutex
	m  map[string]repHit
}

type repHit struct {
	spam  bool
	said  string
	until time.Time
}

// reputationCheck asks the spam database about a caller: whether it says spam, and what it said. A
// slow, broken or refusing service counts as "not spam": a call is never held up or refused because
// the database could not be asked.
func (e *Engine) reputationCheck(cfg *config.Config, number string) (bool, string) {
	r := cfg.System.Screening.Reputation
	key := r.URL + "\x00" + r.Field + "\x00" + number
	e.reps.mu.Lock()
	if h, ok := e.reps.m[key]; ok && time.Now().Before(h.until) {
		e.reps.mu.Unlock()
		return h.spam, h.said
	}
	e.reps.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(r.TimeoutMS, 100))*time.Millisecond)
	defer cancel()
	v, err := fetchReputation(ctx, cfg, r, number)
	if err != nil {
		e.Log.Info("spam database lookup failed", "err", err.Error())
		return false, ""
	}
	spam, said := spamVerdict(v, r)
	keep := time.Duration(r.CacheHours) * time.Hour
	if r.CacheHours == 0 {
		keep = 24 * time.Hour
	}
	if r.CacheHours >= 0 {
		e.reps.mu.Lock()
		if e.reps.m == nil || len(e.reps.m) > 20000 {
			e.reps.m = map[string]repHit{}
		}
		e.reps.m[key] = repHit{spam: spam, said: said, until: time.Now().Add(keep)}
		e.reps.mu.Unlock()
	}
	return spam, said
}

// TestReputation asks the spam database about a number now, past the cache: for the Test button. It
// returns whether it says spam, and the value it found.
func (e *Engine) TestReputation(ctx context.Context, cfg *config.Config, number string) (bool, string, error) {
	r := cfg.System.Screening.Reputation
	if r.URL == "" {
		return false, "", fmt.Errorf("no spam database is set")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max(r.TimeoutMS, 100))*time.Millisecond)
	defer cancel()
	v, err := fetchReputation(ctx, cfg, r, cleanNumber(number))
	if err != nil {
		return false, "", err
	}
	spam, said := spamVerdict(v, r)
	return spam, said, nil
}

// fetchReputation asks the database and returns the value at its Field (nil when the number is not
// listed: a 404, or no such field).
func fetchReputation(ctx context.Context, cfg *config.Config, r config.Reputation, number string) (any, error) {
	u := strings.NewReplacer("{number}", url.QueryEscape(number), "{e164}", url.QueryEscape(e164For(cfg, number))).Replace(r.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BitVoice")
	req.Header.Set("Accept", "application/json")
	if r.Username != "" {
		req.SetBasicAuth(r.Username, r.Password)
	}
	resp, err := lookupClient.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err // never log the address: it may carry a key
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, &lookupStatus{resp.StatusCode}
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("the answer is not JSON")
	}
	return config.JSONAt(v, r.Field), nil
}

// spamVerdict reads a database's value: true, a number at or above SpamAt, or text equal to SpamText
// (or a number written as text) is spam. said is the value as text, for the call's why trail.
func spamVerdict(v any, r config.Reputation) (bool, string) {
	at := r.SpamAt
	if at == 0 {
		at = 1
	}
	switch t := v.(type) {
	case bool:
		return t, strconv.FormatBool(t)
	case float64:
		return t >= at, r.Field + " " + strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		if r.SpamText != "" && strings.EqualFold(strings.TrimSpace(t), r.SpamText) {
			return true, r.Field + " " + t
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f >= at, r.Field + " " + t
		}
		return false, r.Field + " " + t
	}
	return false, "not listed"
}

// e164For writes a caller's number in E.164 (+ and the country code) as best it can from how the
// provider sent it and this system's country.
func e164For(cfg *config.Config, n string) string {
	n = cleanNumber(n)
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	cc := cfg.System.CallingCode()
	switch {
	case d == "":
		return n
	case strings.HasPrefix(n, "+"):
		return "+" + d
	case strings.HasPrefix(d, "00"):
		return "+" + d[2:]
	case cc == "1" && strings.HasPrefix(d, "011"):
		return "+" + d[3:]
	case cc == "1" && len(d) == 10:
		return "+1" + d
	case cc == "1" && len(d) == 11 && d[0] == '1':
		return "+" + d
	case cc != "" && cc != "1" && strings.HasPrefix(d, "0"):
		return "+" + cc + d[1:]
	}
	return "+" + d
}
