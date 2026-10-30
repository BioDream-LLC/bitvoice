package workspace

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/calendar"
)

// Microsoft is Microsoft 365 through Microsoft Graph, as an app registered in the organisation's
// Entra ID with application permissions (admin consent): User.Read.All and OrgContact.Read.All for
// people and shared contacts, Calendars.ReadBasic for busy times.
type Microsoft struct {
	Tenant, ClientID, Secret string
	// Login and Graph are the services' addresses; empty: Microsoft's public cloud.
	Login, Graph string
	tok          token
}

func (m *Microsoft) Name() string { return "Microsoft 365" }

func (m *Microsoft) login() string {
	if m.Login != "" {
		return strings.TrimRight(m.Login, "/")
	}
	return "https://login.microsoftonline.com"
}

func (m *Microsoft) graph() string {
	if m.Graph != "" {
		return strings.TrimRight(m.Graph, "/")
	}
	return "https://graph.microsoft.com"
}

func (m *Microsoft) token(ctx context.Context) (string, error) {
	return m.tok.get(func() (string, time.Duration, error) {
		form := url.Values{"client_id": {m.ClientID}, "client_secret": {m.Secret}, "grant_type": {"client_credentials"},
			"scope": {"https://graph.microsoft.com/.default"}}
		req, err := http.NewRequestWithContext(ctx, "POST", m.login()+"/"+url.PathEscape(m.Tenant)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, err
		}
		return tokenAnswer(resp, func(code, desc string) string {
			switch {
			case strings.Contains(desc, "AADSTS90002"), strings.Contains(desc, "AADSTS900023"):
				return "Microsoft does not know that tenant: give the directory (tenant) ID of your Entra ID"
			case strings.Contains(desc, "AADSTS700016"):
				return "Microsoft knows no app with that application (client) ID in this tenant"
			case strings.Contains(desc, "AADSTS7000215"), strings.Contains(desc, "AADSTS7000222"):
				return "Microsoft refused the client secret: it is wrong or has expired (make a new one under Certificates & secrets)"
			}
			return "Microsoft refused the sign-in: " + firstLine(code+": "+desc)
		})
	})
}

func (m *Microsoft) advise(status int, code, msg string) string {
	switch {
	case status == 403 || code == "Authorization_RequestDenied" || code == "ErrorAccessDenied":
		return "Microsoft Graph refused (" + firstLine(msg) + "): give the app the application permissions User.Read.All, OrgContact.Read.All and Calendars.ReadBasic, then Grant admin consent"
	case status == 401:
		return "Microsoft Graph did not accept the token: " + firstLine(msg)
	case status == 429:
		return "Microsoft Graph asks BitVoice to slow down; it tries again later"
	}
	return "Microsoft Graph answered " + http.StatusText(status) + ": " + firstLine(code+" "+msg)
}

// list reads every page of a Graph collection.
func (m *Microsoft) list(ctx context.Context, path string, each func(raw map[string]any)) error {
	tok, err := m.token(ctx)
	if err != nil {
		return err
	}
	next := m.graph() + path
	for page := 0; next != "" && page < maxPages; page++ {
		var out struct {
			Value []map[string]any `json:"value"`
			Next  string           `json:"@odata.nextLink"`
		}
		if err := getJSON(ctx, "GET", next, tok, nil, &out, m.advise); err != nil {
			return err
		}
		for _, v := range out.Value {
			each(v)
		}
		next = out.Next
		if next != "" && !sameHost(next, m.graph()) {
			return errorf("Microsoft Graph's next page is on another server (%s): not followed", hostOf(next))
		}
	}
	return nil
}

func (m *Microsoft) People(ctx context.Context) ([]Person, error) {
	var out []Person
	err := m.list(ctx, "/v1.0/users?$select=id,displayName,mail,userPrincipalName,businessPhones,mobilePhone,department,jobTitle,companyName,accountEnabled&$top=999",
		func(u map[string]any) {
			if on, ok := u["accountEnabled"].(bool); ok && !on {
				return
			}
			p := Person{ID: str(u["id"]), Name: str(u["displayName"]), Email: firstNonEmpty(str(u["mail"]), str(u["userPrincipalName"])),
				Company: str(u["companyName"]), Department: str(u["department"]), Title: str(u["jobTitle"])}
			for _, n := range list(u["businessPhones"]) {
				addNumber(&p, "work", str(n))
			}
			addNumber(&p, "mobile", str(u["mobilePhone"]))
			if p.Name != "" {
				out = append(out, p)
			}
		})
	if err != nil {
		return nil, err
	}
	// Shared contacts: the organisation's contacts in Exchange (orgContact).
	err = m.list(ctx, "/v1.0/contacts?$select=id,displayName,mail,phones,companyName,department,jobTitle&$top=999", func(c map[string]any) {
		p := Person{ID: str(c["id"]), Name: str(c["displayName"]), Email: str(c["mail"]), Company: str(c["companyName"]),
			Department: str(c["department"]), Title: str(c["jobTitle"]), Shared: true}
		for _, n := range list(c["phones"]) {
			if x, ok := n.(map[string]any); ok {
				addNumber(&p, str(x["type"]), str(x["number"]))
			}
		}
		if p.Name != "" {
			out = append(out, p)
		}
	})
	return out, err
}

// graphTime reads Graph's dateTimeTimeZone in UTC (no Prefer: outlook.timezone is sent).
func graphTime(v any) (time.Time, bool) {
	m, _ := v.(map[string]any)
	s := str(m["dateTime"])
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05.9999999", s, time.UTC)
	return t, err == nil
}

func (m *Microsoft) Busy(ctx context.Context, emails []string, from, to time.Time) (map[string][]calendar.Busy, map[string]string, error) {
	busy, problems := map[string][]calendar.Busy{}, map[string]string{}
	if len(emails) == 0 {
		return busy, problems, nil
	}
	tok, err := m.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	const batch = 50
	for i := 0; i < len(emails); i += batch {
		part := emails[i:min(i+batch, len(emails))]
		body := map[string]any{"schedules": part, "availabilityViewInterval": 15,
			"startTime": map[string]string{"dateTime": from.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
			"endTime":   map[string]string{"dateTime": to.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"}}
		var out struct {
			Value []struct {
				ID    string `json:"scheduleId"`
				Items []struct {
					Status  string `json:"status"`
					Subject string `json:"subject"`
					Private bool   `json:"isPrivate"`
					Start   any    `json:"start"`
					End     any    `json:"end"`
				} `json:"scheduleItems"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"value"`
		}
		// Asked of the first mailbox's calendar: with application permissions any mailbox may ask.
		if err := getJSON(ctx, "POST", m.graph()+"/v1.0/users/"+url.PathEscape(part[0])+"/calendar/getSchedule", tok, body, &out, m.advise); err != nil {
			return nil, nil, err
		}
		for _, s := range out.Value {
			key := strings.ToLower(s.ID)
			if s.Error != nil {
				problems[key] = "Microsoft 365: " + firstLine(s.Error.Message)
				continue
			}
			for _, it := range s.Items {
				st := strings.ToLower(it.Status)
				if st != "busy" && st != "oof" {
					continue // free, tentative, working elsewhere: the phone rings
				}
				a, ok1 := graphTime(it.Start)
				b, ok2 := graphTime(it.End)
				if !ok1 || !ok2 {
					continue
				}
				summary := it.Subject
				if it.Private {
					summary = ""
				}
				busy[key] = append(busy[key], calendar.Busy{Start: a, End: b, Summary: summary, Away: st == "oof"})
			}
		}
	}
	return busy, problems, nil
}
