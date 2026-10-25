// Package inbox carries the messaging channels BitVoice answers beside calls and texts: WhatsApp
// (Meta's Cloud API), Facebook Messenger and the web chat on your own site. Meta's webhooks are
// checked with the app secret (X-Hub-Signature-256) before anything in them is believed.
package inbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GraphBase is Meta's Graph API; tests point it elsewhere. Meta retires a version two years after the
// next one comes out (v23.0 is served until mid 2027): move it on with each new one.
var GraphBase = "https://graph.facebook.com/v23.0"

// UserAgent names BitVoice to Meta as its specification asks, NAME/MAJOR.MINOR.PATCH; main sets the
// release number.
var UserAgent = "BitVoice/0.0.0"

// SetVersion puts a release number such as v0.1.12 into UserAgent (anything else keeps 0.0.0).
func SetVersion(v string) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return
	}
	for _, x := range parts {
		if x == "" || strings.Trim(x, "0123456789") != "" {
			return
		}
	}
	UserAgent = "BitVoice/" + v
}

var client = &http.Client{Timeout: 20 * time.Second}

// Incoming is one message a channel received.
type Incoming struct {
	Channel string // whatsapp or messenger
	From    string // the WhatsApp number, or the Messenger user's page-scoped ID
	Name    string // the profile name, when the channel gives it
	ID      string // the channel's message ID
	Text    string
	At      time.Time
	To      string // our WhatsApp phone number ID, or our page ID
}

// VerifySignature checks a Meta webhook body against X-Hub-Signature-256 with the app secret.
func VerifySignature(body []byte, header, appSecret string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || appSecret == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// Challenge answers Meta's webhook verification (GET with hub.mode, hub.verify_token, hub.challenge):
// the challenge back when the token matches, else "".
func Challenge(q url.Values, verifyToken string) string {
	if q.Get("hub.mode") != "subscribe" || verifyToken == "" || !hmac.Equal([]byte(q.Get("hub.verify_token")), []byte(verifyToken)) {
		return ""
	}
	return q.Get("hub.challenge")
}

// ParseWhatsApp reads the messages in a WhatsApp Cloud API webhook (text ones; others are noted by kind).
func ParseWhatsApp(body []byte) ([]Incoming, error) {
	var w struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Metadata struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata"`
					Contacts []struct {
						Profile struct {
							Name string `json:"name"`
						} `json:"profile"`
						WaID string `json:"wa_id"`
					} `json:"contacts"`
					Messages []struct {
						From      string `json:"from"`
						ID        string `json:"id"`
						Timestamp string `json:"timestamp"`
						Type      string `json:"type"`
						Text      struct {
							Body string `json:"body"`
						} `json:"text"`
						Button struct {
							Text string `json:"text"`
						} `json:"button"`
						Interactive struct {
							ButtonReply struct {
								Title string `json:"title"`
							} `json:"button_reply"`
							ListReply struct {
								Title string `json:"title"`
							} `json:"list_reply"`
						} `json:"interactive"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, err
	}
	var out []Incoming
	for _, e := range w.Entry {
		for _, ch := range e.Changes {
			v := ch.Value
			names := map[string]string{}
			for _, c := range v.Contacts {
				names[c.WaID] = c.Profile.Name
			}
			for _, m := range v.Messages {
				text := m.Text.Body
				switch m.Type {
				case "text":
				case "button":
					text = m.Button.Text
				case "interactive":
					text = m.Interactive.ButtonReply.Title + m.Interactive.ListReply.Title
				default:
					text = "[" + m.Type + "]" // a picture, a voice note, a location: shown as what it is
				}
				at := time.Now()
				var secs int64
				if _, err := fmt.Sscan(m.Timestamp, &secs); err == nil && secs > 0 {
					at = time.Unix(secs, 0)
				}
				out = append(out, Incoming{Channel: "whatsapp", From: "+" + strings.TrimPrefix(m.From, "+"), Name: names[m.From], ID: m.ID,
					Text: text, At: at, To: v.Metadata.PhoneNumberID})
			}
		}
	}
	return out, nil
}

// ParseMessenger reads the messages in a Messenger webhook (echoes of our own replies are left out).
func ParseMessenger(body []byte) ([]Incoming, error) {
	var w struct {
		Object string `json:"object"`
		Entry  []struct {
			ID        string `json:"id"`
			Messaging []struct {
				Sender struct {
					ID string `json:"id"`
				} `json:"sender"`
				Recipient struct {
					ID string `json:"id"`
				} `json:"recipient"`
				Timestamp int64 `json:"timestamp"`
				Message   *struct {
					Mid         string `json:"mid"`
					Text        string `json:"text"`
					IsEcho      bool   `json:"is_echo"`
					Attachments []struct {
						Type string `json:"type"`
					} `json:"attachments"`
				} `json:"message"`
				Postback *struct {
					Title string `json:"title"`
				} `json:"postback"`
			} `json:"messaging"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, err
	}
	var out []Incoming
	for _, e := range w.Entry {
		for _, m := range e.Messaging {
			at := time.Now()
			if m.Timestamp > 0 {
				at = time.UnixMilli(m.Timestamp)
			}
			switch {
			case m.Message != nil && !m.Message.IsEcho:
				text := m.Message.Text
				if text == "" && len(m.Message.Attachments) > 0 {
					text = "[" + m.Message.Attachments[0].Type + "]"
				}
				out = append(out, Incoming{Channel: "messenger", From: m.Sender.ID, ID: m.Message.Mid, Text: text, At: at, To: m.Recipient.ID})
			case m.Postback != nil:
				out = append(out, Incoming{Channel: "messenger", From: m.Sender.ID, Text: m.Postback.Title, At: at, To: m.Recipient.ID})
			}
		}
	}
	return out, nil
}

// graphError reads Meta's error answer.
func graphError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("%s (code %d)", e.Error.Message, e.Error.Code)
	}
	return errors.New("Meta answered " + resp.Status)
}

