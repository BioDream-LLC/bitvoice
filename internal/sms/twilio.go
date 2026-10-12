package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Twilio is Twilio's Messages API (also SignalWire's compatibility API, at the space's address):
// Basic authentication with the account SID (project id) and auth token; received texts come to
// the callback as a form.
type Twilio struct {
	Name, Base, Path, Username, Key string
	Client                          *http.Client
}

// Send posts one message.
func (t *Twilio) Send(ctx context.Context, from, to, text string) (string, error) {
	form := url.Values{"From": {from}, "To": {to}, "Body": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+t.Path+"/Accounts/"+url.PathEscape(t.Username)+"/Messages.json", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(t.Username, t.Key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		SID     string `json:"sid"`
		Message string `json:"message"`
	}
	if err := call(t.Client, req, t.Name, &out, func(b []byte) string { _ = json.Unmarshal(b, &out); return out.Message }); err != nil {
		return "", err
	}
	return out.SID, nil
}

// Inbound reads the form Twilio posts for a received message.
func (t *Twilio) Inbound(r *http.Request) (Inbound, error) {
	if err := r.ParseForm(); err != nil {
		return Inbound{}, err
	}
	f := r.PostForm
	if f.Get("From") == "" {
		return Inbound{}, fmt.Errorf("not a %s message: no sender", t.Name)
	}
	return Inbound{ID: f.Get("MessageSid"), From: plus(f.Get("From")), To: plus(f.Get("To")), Text: f.Get("Body"), At: time.Now().UTC()}, nil
}

// call sends a request and decodes a JSON answer into out; on an error status, explain reads the
// provider's own words from the body.
func call(c *http.Client, req *http.Request, name string, out any, explain func([]byte) string) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%s could not be reached: %w", name, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s refused the account details", name)
	}
	if resp.StatusCode/100 != 2 {
		msg := ""
		if explain != nil {
			msg = explain(b)
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("%s answered something unexpected: %w", name, err)
		}
	}
	return nil
}

func digits(n string) string { return strings.TrimPrefix(strings.TrimSpace(n), "+") }

var errNoSender = errors.New("no sender")
