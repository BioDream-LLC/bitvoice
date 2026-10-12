// Package oidc signs administrators in through an OpenID Connect identity provider (Microsoft Entra ID,
// Google, Okta, Keycloak, Authentik and others): the authorisation code flow with PKCE, and ID token
// checks (signature against the provider's published keys, issuer, audience, expiry and nonce). Only the
// standard library's RSA and ECDSA verification is used; nothing cryptographic is written here.
package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Provider is an identity provider's published configuration.
type Provider struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURL  string `json:"jwks_uri"`

	client *http.Client
	mu     sync.Mutex
	keys   map[string]crypto.PublicKey
	loaded time.Time
}

// Discover reads issuer/.well-known/openid-configuration.
func Discover(ctx context.Context, client *http.Client, issuer string) (*Provider, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	u := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	var p Provider
	if err := getJSON(ctx, client, u, &p); err != nil {
		return nil, fmt.Errorf("reading the provider's settings at %s: %w", u, err)
	}
	if strings.TrimRight(p.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return nil, fmt.Errorf("the provider calls itself %q, not %q: check the issuer address", p.Issuer, issuer)
	}
	if p.AuthURL == "" || p.TokenURL == "" || p.JWKSURL == "" {
		return nil, errors.New("the provider's settings are missing an endpoint")
	}
	for _, e := range []string{p.AuthURL, p.TokenURL, p.JWKSURL} {
		if pu, err := url.Parse(e); err != nil || (pu.Scheme != "https" && !isLocal(pu.Hostname())) {
			return nil, fmt.Errorf("endpoint %s must use https", e)
		}
	}
	p.client = client
	return &p, nil
}