func post(ctx context.Context, u, token string, body any) (string, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", graphError(resp)
	}
	var r struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		MessageID string `json:"message_id"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&r)
	if len(r.Messages) > 0 {
		return r.Messages[0].ID, nil
	}
	return r.MessageID, nil
}

// SendWhatsApp sends a text from our WhatsApp number (its phone number ID) to a number. WhatsApp
// takes free-form text only within 24 hours of the customer's last message; after that it answers
// with an error, and a template must be used (from WhatsApp Manager).
func SendWhatsApp(ctx context.Context, phoneNumberID, token, to, text string) (string, error) {
	return post(ctx, GraphBase+"/"+url.PathEscape(phoneNumberID)+"/messages", token, map[string]any{
		"messaging_product": "whatsapp", "recipient_type": "individual", "to": strings.TrimPrefix(to, "+"),
		"type": "text", "text": map[string]any{"preview_url": false, "body": text}})
}

// SendMessenger sends a text from our page to a Messenger user, as a reply (messaging_type RESPONSE,
// allowed within 24 hours of their last message).
func SendMessenger(ctx context.Context, pageToken, to, text string) (string, error) {
	return post(ctx, GraphBase+"/me/messages?access_token="+url.QueryEscape(pageToken), "", map[string]any{
		"recipient": map[string]string{"id": to}, "messaging_type": "RESPONSE", "message": map[string]string{"text": text}})
}

// MessengerName asks Meta for a Messenger user's name (empty when it will not say).
func MessengerName(ctx context.Context, pageToken, psid string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GraphBase+"/"+url.PathEscape(psid)+"?fields=first_name,last_name&access_token="+url.QueryEscape(pageToken), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var r struct {
		First string `json:"first_name"`
		Last  string `json:"last_name"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&r) != nil {
		return ""
	}
	return strings.TrimSpace(r.First + " " + r.Last)
}
