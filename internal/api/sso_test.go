package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// fakeProvider is an OpenID provider that signs in whoever the test names, with the given groups.
type fakeProvider struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	sub    string
	email  string
	groups []any
	nonce  map[string]string // code -> nonce
}

func newProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{nonce: map[string]string{}}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	b64 := base64.RawURLEncoding.EncodeToString
	m := http.NewServeMux()
	m.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/auth",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/keys"})
	})
	m.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k", "n": b64(f.key.N.Bytes()), "e": b64(big.NewInt(int64(f.key.E)).Bytes())}}})
	})
	// The person "signs in" at once and is sent back with a code.
	m.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := "code-" + q.Get("state")[:8]
		f.nonce[code] = q.Get("nonce")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	m.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		claims, _ := json.Marshal(map[string]any{"iss": f.srv.URL, "aud": "bv", "sub": f.sub, "email": f.email, "name": "Ann Example",
			"nonce": f.nonce[r.Form.Get("code")], "exp": time.Now().Add(time.Hour).Unix(), "groups": f.groups})
		hdr := b64([]byte(`{"alg":"RS256","kid":"k"}`)) + "." + b64(claims)
		sum := sha256.Sum256([]byte(hdr))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": hdr + "." + b64(sig)})
	})
	f.srv = httptest.NewServer(m)
	t.Cleanup(f.srv.Close)
	return f
}

func TestSingleSignOn(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup", code)
	}
	idp := newProvider(t)
	_, _, err := s.Store.Update("test", "sso", 0, func(c *config.Config) error {
		c.System.SSO = config.SSO{Enabled: true, Issuer: idp.srv.URL, ClientID: "bv", ClientSecret: "x",
			AdminGroups: []string{"pbx-admins"}, ViewerGroups: []string{"pbx-staff"}, PasswordSignIn: true}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, out := admin.do("GET", "/api/auth/options", nil); code != 200 || out["sso"].(map[string]any)["enabled"] != true {
		t.Fatalf("options %v", out)
	}
	signIn := func(sub, email string, groups ...any) (*client, string) {
		idp.sub, idp.email, idp.groups = sub, email, groups
		c := newClient(t, ts.URL)
		resp, err := c.http.Get(ts.URL + "/api/auth/sso/start")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return c, resp.Request.URL.String() // where the redirects ended
	}

	// An administrator from the provider.
	c, end := signIn("u-1", "ann@example.com", "pbx-admins")
	if strings.Contains(end, "sso_error") {
		t.Fatalf("sign-in failed: %s", end)
	}
	code, me := c.do("GET", "/api/auth/me", nil)
	if code != 200 || me["username"] != "ann" || me["role"] != "admin" || me["name"] != "Ann Example" {
		t.Fatalf("me %d %v", code, me)
	}
	// The same person later, now only in the staff group: same account, now a viewer.
	c, _ = signIn("u-1", "ann@example.com", "pbx-staff")
	if _, me := c.do("GET", "/api/auth/me", nil); me["username"] != "ann" || me["role"] != "viewer" {
		t.Fatalf("me again %v", me)
	}
	// Someone else whose email matches the existing password account gets their own account.
	c, _ = signIn("u-2", "owner@example.com", "pbx-staff")
	if _, me := c.do("GET", "/api/auth/me", nil); me["username"] != "owner-2" {
		t.Fatalf("took over a password account: %v", me)
	}
	// Not in any allowed group.
	_, end = signIn("u-3", "eve@example.com", "marketing")
	if !strings.Contains(end, "sso_error") {
		t.Fatalf("outsider signed in: %s", end)
	}
	// A callback with a forged state is refused.
	c = newClient(t, ts.URL)
	resp, _ := c.http.Get(ts.URL + "/api/auth/sso/callback?code=x&state=forged")
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.String(), "sso_error") {
		t.Fatal("forged callback accepted")
	}
	// SSO-only accounts have no password.
	if code, _ := newClient(t, ts.URL).do("POST", "/api/auth/login", map[string]string{"username": "ann", "password": ""}); code == 200 {
		t.Fatal("SSO account signed in without a password")
	}
}
