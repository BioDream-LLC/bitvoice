package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func postJSON(ctx context.Context, c *http.Client, name, url, ctype string, body any, auth func(*http.Request), out any, explain func([]byte) string) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	if ctype == "" {
		ctype = "application/json"
	}
	req.Header.Set("Content-Type", ctype)
	auth(req)
	return call(c, req, name, out, explain)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(v)
}

func when(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

// Telnyx is Telnyx's Messaging API v2: a bearer API key; received texts come as a message.received
// webhook event.
type Telnyx struct {
	Base, Key string
	Client    *http.Client
}

func (t *Telnyx) Send(ctx context.Context, from, to, text string) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := postJSON(ctx, t.Client, "Telnyx", t.Base+"/v2/messages", "", map[string]string{"from": from, "to": to, "text": text},
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+t.Key) }, &out, func(b []byte) string {
			var e struct {
				Errors []struct {
					Title, Detail string
				} `json:"errors"`
			}
			if json.Unmarshal(b, &e) == nil && len(e.Errors) > 0 {
				return e.Errors[0].Title + ": " + e.Errors[0].Detail
			}
			return ""
		})
	return out.Data.ID, err
}

func (t *Telnyx) Inbound(r *http.Request) (Inbound, error) {
	var in struct {
		Data struct {
			EventType string `json:"event_type"`
			Payload   struct {
				ID   string `json:"id"`
				From struct {
					PhoneNumber string `json:"phone_number"`
				} `json:"from"`
				To []struct {
					PhoneNumber string `json:"phone_number"`
				} `json:"to"`
				Text       string `json:"text"`
				ReceivedAt string `json:"received_at"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := readJSON(r, &in); err != nil {
		return Inbound{}, fmt.Errorf("not a Telnyx event: %w", err)
	}
	p := in.Data.Payload
	if in.Data.EventType != "message.received" || p.From.PhoneNumber == "" {
		return Inbound{}, ErrIgnore
	}
	to := ""
	if len(p.To) > 0 {
		to = p.To[0].PhoneNumber
	}
	return Inbound{ID: p.ID, From: plus(p.From.PhoneNumber), To: plus(to), Text: p.Text, At: when(p.ReceivedAt)}, nil
}

// Bandwidth is Bandwidth's Messaging API v2: Basic authentication with the API user and password,
// the account id in the address and a messaging application id; received texts come as a
// message-received callback.
type Bandwidth struct {
	Base, Username, Key, Account, App string
	Client                            *http.Client
}

func (b *Bandwidth) Send(ctx context.Context, from, to, text string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := postJSON(ctx, b.Client, "Bandwidth", b.Base+"/api/v2/users/"+b.Account+"/messages", "",
		map[string]any{"from": from, "to": []string{to}, "text": text, "applicationId": b.App},
		func(r *http.Request) { r.SetBasicAuth(b.Username, b.Key) }, &out, func(x []byte) string {
			var e struct {
				Description string `json:"description"`
			}
			_ = json.Unmarshal(x, &e)
			return e.Description
		})
	return out.ID, err
}

func (b *Bandwidth) Inbound(r *http.Request) (Inbound, error) {
	var in []struct {
		Type    string `json:"type"`
		To      string `json:"to"`
		Message struct {
			ID   string   `json:"id"`
			From string   `json:"from"`
			To   []string `json:"to"`
			Text string   `json:"text"`
			Time string   `json:"time"`
		} `json:"message"`
	}
	if err := readJSON(r, &in); err != nil {
		return Inbound{}, fmt.Errorf("not a Bandwidth callback: %w", err)
	}
	for _, e := range in {
		if e.Type == "message-received" && e.Message.From != "" {
			to := e.To
			if to == "" && len(e.Message.To) > 0 {
				to = e.Message.To[0]
			}
			return Inbound{ID: e.Message.ID, From: plus(e.Message.From), To: plus(to), Text: e.Message.Text, At: when(e.Message.Time)}, nil
		}
	}
	return Inbound{}, ErrIgnore // delivery receipts
}

// Plivo is Plivo's Message API: Basic authentication with the auth id and auth token; received
// texts come to the message URL as a form.
type Plivo struct {
	Base, Username, Key string
	Client              *http.Client
}

func (p *Plivo) Send(ctx context.Context, from, to, text string) (string, error) {
	var out struct {
		UUID  []string `json:"message_uuid"`
		Error string   `json:"error"`
	}
	err := postJSON(ctx, p.Client, "Plivo", p.Base+"/v1/Account/"+p.Username+"/Message/", "",
		map[string]string{"src": digits(from), "dst": digits(to), "text": text},
		func(r *http.Request) { r.SetBasicAuth(p.Username, p.Key) }, &out, func(b []byte) string { _ = json.Unmarshal(b, &out); return out.Error })
	if err != nil || len(out.UUID) == 0 {
		return "", err
	}
	return out.UUID[0], nil
}

func (p *Plivo) Inbound(r *http.Request) (Inbound, error) {
	if err := r.ParseForm(); err != nil {
		return Inbound{}, err
	}
	f := r.Form
	if f.Get("From") == "" {
		return Inbound{}, fmt.Errorf("not a Plivo message: %w", errNoSender)
	}
	return Inbound{ID: f.Get("MessageUUID"), From: plus(f.Get("From")), To: plus(f.Get("To")), Text: f.Get("Text"), At: time.Now().UTC()}, nil
}

// Flowroute is Flowroute's Messaging API v2.1 (JSON:API): Basic authentication with the access key
// and secret key; received texts come as a JSON:API message.
type Flowroute struct {
	Base, Username, Key string
	Client              *http.Client
}

func (f *Flowroute) Send(ctx context.Context, from, to, text string) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	body := map[string]any{"data": map[string]any{"type": "message", "attributes": map[string]string{"to": digits(to), "from": digits(from), "body": text}}}
	err := postJSON(ctx, f.Client, "Flowroute", f.Base+"/v2.1/messages", "application/vnd.api+json", body,
		func(r *http.Request) { r.SetBasicAuth(f.Username, f.Key) }, &out, func(b []byte) string {
			var e struct {
				Errors []struct {
					Detail string `json:"detail"`
				} `json:"errors"`
			}
			if json.Unmarshal(b, &e) == nil && len(e.Errors) > 0 {
				return e.Errors[0].Detail
			}
			return ""
		})
	return out.Data.ID, err
}

func (f *Flowroute) Inbound(r *http.Request) (Inbound, error) {
	var in struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				From, To, Body, Timestamp string
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := readJSON(r, &in); err != nil || in.Data.Attributes.From == "" {
		return Inbound{}, fmt.Errorf("not a Flowroute message")
	}
	a := in.Data.Attributes
	return Inbound{ID: in.Data.ID, From: plus(a.From), To: plus(a.To), Text: a.Body, At: when(a.Timestamp)}, nil
}
