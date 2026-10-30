// Package workspace reads an organisation's people, shared contacts and calendars' busy times from
// Microsoft 365 (Microsoft Graph, an app registration with application permissions) or Google
// Workspace (a service account with domain-wide delegation), for BitVoice's directory and for who
// is away. It only reads, with the standard library alone (its own OAuth client-credentials and
// JWT-bearer token requests).
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/calendar"
)

// Person is someone in the organisation's directory, or a shared contact.
type Person struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Email      string   `json:"email,omitempty"`
	Company    string   `json:"company,omitempty"`
	Department string   `json:"department,omitempty"`
	Title      string   `json:"title,omitempty"`
	Numbers    []Number `json:"numbers,omitempty"`
	// Shared is a shared contact (outside the organisation), not a person in it.
	Shared bool `json:"shared,omitempty"`
}

// Number is one of a person's phone numbers: work, mobile, home or other.
type Number struct {
	Kind   string `json:"kind"`
	Number string `json:"number"`
}

// Source is Microsoft 365 or Google Workspace.
type Source interface {
	// People lists the organisation's people and shared contacts.
	People(ctx context.Context) ([]Person, error)
	// Busy gives each email's busy times between from and to; problems names those it could not read.
	Busy(ctx context.Context, emails []string, from, to time.Time) (busy map[string][]calendar.Busy, problems map[string]string, err error)
	// Name is how screens name it.
	Name() string
}

var client = &http.Client{Timeout: 30 * time.Second}

// maxPages bounds a listing (1,000 people a page): a runaway paging link stops here.
const maxPages = 200

// token is a cached access token.
type token struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (t *token) get(fetch func() (string, time.Duration, error)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.value != "" && time.Until(t.expires) > time.Minute {
		return t.value, nil
	}
	v, life, err := fetch()
	if err != nil {
		return "", err
	}
	t.value, t.expires = v, time.Now().Add(life)
	return v, nil
}

// tokenAnswer reads an OAuth token endpoint's answer, or its error in plain words.
func tokenAnswer(resp *http.Response, advise func(code, desc string) string) (string, time.Duration, error) {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &t)
	if resp.StatusCode != 200 || t.AccessToken == "" {
		if t.Error == "" {
			t.Error = resp.Status
		}
		return "", 0, errors.New(advise(t.Error, t.Description))
	}
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	return t.AccessToken, time.Duration(t.ExpiresIn) * time.Second, nil
}

// getJSON sends a request with the bearer token and decodes its JSON answer into v; a failure comes
// back with the service's own message.
func getJSON(ctx context.Context, method, u, bearer string, body any, v any, advise func(status int, code, msg string) string) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s could not be reached: %w", hostOf(u), err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code    any    `json:"code"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		code := e.Error.Status
		if s, ok := e.Error.Code.(string); ok && s != "" {
			code = s
		}
		return errors.New(advise(resp.StatusCode, code, e.Error.Message))
	}
	return json.Unmarshal(b, v)
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Host
}

// sameHost says whether a paging link stays on the service it came from, so a token is never sent
// anywhere else.
func sameHost(next, base string) bool {
	a, err1 := url.Parse(next)
	b, err2 := url.Parse(base)
	return err1 == nil && err2 == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

// numberKind maps a service's kind of phone number to BitVoice's.
func numberKind(k string) string {
	switch strings.ToLower(k) {
	case "mobile", "cell", "mobilephone", "workmobile":
		return "mobile"
	case "home", "homefax":
		return "home"
	case "business", "work", "businessphones", "main", "company", "":
		return "work"
	}
	return "other"
}

// cleanNumber keeps the characters a phone dials.
func cleanNumber(n string) string {
	var b strings.Builder
	for i, r := range strings.TrimSpace(n) {
		switch {
		case r >= '0' && r <= '9', r == '*', r == '#':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func addNumber(p *Person, kind, n string) {
	n = cleanNumber(n)
	if len(n) < 3 {
		return
	}
	for _, x := range p.Numbers {
		if x.Number == n {
			return
		}
	}
	p.Numbers = append(p.Numbers, Number{Kind: numberKind(kind), Number: n})
}
