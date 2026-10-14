package pbx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// OpenCNAMBase is OpenCNAM's API address; tests point it elsewhere.
var OpenCNAMBase = "https://api.opencnam.com/v3/phone/"

type lookupCache struct {
	mu sync.Mutex
	m  map[string]lookupHit
}

type lookupHit struct {
	name  string
	until time.Time
}

// lookupName asks a caller ID lookup for a caller's name: from the cache, else from the service, never
// waiting longer than the lookup allows. "" when it does not know, failed or took too long.
func (e *Engine) lookupName(ctx context.Context, l *config.CallerIDLookup, number string) string {
	if number == "" || l == nil {
		return ""
	}
	key := l.ID + "\x00" + number
	e.lookups.mu.Lock()
	if h, ok := e.lookups.m[key]; ok && time.Now().Before(h.until) {
		e.lookups.mu.Unlock()
		return h.name
	}
	e.lookups.mu.Unlock()
	timeout := time.Duration(max(l.TimeoutMS, 100)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, err := fetchName(ctx, l, number)
	if err != nil {
		e.Log.Info("caller ID lookup failed", "lookup", l.Name, "err", err.Error())
		return "" // not cached: the next call asks again
	}
	keep := time.Duration(l.CacheHours) * time.Hour
	if l.CacheHours == 0 {
		keep = 24 * time.Hour
	}
	if name == "" && keep > time.Hour {
		keep = time.Hour // an unknown number may be listed soon
	}
	if l.CacheHours >= 0 {
		e.lookups.mu.Lock()
		if e.lookups.m == nil || len(e.lookups.m) > 20000 {
			e.lookups.m = map[string]lookupHit{}
		}
		e.lookups.m[key] = lookupHit{name: name, until: time.Now().Add(keep)}
		e.lookups.mu.Unlock()
	}
	return name
}

// ClearLookupCache forgets every looked-up name (after a lookup is changed).
func (e *Engine) ClearLookupCache() {
	e.lookups.mu.Lock()
	e.lookups.m = nil
	e.lookups.mu.Unlock()
}

// TestLookup asks a lookup for a number now, past the cache: for the Test button.
func (e *Engine) TestLookup(ctx context.Context, l *config.CallerIDLookup, number string) (string, error) {
	timeout := time.Duration(max(l.TimeoutMS, 100)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fetchName(ctx, l, cleanNumber(number))
}

var lookupClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func fetchName(ctx context.Context, l *config.CallerIDLookup, number string) (string, error) {
	var u string
	switch l.Kind {
	case "opencnam":
		e164 := number
		switch {
		case strings.HasPrefix(e164, "+"):
		case len(e164) == 10:
			e164 = "+1" + e164
		case len(e164) == 11 && e164[0] == '1':
			e164 = "+" + e164
		default:
			e164 = "+" + e164
		}
		base := OpenCNAMBase
		if l.URL != "" {
			base = strings.TrimRight(l.URL, "/") + "/"
		}
		q := url.Values{"account_sid": {l.Username}, "auth_token": {l.Password}, "format": {"pbx"}}
		u = base + url.PathEscape(e164) + "?" + q.Encode()
	default:
		u = strings.ReplaceAll(l.URL, "{number}", url.QueryEscape(number))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "BitVoice")
	if l.Kind == "http" && l.Username != "" {
		req.SetBasicAuth(l.Username, l.Password)
	}
	resp, err := lookupClient.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err // never log the address: it may carry a token
		}
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusNotFound {
		return "", nil // not listed
	}
	if resp.StatusCode/100 != 2 {
		return "", &lookupStatus{resp.StatusCode}
	}
	name := string(body)
	if l.Kind == "http" && l.Field != "" {
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return "", err
		}
		name = ""
		for _, k := range strings.Split(l.Field, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = m[k]
		}
		if s, ok := v.(string); ok {
			name = s
		}
	}
	return cleanName(name), nil
}

type lookupStatus struct{ code int }

func (s *lookupStatus) Error() string { return "the service answered " + http.StatusText(s.code) }

// cleanName keeps the first line of an answer, without control characters, at most 50 characters,
// and drops answers that only say the name is unknown.
func cleanName(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 50 {
		s = strings.TrimSpace(string(r[:50]))
	}
	switch strings.ToLower(s) {
	case "unknown", "unavailable", "not found", "null", "none", "-":
		return ""
	}
	return s
}
