package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrIgnore marks a callback that is not a received text (a delivery receipt, another event): the
// provider gets 200 and nothing is stored.
var ErrIgnore = errors.New("sms: not a received text")

// Vonage is Vonage's SMS API: the API key and secret in the request; received texts come to the
// inbound webhook as a form or query.
type Vonage struct {
	Base, Username, Key string
	Client              *http.Client
}

func (v *Vonage) Send(ctx context.Context, from, to, text string) (string, error) {
	form := url.Values{"api_key": {v.Username}, "api_secret": {v.Key}, "from": {digits(from)}, "to": {digits(to)}, "text": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.Base+"/sms/json", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		Messages []struct {
			Status    string `json:"status"`
			ID        string `json:"message-id"`
			ErrorText string `json:"error-text"`
		} `json:"messages"`
	}
	if err := call(v.Client, req, "Vonage", &out, nil); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 {
		return "", errors.New("Vonage sent nothing back")
	}
	if m := out.Messages[0]; m.Status != "0" {
		if m.Status == "4" {
			return "", errors.New("Vonage refused the account details")
		}
		return "", fmt.Errorf("Vonage: %s", m.ErrorText)
	}
	return out.Messages[0].ID, nil
}

func (v *Vonage) Inbound(r *http.Request) (Inbound, error) {
	if err := r.ParseForm(); err != nil {
		return Inbound{}, err
	}
	f := r.Form
	if f.Get("msisdn") == "" {
		var in map[string]string // the JSON form of the same webhook
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && readJSON(r, &in) == nil {
			for k, val := range in {
				f.Set(k, val)
			}
		}
	}
	if f.Get("msisdn") == "" {
		return Inbound{}, fmt.Errorf("not a Vonage message: %w", errNoSender)
	}
	at := time.Now().UTC()
	if t, err := time.Parse("2006-01-02 15:04:05", f.Get("message-timestamp")); err == nil {
		at = t
	}
	return Inbound{ID: f.Get("messageId"), From: plus(f.Get("msisdn")), To: plus(f.Get("to")), Text: f.Get("text"), At: at}, nil
}

// VoIPms is VoIP.ms's REST API (sendSMS): the API user name and API password in the query, and the
// number as ten digits (North America). Received texts come to the SMS URL callback, set in the
// VoIP.ms portal as ...?from={FROM}&to={TO}&message={MESSAGE}&id={ID}.
type VoIPms struct {
	Base, Username, Key string
	Client              *http.Client
}

func nanp(n string) string {
	d := digits(n)
	if len(d) == 11 && d[0] == '1' {
		return d[1:]
	}
	return d
}

func (v *VoIPms) Send(ctx context.Context, from, to, text string) (string, error) {
	q := url.Values{"api_username": {v.Username}, "api_password": {v.Key}, "method": {"sendSMS"}, "did": {nanp(from)}, "dst": {nanp(to)}, "message": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.Base+"/api/v1/rest.php?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Status string          `json:"status"`
		SMS    json.RawMessage `json:"sms"`
	}
	if err := call(v.Client, req, "VoIP.ms", &out, nil); err != nil {
		return "", err
	}
	switch out.Status {
	case "success":
		return strings.Trim(string(out.SMS), `"`), nil
	case "invalid_credentials", "ip_not_enabled", "api_not_enabled":
		return "", fmt.Errorf("VoIP.ms refused the request (%s): check the API password and the allowed address in the portal", out.Status)
	}
	return "", fmt.Errorf("VoIP.ms: %s", out.Status)
}

func (v *VoIPms) Inbound(r *http.Request) (Inbound, error) {
	if err := r.ParseForm(); err != nil {
		return Inbound{}, err
	}
	f := r.Form
	if f.Get("from") == "" {
		return Inbound{}, fmt.Errorf("not a VoIP.ms message: %w", errNoSender)
	}
	us := func(n string) string { // VoIP.ms sends ten digits
		if d := digits(n); len(d) == 10 {
			return "+1" + d
		}
		return plus(n)
	}
	return Inbound{ID: f.Get("id"), From: us(f.Get("from")), To: us(f.Get("to")), Text: f.Get("message"), At: time.Now().UTC()}, nil
}

// Webhook is any texting service, or your own gateway: BitVoice posts {"from","to","text"} as JSON
// to the address (with the key as a bearer token when set) and expects {"id"} back; received texts
// are posted to BitVoice the same way, {"id","from","to","text"}.
type Webhook struct {
	URL, Key string
	Client   *http.Client
}

func (w *Webhook) Send(ctx context.Context, from, to, text string) (string, error) {
	var out struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	err := postJSON(ctx, w.Client, "The texting webhook", w.URL, "", map[string]string{"from": from, "to": to, "text": text},
		func(r *http.Request) {
			if w.Key != "" {
				r.Header.Set("Authorization", "Bearer "+w.Key)
			}
		}, &out, func(b []byte) string { _ = json.Unmarshal(b, &out); return out.Error })
	return out.ID, err
}

func (w *Webhook) Inbound(r *http.Request) (Inbound, error) {
	var in struct{ ID, From, To, Text string }
	if err := readJSON(r, &in); err != nil || in.From == "" {
		return Inbound{}, errors.New(`not a text: post {"from","to","text"} as JSON`)
	}
	return Inbound{ID: in.ID, From: plus(in.From), To: plus(in.To), Text: in.Text, At: time.Now().UTC()}, nil
}
