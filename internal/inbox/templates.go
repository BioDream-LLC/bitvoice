package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A Template is a WhatsApp message template approved in WhatsApp Manager: the only kind of message
// WhatsApp lets a business send more than 24 hours after the customer last wrote.
type Template struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Status   string `json:"status"`   // APPROVED, PENDING, REJECTED, PAUSED, DISABLED
	Category string `json:"category"` // MARKETING, UTILITY, AUTHENTICATION
	Header   string `json:"header,omitempty"`
	Body     string `json:"body"`
	Footer   string `json:"footer,omitempty"`
	// Params is how many {{1}}, {{2}} ... the body holds, each filled in when it is sent.
	Params int `json:"params"`
	// Usable is false for templates this inbox cannot fill (a picture or document header, a
	// variable header, buttons with a variable link, named parameters); Why says which.
	Usable bool   `json:"usable"`
	Why    string `json:"why,omitempty"`
}

var paramRe = regexp.MustCompile(`\{\{\s*(\d+)\s*\}\}`)
var namedRe = regexp.MustCompile(`\{\{\s*[A-Za-z_][A-Za-z0-9_]*\s*\}\}`)

type rawTemplate struct {
	Name       string `json:"name"`
	Language   string `json:"language"`
	Status     string `json:"status"`
	Category   string `json:"category"`
	Components []struct {
		Type    string `json:"type"`
		Format  string `json:"format"`
		Text    string `json:"text"`
		Buttons []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"buttons"`
	} `json:"components"`
}

func (r rawTemplate) template() Template {
	t := Template{Name: r.Name, Language: r.Language, Status: r.Status, Category: r.Category, Usable: r.Status == "APPROVED"}
	if !t.Usable {
		t.Why = "not approved (" + strings.ToLower(r.Status) + ")"
	}
	no := func(why string) {
		if t.Usable {
			t.Usable, t.Why = false, why
		}
	}
	for _, c := range r.Components {
		switch strings.ToUpper(c.Type) {
		case "HEADER":
			if f := strings.ToUpper(c.Format); f != "" && f != "TEXT" {
				kinds := map[string]string{"IMAGE": "a picture", "VIDEO": "a video", "DOCUMENT": "a document", "LOCATION": "a location"}
				k := kinds[f]
				if k == "" {
					k = strings.ToLower(f)
				}
				no("its header is " + k)
			}
			t.Header = c.Text
			if paramRe.MatchString(c.Text) || namedRe.MatchString(c.Text) {
				no("its header has a variable")
			}
		case "BODY":
			t.Body = c.Text
			for _, m := range paramRe.FindAllStringSubmatch(c.Text, -1) {
				if n, _ := strconv.Atoi(m[1]); n > t.Params {
					t.Params = n
				}
			}
			if namedRe.MatchString(c.Text) {
				no("it uses named parameters")
			}
		case "FOOTER":
			t.Footer = c.Text
		case "BUTTONS":
			for _, b := range c.Buttons {
				if strings.ToUpper(b.Type) == "URL" && paramRe.MatchString(b.URL) {
					no("a button's link has a variable")
				}
				if strings.ToUpper(b.Type) == "COPY_CODE" || strings.ToUpper(b.Type) == "OTP" {
					no("it has a code button")
				}
			}
		}
	}
	return t
}

// Fill puts the parameters into the template's text, as the customer will read it.
func (t Template) Fill(params []string) string {
	f := func(s string) string {
		return paramRe.ReplaceAllStringFunc(s, func(m string) string {
			n, _ := strconv.Atoi(paramRe.FindStringSubmatch(m)[1])
			if n >= 1 && n <= len(params) {
				return params[n-1]
			}
			return m
		})
	}
	parts := []string{}
	for _, s := range []string{t.Header, f(t.Body), t.Footer} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// WhatsAppTemplates lists the business account's message templates (every page of them, up to a
// thousand).
func WhatsAppTemplates(ctx context.Context, businessAccountID, token string) ([]Template, error) {
	if businessAccountID == "" {
		return nil, errors.New("give the WhatsApp business account ID to list its templates")
	}
	next := GraphBase + "/" + url.PathEscape(businessAccountID) + "/message_templates?limit=200&fields=name,language,status,category,components"
	var out []Template
	for page := 0; next != "" && page < 5; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			err := graphError(resp)
			resp.Body.Close()
			return nil, err
		}
		var r struct {
			Data   []rawTemplate `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, t := range r.Data {
			out = append(out, t.template())
		}
		next = ""
		// Only follow pages on Meta's own API, never an address a reply names elsewhere.
		if strings.HasPrefix(r.Paging.Next, GraphBase+"/") {
			next = r.Paging.Next
		}
	}
	return out, nil
}

// SendWhatsAppTemplate sends an approved template, its body's {{1}}, {{2}} ... filled with params.
func SendWhatsAppTemplate(ctx context.Context, phoneNumberID, token, to string, t Template, params []string) (string, error) {
	// policy "deterministic" is the only value Meta's specification allows, and it requires it.
	tpl := map[string]any{"name": t.Name, "language": map[string]string{"policy": "deterministic", "code": t.Language}}
	if len(params) > 0 {
		ps := make([]map[string]string, len(params))
		for i, p := range params {
			ps[i] = map[string]string{"type": "text", "text": p}
		}
		tpl["components"] = []map[string]any{{"type": "body", "parameters": ps}}
	}
	return post(ctx, GraphBase+"/"+url.PathEscape(phoneNumberID)+"/messages", token, map[string]any{
		"messaging_product": "whatsapp", "recipient_type": "individual", "to": strings.TrimPrefix(to, "+"),
		"type": "template", "template": tpl})
}

// SendMessengerTagged sends a human agent's reply after the 24-hour window, within 7 days of the
// person's last message (Meta's HUMAN_AGENT tag; the app needs Meta's Human Agent permission).
func SendMessengerTagged(ctx context.Context, pageToken, to, text string) (string, error) {
	return post(ctx, GraphBase+"/me/messages?access_token="+url.QueryEscape(pageToken), "", map[string]any{
		"recipient": map[string]string{"id": to}, "messaging_type": "MESSAGE_TAG", "tag": "HUMAN_AGENT", "message": map[string]string{"text": text}})
}

// MessengerWindow says which kind of reply Messenger takes now: "open" (any reply, 24 hours),
// "agent" (a human agent's, tagged, up to 7 days) or "shut".
func MessengerWindow(lastIn time.Time) string {
	switch d := time.Since(lastIn); {
	case d < 24*time.Hour:
		return "open"
	case d < 7*24*time.Hour:
		return "agent"
	}
	return "shut"
}
