package workspace

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/calendar"
)

// Google is Google Workspace through a service account with domain-wide delegation: BitVoice signs
// in as the account, acting for a Workspace administrator, with the scopes directory.readonly
// (people and shared contacts, People API) and calendar.freebusy (busy times).
type Google struct {
	// Key is the service account's key file (JSON) as Google Cloud gives it.
	Key string
	// Admin is the Workspace administrator the account acts for.
	Admin string
	// TokenURL, PeopleURL and CalendarURL are the services' addresses; empty: Google's (the key
	// file's token_uri for the token).
	TokenURL, PeopleURL, CalendarURL string
	tok                              token
}

// Scopes are what the service account must be allowed in the Admin console (Security, API
// controls, Domain-wide delegation).
const GoogleScopes = "https://www.googleapis.com/auth/directory.readonly https://www.googleapis.com/auth/calendar.freebusy"

func (g *Google) Name() string { return "Google Workspace" }

type serviceKey struct {
	Type       string `json:"type"`
	Email      string `json:"client_email"`
	PrivateKey string `json:"private_key"`
	TokenURI   string `json:"token_uri"`
}

// ParseGoogleKey reads a service account key file, saying plainly what is wrong with it.
func ParseGoogleKey(s string) (serviceKey, *rsa.PrivateKey, error) {
	var k serviceKey
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &k); err != nil {
		return k, nil, errors.New("the key is not a service account key file (JSON from Google Cloud, IAM, Service accounts, Keys)")
	}
	if k.Type != "service_account" || k.Email == "" || k.PrivateKey == "" {
		return k, nil, errors.New("the key file is not a service account's (it needs type service_account, client_email and private_key)")
	}
	blk, _ := pem.Decode([]byte(k.PrivateKey))
	if blk == nil {
		return k, nil, errors.New("the key file's private_key is not a PEM key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		if r, err2 := x509.ParsePKCS1PrivateKey(blk.Bytes); err2 == nil {
			parsed = r
		} else {
			return k, nil, errors.New("the key file's private key cannot be read: " + err.Error())
		}
	}
	rk, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return k, nil, errors.New("the key file's private key is not an RSA key")
	}
	return k, rk, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (g *Google) token(ctx context.Context) (string, error) {
	return g.tok.get(func() (string, time.Duration, error) {
		k, key, err := ParseGoogleKey(g.Key)
		if err != nil {
			return "", 0, err
		}
		if g.Admin == "" {
			return "", 0, errors.New("give the email of a Workspace administrator the service account acts for")
		}
		tokenURL := firstNonEmpty(g.TokenURL, k.TokenURI, "https://oauth2.googleapis.com/token")
		now := time.Now()
		head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
		claims, _ := json.Marshal(map[string]any{"iss": k.Email, "sub": g.Admin, "scope": GoogleScopes, "aud": tokenURL,
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
		signing := b64(head) + "." + b64(claims)
		sum := sha256.Sum256([]byte(signing))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			return "", 0, err
		}
		form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signing + "." + b64(sig)}}
		req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, err
		}
		return tokenAnswer(resp, func(code, desc string) string {
			switch code {
			case "unauthorized_client":
				return "Google refused: the service account is not allowed these scopes for your domain (Admin console, Security, API controls, Domain-wide delegation: add its client ID with " + GoogleScopes + ")"
			case "invalid_grant":
				return "Google refused the sign-in (" + firstLine(desc) + "): check the administrator's email, and that the key has not been deleted"
			}
			return "Google refused the sign-in: " + firstLine(code+": "+desc)
		})
	})
}

func (g *Google) advise(status int, code, msg string) string {
	switch {
	case status == 403 && strings.Contains(msg, "has not been used"), code == "SERVICE_DISABLED":
		return "Google refused: switch on the People API and the Google Calendar API in the service account's Google Cloud project (" + firstLine(msg) + ")"
	case status == 403:
		return "Google refused (" + firstLine(msg) + "): check the domain-wide delegation scopes"
	case status == 429:
		return "Google asks BitVoice to slow down; it tries again later"
	}
	return fmt.Sprintf("Google answered %d: %s", status, firstLine(code+" "+msg))
}