func isLocal(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

func getJSON(ctx context.Context, c *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// Flow is one sign-in in progress: what must come back with the code.
type Flow struct {
	State, Nonce, Verifier string
	Started                time.Time
}

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start begins a sign-in and returns the provider address to send the browser to.
func (p *Provider) Start(clientID, redirect string, scopes []string) (Flow, string) {
	f := Flow{State: random(), Nonce: random(), Verifier: random(), Started: time.Now()}
	sum := sha256.Sum256([]byte(f.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {f.State},
		"nonce":                 {f.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(p.AuthURL, "?") {
		sep = "&"
	}
	return f, p.AuthURL + sep + q.Encode()
}

// Claims are what the ID token says about the person.
type Claims map[string]any

func (c Claims) String(k string) string { s, _ := c[k].(string); return s }

// Strings reads a claim that may be a list or one string (groups, roles).
func (c Claims) Strings(k string) []string {
	switch v := c[k].(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Exchange trades the code for tokens and returns the checked ID token's claims.
func (p *Provider) Exchange(ctx context.Context, f Flow, code, clientID, secret, redirect string) (Claims, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {f.Verifier}}
	if secret != "" {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return nil, fmt.Errorf("the provider's answer could not be read (HTTP %d)", resp.StatusCode)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("the provider refused: %s %s", tok.Error, tok.Desc)
	}
	if tok.IDToken == "" {
		return nil, errors.New("the provider sent no ID token: is the openid scope allowed?")
	}
	return p.Verify(ctx, tok.IDToken, clientID, f.Nonce, time.Now())
}

// Verify checks an ID token and returns its claims.
func (p *Provider) Verify(ctx context.Context, raw, clientID, nonce string, now time.Time) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &hdr); err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	key, err := p.key(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	if err := verifySig(hdr.Alg, key, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return nil, err
	}
	var c Claims
	if err := decodePart(parts[1], &c); err != nil {
		return nil, err
	}
	if strings.TrimRight(c.String("iss"), "/") != strings.TrimRight(p.Issuer, "/") {
		return nil, errors.New("the token is from another issuer")
	}
	aud := c.Strings("aud")
	ok := false
	for _, a := range aud {
		ok = ok || a == clientID
	}
	if !ok {
		return nil, errors.New("the token is for another application")
	}
	if len(aud) > 1 && c.String("azp") != "" && c.String("azp") != clientID {
		return nil, errors.New("the token was issued to another application")
	}
	exp, _ := c["exp"].(float64)
	if exp == 0 || now.After(time.Unix(int64(exp), 0).Add(time.Minute)) {
		return nil, errors.New("the token has expired")
	}
	if iat, _ := c["iat"].(float64); iat != 0 && time.Unix(int64(iat), 0).After(now.Add(5*time.Minute)) {
		return nil, errors.New("the token is from the future: check this computer's clock")
	}
	if nonce != "" && c.String("nonce") != nonce {
		return nil, errors.New("the token does not belong to this sign-in")
	}
	if c.String("sub") == "" {
		return nil, errors.New("the token names nobody")
	}
	return c, nil
}

func decodePart(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return errors.New("malformed ID token")
	}
	return json.Unmarshal(b, v)
}

func verifySig(alg string, key crypto.PublicKey, signed, sig []byte) error {
	if len(alg) != 5 {
		return fmt.Errorf("unsupported token signature %q", alg)
	}
	var h crypto.Hash
	switch alg[2:] {
	case "256":
		h = crypto.SHA256
	case "384":
		h = crypto.SHA384
	case "512":
		h = crypto.SHA512
	default:
		return fmt.Errorf("unsupported token signature %q", alg)
	}
	digest := hashOf(h, signed)
	bad := errors.New("the token's signature is not valid")
	switch alg[:2] {
	case "RS", "PS":
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return bad
		}
		if alg[:2] == "RS" {
			if rsa.VerifyPKCS1v15(k, h, digest, sig) != nil {
				return bad
			}
		} else if rsa.VerifyPSS(k, h, digest, sig, nil) != nil {
			return bad
		}
	case "ES":
		k, ok := key.(*ecdsa.PublicKey)
		if !ok || len(sig)%2 != 0 {
			return bad
		}
		r, s := new(big.Int).SetBytes(sig[:len(sig)/2]), new(big.Int).SetBytes(sig[len(sig)/2:])
		if !ecdsa.Verify(k, digest, r, s) {
			return bad
		}
	default:
		return fmt.Errorf("unsupported token signature %q", alg) // "none" and HMAC are never accepted
	}
	return nil
}

func hashOf(h crypto.Hash, b []byte) []byte {
	switch h {
	case crypto.SHA384:
		s := sha512.Sum384(b)
		return s[:]
	case crypto.SHA512:
		s := sha512.Sum512(b)
		return s[:]
	}
	s := sha256.Sum256(b)
	return s[:]
}

// key finds a signing key by id, reloading the key set when the id is new (providers rotate keys), at
// most once a minute.
func (p *Provider) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k, ok := p.keys[kid]; ok {
		return k, nil
	}
	if time.Since(p.loaded) < time.Minute && p.keys != nil {
		return nil, errors.New("the token is signed with an unknown key")
	}
	var set struct {
		Keys []struct {
			Kty, Kid, Use, N, E, Crv, X, Y string
		} `json:"keys"`
	}
	if err := getJSON(ctx, p.client, p.JWKSURL, &set); err != nil {
		return nil, fmt.Errorf("reading the provider's keys: %w", err)
	}
	p.keys, p.loaded = map[string]crypto.PublicKey{}, time.Now()
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, err1 := base64.RawURLEncoding.DecodeString(k.N)
			e, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil || len(n) < 256 { // at least 2048-bit
				continue
			}
			p.keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		case "EC":
			var curve elliptic.Curve
			switch k.Crv {
			case "P-256":
				curve = elliptic.P256()
			case "P-384":
				curve = elliptic.P384()
			case "P-521":
				curve = elliptic.P521()
			default:
				continue
			}
			x, err1 := base64.RawURLEncoding.DecodeString(k.X)
			y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			pk := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			if !curve.IsOnCurve(pk.X, pk.Y) {
				continue
			}
			p.keys[k.Kid] = pk
		}
	}
	if k, ok := p.keys[kid]; ok {
		return k, nil
	}
	if kid == "" && len(p.keys) == 1 {
		for _, k := range p.keys {
			return k, nil
		}
	}
	return nil, errors.New("the token is signed with an unknown key")
}
