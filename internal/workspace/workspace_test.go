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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// GraphStandIn answers as Microsoft's token service and Graph do (shapes from Microsoft's Graph
// reference: users, orgContact, calendar getSchedule), checking what BitVoice sends. Exported for
// the API's tests and the browser tests' stand-in.
func graphStandIn(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/contoso-tenant/oauth2/v2.0/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "https://graph.microsoft.com/.default" || r.Form.Get("client_id") != "app-1" {
				t.Errorf("token form %v", r.Form)
			}
			if r.Form.Get("client_secret") != "s3cret" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`))
				return
			}
			w.Write([]byte(`{"token_type":"Bearer","expires_in":3599,"access_token":"graph-token"}`))
			return
		case r.Header.Get("Authorization") != "Bearer graph-token":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":"InvalidAuthenticationToken","message":"Access token is empty."}}`))
			return
		case r.URL.Path == "/v1.0/users" && r.URL.Query().Get("$skiptoken") == "":
			if !strings.Contains(r.URL.Query().Get("$select"), "businessPhones") {
				t.Errorf("users select %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"@odata.context":"https://graph.microsoft.com/v1.0/$metadata#users","value":[
				{"id":"u1","displayName":"Adele Vance","mail":"AdeleV@contoso.com","businessPhones":["+1 425 555 0109"],"mobilePhone":"+1 (425) 555-0110","department":"Retail","jobTitle":"Retail Manager","accountEnabled":true},
				{"id":"u2","displayName":"Old Account","mail":"old@contoso.com","businessPhones":["+14255550111"],"accountEnabled":false}],
				"@odata.nextLink":"`+srv.URL+`/v1.0/users?$skiptoken=page2"}`)
		case r.URL.Path == "/v1.0/users":
			io.WriteString(w, `{"value":[{"id":"u3","displayName":"Megan Bowen","mail":null,"userPrincipalName":"MeganB@contoso.com","businessPhones":[],"mobilePhone":null,"accountEnabled":true}]}`)
		case r.URL.Path == "/v1.0/contacts":
			io.WriteString(w, `{"value":[{"id":"c1","displayName":"Fabrikam Support","mail":"help@fabrikam.example","companyName":"Fabrikam","phones":[{"type":"business","number":"+1 800 555 0100"},{"type":"mobile","number":null}]}]}`)
		case r.URL.Path == "/v1.0/users/AdeleV@contoso.com/calendar/getSchedule" && r.Method == "POST":
			var in struct {
				Schedules []string `json:"schedules"`
				Start     struct {
					DateTime, TimeZone string
				} `json:"startTime"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Start.TimeZone != "UTC" || len(in.Schedules) != 2 {
				t.Errorf("getSchedule body %+v", in)
			}
			io.WriteString(w, `{"value":[
				{"scheduleId":"AdeleV@contoso.com","availabilityView":"0220","scheduleItems":[
					{"isPrivate":false,"status":"busy","subject":"Quarterly review","start":{"dateTime":"2026-10-30T15:00:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-10-30T16:00:00.0000000","timeZone":"UTC"}},
					{"isPrivate":true,"status":"oof","subject":"Doctor","start":{"dateTime":"2026-10-30T18:00:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-10-31T00:00:00.0000000","timeZone":"UTC"}},
					{"isPrivate":false,"status":"tentative","subject":"Maybe","start":{"dateTime":"2026-10-30T12:00:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-10-30T13:00:00.0000000","timeZone":"UTC"}}]},
				{"scheduleId":"nobody@contoso.com","availabilityView":"","error":{"message":"The requested user 'nobody@contoso.com' is invalid.","responseCode":"ErrorInvalidUser"}}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	file, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "bitvoice@acme-project.iam.gserviceaccount.com",
		"private_key": pemKey, "token_uri": "https://oauth2.googleapis.com/token"})
	return k, string(file)
}

// googleStandIn answers as Google's token service (JWT bearer grant), People API's
// listDirectoryPeople and Calendar's freeBusy do, checking the JWT's signature and claims.
func googleStandIn(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			parts := strings.Split(r.Form.Get("assertion"), ".")
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || len(parts) != 3 {
				t.Errorf("token form %v", r.Form)
				w.WriteHeader(400)
				return
			}
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
				t.Errorf("JWT signature: %v", err)
			}
			cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var c map[string]any
			_ = json.Unmarshal(cb, &c)
			if c["iss"] != "bitvoice@acme-project.iam.gserviceaccount.com" || c["aud"] != srv.URL+"/token" || c["scope"] != GoogleScopes {
				t.Errorf("JWT claims %v", c)
			}
			if c["sub"] != "admin@acme.example" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"unauthorized_client","error_description":"Client is unauthorized to retrieve access tokens using this method."}`)
				return
			}
			io.WriteString(w, `{"access_token":"ya29.google-token","expires_in":3599,"token_type":"Bearer"}`)
		case r.Header.Get("Authorization") != "Bearer ya29.google-token":
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)
		case r.URL.Path == "/v1/people:listDirectoryPeople":
			q := r.URL.Query()
			if q.Get("readMask") == "" || len(q["sources"]) != 2 {
				t.Errorf("listDirectoryPeople %q", r.URL.RawQuery)
			}
			if q.Get("pageToken") == "" {
				io.WriteString(w, `{"people":[{"resourceName":"people/1","names":[{"displayName":"Ana Ruiz"}],"emailAddresses":[{"value":"ana@acme.example"}],
					"phoneNumbers":[{"value":"(555) 010-2000","type":"work"},{"value":"+1 555 010 2001","type":"mobile"}],"organizations":[{"name":"Acme","department":"Sales","title":"Lead"}],
					"metadata":{"sources":[{"type":"DOMAIN_PROFILE","id":"1"}]}}],"nextPageToken":"p2"}`)
				return
			}
			io.WriteString(w, `{"people":[{"resourceName":"people/c9","names":[{"displayName":"Plumbing Co"}],"phoneNumbers":[{"value":"555-0199"}],"metadata":{"sources":[{"type":"DOMAIN_CONTACT","id":"c9"}]}}]}`)
		case r.URL.Path == "/calendar/v3/freeBusy" && r.Method == "POST":
			var in struct {
				TimeMin string              `json:"timeMin"`
				Items   []map[string]string `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if _, err := time.Parse(time.RFC3339, in.TimeMin); err != nil || len(in.Items) != 2 {
				t.Errorf("freeBusy body %+v", in)
			}
			io.WriteString(w, `{"kind":"calendar#freeBusy","timeMin":"2026-10-30T00:00:00.000Z","timeMax":"2026-10-31T00:00:00.000Z","calendars":{
				"ana@acme.example":{"busy":[{"start":"2026-10-30T14:00:00Z","end":"2026-10-30T14:30:00Z"}]},
				"ghost@acme.example":{"errors":[{"domain":"global","reason":"notFound"}],"busy":[]}}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMicrosoft(t *testing.T) {
	g := graphStandIn(t)
	m := &Microsoft{Tenant: "contoso-tenant", ClientID: "app-1", Secret: "s3cret", Login: g.URL, Graph: g.URL}
	ctx := context.Background()
	people, err := m.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The disabled account is left out; both pages read; the shared contact marked.
	if len(people) != 3 {
		t.Fatalf("people: %+v", people)
	}
	a := people[0]
	if a.Name != "Adele Vance" || a.Email != "AdeleV@contoso.com" || len(a.Numbers) != 2 || a.Numbers[0] != (Number{"work", "+14255550109"}) || a.Numbers[1] != (Number{"mobile", "+14255550110"}) || a.Department != "Retail" {
		t.Fatalf("Adele: %+v", a)
	}
	if people[1].Email != "MeganB@contoso.com" || len(people[1].Numbers) != 0 {
		t.Fatalf("Megan (no mail: her sign-in name): %+v", people[1])
	}
	if c := people[2]; !c.Shared || c.Company != "Fabrikam" || len(c.Numbers) != 1 || c.Numbers[0].Number != "+18005550100" {
		t.Fatalf("shared contact: %+v", c)
	}
	from := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	busy, problems, err := m.Busy(ctx, []string{"AdeleV@contoso.com", "nobody@contoso.com"}, from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b := busy["adelev@contoso.com"]
	if len(b) != 2 || b[0].Summary != "Quarterly review" || b[0].Away || !b[0].Start.Equal(from.Add(15*time.Hour)) || !b[1].Away || b[1].Summary != "" {
		t.Fatalf("busy (tentative left out, private untitled, oof away): %+v", b)
	}
	if !strings.Contains(problems["nobody@contoso.com"], "is invalid") {
		t.Fatalf("problems %v", problems)
	}
	// A wrong secret is said in plain words.
	bad := &Microsoft{Tenant: "contoso-tenant", ClientID: "app-1", Secret: "wrong", Login: g.URL, Graph: g.URL}
	if _, err := bad.People(ctx); err == nil || !strings.Contains(err.Error(), "client secret") {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestGoogle(t *testing.T) {
	k, file := testKey(t)
	srv := googleStandIn(t, &k.PublicKey)
	g := &Google{Key: file, Admin: "admin@acme.example", TokenURL: srv.URL + "/token", PeopleURL: srv.URL, CalendarURL: srv.URL}
	ctx := context.Background()
	people, err := g.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].Name != "Ana Ruiz" || people[0].Department != "Sales" || people[0].Numbers[0] != (Number{"work", "5550102000"}) ||
		people[0].Numbers[1] != (Number{"mobile", "+15550102001"}) || !people[1].Shared || people[1].Numbers[0].Number != "5550199" {
		t.Fatalf("people: %+v", people)
	}
	from := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	busy, problems, err := g.Busy(ctx, []string{"ana@acme.example", "ghost@acme.example"}, from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if b := busy["ana@acme.example"]; len(b) != 1 || !b[0].Start.Equal(from.Add(14*time.Hour)) || b[0].Away {
		t.Fatalf("busy %+v", busy)
	}
	if !strings.Contains(problems["ghost@acme.example"], "no such calendar") {
		t.Fatalf("problems %v", problems)
	}
	// Delegation not granted for that administrator: said in plain words.
	g2 := &Google{Key: file, Admin: "someone@acme.example", TokenURL: srv.URL + "/token", PeopleURL: srv.URL, CalendarURL: srv.URL}
	if _, err := g2.People(ctx); err == nil || !strings.Contains(err.Error(), "Domain-wide delegation") {
		t.Fatalf("no delegation: %v", err)
	}
	if _, _, err := ParseGoogleKey(`{"type":"authorized_user"}`); err == nil || !strings.Contains(err.Error(), "service account") {
		t.Fatalf("a user's credentials accepted: %v", err)
	}
}

// A paging link to another server is not followed with the token.
func TestPagingStaysHome(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the token went to another server: %s", r.Header.Get("Authorization"))
	}))
	defer evil.Close()
	var g *httptest.Server
	g = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			io.WriteString(w, `{"access_token":"t","expires_in":3600}`)
			return
		}
		io.WriteString(w, `{"value":[],"@odata.nextLink":"`+evil.URL+`/v1.0/users?$skiptoken=x"}`)
	}))
	defer g.Close()
	m := &Microsoft{Tenant: "x", ClientID: "a", Secret: "b", Login: g.URL, Graph: g.URL}
	if _, err := m.People(context.Background()); err == nil || !strings.Contains(err.Error(), "another server") {
		t.Fatalf("followed: %v", err)
	}
}