func (g *Google) peopleBase() string {
	return strings.TrimRight(firstNonEmpty(g.PeopleURL, "https://people.googleapis.com"), "/")
}

func (g *Google) calendarBase() string {
	return strings.TrimRight(firstNonEmpty(g.CalendarURL, "https://www.googleapis.com"), "/")
}

func (g *Google) People(ctx context.Context) ([]Person, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, err
	}
	var out []Person
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{"readMask": {"names,emailAddresses,phoneNumbers,organizations"}, "pageSize": {"1000"},
			"sources": {"DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE", "DIRECTORY_SOURCE_TYPE_DOMAIN_CONTACT"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var res struct {
			People []struct {
				Resource string `json:"resourceName"`
				Names    []struct {
					Display string `json:"displayName"`
				} `json:"names"`
				Emails []struct {
					Value string `json:"value"`
				} `json:"emailAddresses"`
				Phones []struct {
					Value string `json:"value"`
					Type  string `json:"type"`
				} `json:"phoneNumbers"`
				Orgs []struct {
					Name, Department, Title string
				} `json:"organizations"`
				Metadata struct {
					Sources []struct {
						Type string `json:"type"`
					} `json:"sources"`
				} `json:"metadata"`
			} `json:"people"`
			Next string `json:"nextPageToken"`
		}
		if err := getJSON(ctx, "GET", g.peopleBase()+"/v1/people:listDirectoryPeople?"+q.Encode(), tok, nil, &res, g.advise); err != nil {
			return nil, err
		}
		for _, x := range res.People {
			p := Person{ID: x.Resource}
			if len(x.Names) > 0 {
				p.Name = x.Names[0].Display
			}
			if len(x.Emails) > 0 {
				p.Email = x.Emails[0].Value
			}
			if len(x.Orgs) > 0 {
				p.Company, p.Department, p.Title = x.Orgs[0].Name, x.Orgs[0].Department, x.Orgs[0].Title
			}
			for _, n := range x.Phones {
				addNumber(&p, n.Type, n.Value)
			}
			for _, s := range x.Metadata.Sources {
				p.Shared = p.Shared || s.Type == "DOMAIN_CONTACT"
			}
			if p.Name != "" {
				out = append(out, p)
			}
		}
		if res.Next == "" {
			return out, nil
		}
		pageToken = res.Next
	}
	return out, nil
}

func (g *Google) Busy(ctx context.Context, emails []string, from, to time.Time) (map[string][]calendar.Busy, map[string]string, error) {
	busy, problems := map[string][]calendar.Busy{}, map[string]string{}
	if len(emails) == 0 {
		return busy, problems, nil
	}
	tok, err := g.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	const batch = 50 // Google answers at most 50 calendars a query
	for i := 0; i < len(emails); i += batch {
		part := emails[i:min(i+batch, len(emails))]
		items := []map[string]string{}
		for _, e := range part {
			items = append(items, map[string]string{"id": e})
		}
		var res struct {
			Calendars map[string]struct {
				Busy []struct {
					Start time.Time `json:"start"`
					End   time.Time `json:"end"`
				} `json:"busy"`
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"calendars"`
		}
		body := map[string]any{"timeMin": from.UTC().Format(time.RFC3339), "timeMax": to.UTC().Format(time.RFC3339), "items": items}
		if err := getJSON(ctx, "POST", g.calendarBase()+"/calendar/v3/freeBusy", tok, body, &res, g.advise); err != nil {
			return nil, nil, err
		}
		for id, c := range res.Calendars {
			key := strings.ToLower(id)
			if len(c.Errors) > 0 {
				why := c.Errors[0].Reason
				if why == "notFound" {
					why = "no such calendar in the domain"
				}
				problems[key] = "Google Workspace: " + why
				continue
			}
			for _, b := range c.Busy {
				// freeBusy says busy, not why: a meeting, never time off.
				busy[key] = append(busy[key], calendar.Busy{Start: b.Start, End: b.End})
			}
		}
	}
	return busy, problems, nil
}

func errorf(f string, a ...any) error { return fmt.Errorf(f, a...) }

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
