// Package sms sends and receives text messages through texting providers. Each provider is behind
// the Provider interface, so any number of them can run at once; each of our numbers names the
// provider it belongs to.
package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider is a texting service.
type Provider interface {
	// Send sends text from one of our numbers to another number and returns the provider's id.
	Send(ctx context.Context, from, to, text string) (string, error)
	// Inbound reads the provider's callback for a received message.
	Inbound(r *http.Request) (Inbound, error)
}

// Inbound is a received message as the provider reported it.
type Inbound struct {
	ID   string
	From string // the sender
	To   string // our number
	Text string
	At   time.Time
}

// Kind describes a provider for the settings screen: what its two account fields are called and
// whether it needs the account id, application id or an address.
type Kind struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"` // labels; empty when not asked
	Key      string `json:"key"`
	Account  string `json:"account,omitempty"`
	App      string `json:"app,omitempty"`
	Base     string `json:"base,omitempty"`
	// BaseRequired: the address is the provider's own (a SignalWire space, a webhook), not a sandbox.
	BaseRequired bool   `json:"baseRequired,omitempty"`
	Help         string `json:"help"`
}

// Kinds are the providers BitVoice speaks to.
var Kinds = []Kind{
	{ID: "textmagic", Name: "TextMagic", Username: "User name", Key: "API key", Help: "Settings → API in the TextMagic portal."},
	{ID: "twilio", Name: "Twilio", Username: "Account SID", Key: "Auth token", Help: "The Twilio console's Account Info."},
	{ID: "signalwire", Name: "SignalWire", Username: "Project ID", Key: "API token", Base: "Space address (https://example.signalwire.com)", BaseRequired: true, Help: "API in the SignalWire space."},
	{ID: "telnyx", Name: "Telnyx", Key: "API key", Help: "Auth → API keys in the Telnyx portal; set the messaging profile's webhook to the address below."},
	{ID: "bandwidth", Name: "Bandwidth", Username: "API user", Key: "API password", Account: "Account ID", App: "Messaging application ID", Help: "Bandwidth Dashboard: an API user, and the messaging application whose callback is the address below."},
	{ID: "vonage", Name: "Vonage", Username: "API key", Key: "API secret", Help: "The Vonage API dashboard; set the number's inbound webhook to the address below."},
	{ID: "plivo", Name: "Plivo", Username: "Auth ID", Key: "Auth token", Help: "The Plivo console overview; set the application's message URL to the address below."},
	{ID: "flowroute", Name: "Flowroute", Username: "Access key", Key: "Secret key", Help: "Preferences → API control in Flowroute Manager; set the SMS callback to the address below."},
	{ID: "voipms", Name: "VoIP.ms", Username: "API user name (your email)", Key: "API password", Help: "Main menu → SOAP and REST/JSON API: set an API password and allow this server's address. Set the DID's SMS URL callback to the address below with ?from={FROM}&to={TO}&message={MESSAGE}&id={ID}."},
	{ID: "webhook", Name: "Your own webhook", Key: "Bearer token (optional)", Base: "Address BitVoice posts texts to", BaseRequired: true, Help: `BitVoice posts {"from","to","text"} as JSON and expects {"id"}; post received texts to the address below the same way.`},
}

