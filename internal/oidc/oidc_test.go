package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
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
)

// fakeIdP is an identity provider for tests: it signs ID tokens with an RSA or EC key.
type fakeIdP struct {
	srv    *httptest.Server
	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
	claims map[string]any
	alg    string
	code   string
	pkce   string
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{alg: "RS256", code: "the-code"}
	f.rsaKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	f.ecKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/auth",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "r1", "use": "sig", "n": b64(f.rsaKey.N.Bytes()), "e": b64(big.NewInt(int64(f.rsaKey.E)).Bytes())},
			{"kty": "EC", "kid": "e1", "crv": "P-256", "x": b64(f.ecKey.X.FillBytes(make([]byte, 32))), "y": b64(f.ecKey.Y.FillBytes(make([]byte, 32)))},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != f.code || b64(sum[:]) != f.pkce {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(f.claims)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(c map[string]any) string {
	kid := "r1"
	if strings.HasPrefix(f.alg, "ES") {
		kid = "e1"
	}
	h, _ := json.Marshal(map[string]string{"alg": f.alg, "kid": kid, "typ": "JWT"})
	body, _ := json.Marshal(c)
	signed := b64(h) + "." + b64(body)
	sum := sha256.Sum256([]byte(signed))
	var sig []byte
	switch f.alg {
	case "RS256":
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, sum[:])
	case "PS256":
		sig, _ = rsa.SignPSS(rand.Reader, f.rsaKey, crypto.SHA256, sum[:], nil)
	case "ES256":
		r, s, _ := ecdsa.Sign(rand.Reader, f.ecKey, sum[:])
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	case "none":
		return signed + "."
	}
	return signed + "." + b64(sig)
}

func TestSignInFlow(t *testing.T) {
	f := newIdP(t)
	ctx := context.Background()
	p, err := Discover(ctx, nil, f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	flow, to := p.Start("bitvoice", "https://pbx.example/api/auth/sso/callback", []string{"openid", "profile", "email"})
	u, _ := url.Parse(to)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != flow.State || q.Get("nonce") != flow.Nonce || q.Get("client_id") != "bitvoice" {
		t.Fatalf("authorise URL %s", to)
	}
	f.pkce = q.Get("code_challenge")
	base := map[string]any{"iss": f.srv.URL, "aud": "bitvoice", "sub": "u-1", "email": "ann@example.com", "nonce": flow.Nonce,
		"exp": float64(time.Now().Add(time.Hour).Unix()), "iat": float64(time.Now().Unix()), "groups": []any{"pbx-admins"}}
	for _, alg := range []string{"RS256", "PS256", "ES256"} {
		f.alg, f.claims = alg, base
		c, err := p.Exchange(ctx, flow, "the-code", "bitvoice", "s3cret", "https://pbx.example/api/auth/sso/callback")
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if c.String("email") != "ann@example.com" || c.Strings("groups")[0] != "pbx-admins" {
			t.Fatalf("claims %v", c)
		}
	}
	f.alg = "RS256"
	if _, err := p.Exchange(ctx, flow, "wrong", "bitvoice", "", "https://pbx.example/cb"); err == nil {
		t.Fatal("wrong code accepted")
	}
	bad := func(name string, change func(map[string]any)) {
		c := map[string]any{}
		for k, v := range base {
			c[k] = v
		}
		change(c)
		f.claims = c
		if _, err := p.Exchange(ctx, flow, "the-code", "bitvoice", "", "x"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	bad("another audience", func(c map[string]any) { c["aud"] = "someone-else" })
	bad("another issuer", func(c map[string]any) { c["iss"] = "https://evil.example" })
	bad("expired", func(c map[string]any) { c["exp"] = float64(time.Now().Add(-time.Hour).Unix()) })
	bad("replayed nonce", func(c map[string]any) { c["nonce"] = "other" })
	bad("no subject", func(c map[string]any) { delete(c, "sub") })
	f.claims = base
	f.alg = "none"
	if _, err := p.Exchange(ctx, flow, "the-code", "bitvoice", "", "x"); err == nil {
		t.Fatal("unsigned token accepted")
	}
	// A token whose payload was altered after signing.
	f.alg = "RS256"
	good := f.sign(base)
	parts := strings.Split(good, ".")
	tampered := map[string]any{}
	for k, v := range base {
		tampered[k] = v
	}
	tampered["groups"] = []any{"everyone-is-admin"}
	body, _ := json.Marshal(tampered)
	if _, err := p.Verify(ctx, parts[0]+"."+b64(body)+"."+parts[2], "bitvoice", flow.Nonce, time.Now()); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestDiscoverChecksIssuer(t *testing.T) {
	f := newIdP(t)
	if _, err := Discover(context.Background(), nil, f.srv.URL+"/other"); err == nil {
		t.Fatal("issuer mismatch accepted")
	}
}