// KindOf finds a provider kind.
func KindOf(id string) (Kind, bool) {
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// New makes the provider of a kind. base overrides the provider's API address (a sandbox, tests).
func New(kind, username, key, base string) (Provider, error) {
	return NewWith(kind, username, key, base, "", "")
}

// NewWith is New with a provider's account id and application id (Bandwidth).
func NewWith(kind, username, key, base, account, app string) (Provider, error) {
	k, ok := KindOf(kind)
	if !ok {
		return nil, fmt.Errorf("sms: no provider %q", kind)
	}
	if k.BaseRequired && base == "" {
		return nil, fmt.Errorf("%s needs its address", k.Name)
	}
	or := func(def string) string {
		if base != "" {
			return strings.TrimRight(base, "/")
		}
		return def
	}
	c := &http.Client{Timeout: 15 * time.Second}
	switch kind {
	case "textmagic":
		return &TextMagic{Base: or("https://rest.textmagic.com"), Username: username, Key: key, Client: c}, nil
	case "twilio":
		return &Twilio{Name: "Twilio", Base: or("https://api.twilio.com"), Path: "/2010-04-01", Username: username, Key: key, Client: c}, nil
	case "signalwire":
		return &Twilio{Name: "SignalWire", Base: or(""), Path: "/api/laml/2010-04-01", Username: username, Key: key, Client: c}, nil
	case "telnyx":
		return &Telnyx{Base: or("https://api.telnyx.com"), Key: key, Client: c}, nil
	case "bandwidth":
		if account == "" || app == "" {
			return nil, errors.New("Bandwidth needs the account ID and the messaging application ID")
		}
		return &Bandwidth{Base: or("https://messaging.bandwidth.com"), Username: username, Key: key, Account: account, App: app, Client: c}, nil
	case "vonage":
		return &Vonage{Base: or("https://rest.nexmo.com"), Username: username, Key: key, Client: c}, nil
	case "plivo":
		return &Plivo{Base: or("https://api.plivo.com"), Username: username, Key: key, Client: c}, nil
	case "flowroute":
		return &Flowroute{Base: or("https://api.flowroute.com"), Username: username, Key: key, Client: c}, nil
	case "voipms":
		return &VoIPms{Base: or("https://voip.ms"), Username: username, Key: key, Client: c}, nil
	case "webhook":
		return &Webhook{URL: strings.TrimSpace(base), Key: key, Client: c}, nil
	}
	return nil, fmt.Errorf("sms: no provider %q", kind)
}

// TextMagic is the TextMagic REST API (v2): Basic authentication with the account's user name and an
// API key; replies come to a callback URL as JSON.
type TextMagic struct {
	Base, Username, Key string
	Client              *http.Client
}

// Send posts one message.
func (t *TextMagic) Send(ctx context.Context, from, to, text string) (string, error) {
	body, _ := json.Marshal(map[string]string{"text": text, "phones": strings.TrimPrefix(to, "+"), "from": strings.TrimPrefix(from, "+")})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+"/api/v2/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(t.Username, t.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("TextMagic could not be reached: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
			Errors  struct {
				Fields map[string][]string `json:"fields"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(b, &e)
		msg := e.Message
		for f, list := range e.Errors.Fields {
			msg += "; " + f + ": " + strings.Join(list, ", ")
		}
		if msg == "" {
			msg = resp.Status
		}
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "TextMagic refused the user name or API key"
		}
		return "", errors.New(msg)
	}
	var out struct {
		ID        int64 `json:"id"`
		MessageID int64 `json:"messageId"`
	}
	_ = json.Unmarshal(b, &out)
	id := out.MessageID
	if id == 0 {
		id = out.ID
	}
	return fmt.Sprint(id), nil
}

// Inbound reads TextMagic's callback for a received message.
func (t *TextMagic) Inbound(r *http.Request) (Inbound, error) {
	var in struct {
		ID          json.Number `json:"id"`
		Sender      string      `json:"sender"`
		Receiver    string      `json:"receiver"`
		MessageTime string      `json:"messageTime"`
		Text        string      `json:"text"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.UseNumber()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return Inbound{}, err
		}
		in.ID, in.Sender, in.Receiver, in.MessageTime, in.Text = json.Number(r.PostForm.Get("id")), r.PostForm.Get("sender"), r.PostForm.Get("receiver"), r.PostForm.Get("messageTime"), r.PostForm.Get("text")
	} else if err := dec.Decode(&in); err != nil {
		return Inbound{}, fmt.Errorf("not a TextMagic message: %w", err)
	}
	if in.Sender == "" {
		return Inbound{}, errors.New("not a TextMagic message: no sender")
	}
	at := time.Now()
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} { // TextMagic writes +0000
		if x, err := time.Parse(layout, in.MessageTime); err == nil {
			at = x
			break
		}
	}
	return Inbound{ID: in.ID.String(), From: plus(in.Sender), To: plus(in.Receiver), Text: in.Text, At: at.UTC()}, nil
}

func plus(n string) string {
	n = strings.TrimSpace(n)
	if n != "" && !strings.HasPrefix(n, "+") {
		return "+" + n
	}
	return n
}

// Parts is how many SMS a text takes: 160 characters in one (153 each when split), or 70 (67) when
// it holds characters outside the GSM alphabet.
func Parts(text string) int {
	n, single, multi := 0, 160, 153
	for _, r := range text {
		n++
		if r > 0x7e || r < 0x20 && r != '\n' && r != '\r' {
			single, multi = 70, 67
		}
	}
	if n <= single {
		return 1
	}
	return (n + multi - 1) / multi
}
