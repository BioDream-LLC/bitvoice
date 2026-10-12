// Package push wakes BitVoice's mobile apps for incoming calls: Apple Push Notification service (VoIP
// pushes, token authentication with an ES256 key) and Firebase Cloud Messaging (HTTP v1, with a service
// account). A BitVoice server either holds these credentials itself or sends to a relay (cmd/bvpush, run
// by BioDream for its customers) that does, signing each request with a shared secret.
package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Message is one wake-up for one device.
type Message struct {
	Platform string `json:"platform"` // ios or android
	Token    string `json:"token"`
	CallID   string `json:"callId"`
	From     string `json:"from"`
	FromName string `json:"fromName"`
	Server   string `json:"server"` // the address the app should connect to
	// TTL is how long the push is worth delivering; a call nobody can answer any more is not.
	TTL time.Duration `json:"-"`
}

// ErrGone means the device token is no longer valid (the app was removed): forget the device's token.
var ErrGone = errors.New("push: the device no longer accepts pushes")

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwt(header, claims map[string]any, sign func([]byte) ([]byte, error)) (string, error) {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	sig, err := sign([]byte(in))
	if err != nil {
		return "", err
	}
	return in + "." + b64(sig), nil
}

// --- Apple ---

// APNs sends VoIP pushes. Key is the .p8 file's contents.
type APNs struct {
	KeyID, TeamID, Topic string // Topic is the app's bundle ID
	Key                  string
	Sandbox              bool
	Client               *http.Client
	Base                 string // for tests; empty is Apple's

	mu     sync.Mutex
	token  string
	minted time.Time
}

func (a *APNs) bearer() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Since(a.minted) < 50*time.Minute { // Apple wants a new one at least hourly
		return a.token, nil
	}
	blk, _ := pem.Decode([]byte(a.Key))
	if blk == nil {
		return "", errors.New("push: the APNs key is not a .p8 file")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return "", fmt.Errorf("push: the APNs key: %w", err)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("push: the APNs key is not an EC key")
	}
	t, err := jwt(map[string]any{"alg": "ES256", "kid": a.KeyID}, map[string]any{"iss": a.TeamID, "iat": time.Now().Unix()},
		func(in []byte) ([]byte, error) {
			sum := sha256.Sum256(in)
			r, s, err := ecdsa.Sign(rand.Reader, ek, sum[:])
			if err != nil {
				return nil, err
			}
			return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
		})
	if err != nil {
		return "", err
	}
	a.token, a.minted = t, time.Now()
	return t, nil
}

func (a *APNs) Send(ctx context.Context, m Message) error {
	tok, err := a.bearer()
	if err != nil {
		return err
	}
	base := a.Base
	if base == "" {
		base = "https://api.push.apple.com"
		if a.Sandbox {
			base = "https://api.sandbox.push.apple.com"
		}
	}
	body, _ := json.Marshal(map[string]any{"aps": map[string]any{}, "callId": m.CallID, "from": m.From, "fromName": m.FromName, "server": m.Server})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/3/device/"+url.PathEscape(m.Token), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+tok)
	req.Header.Set("apns-topic", a.Topic+".voip")
	req.Header.Set("apns-push-type", "voip")
	req.Header.Set("apns-priority", "10")
	if m.TTL > 0 {
		req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(m.TTL).Unix(), 10))
	}
	req.Header.Set("apns-collapse-id", m.CallID)
	resp, err := client(a.Client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	var e struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
	if resp.StatusCode == 410 || e.Reason == "BadDeviceToken" || e.Reason == "Unregistered" {
		return ErrGone
	}
	if resp.StatusCode == 403 && e.Reason == "ExpiredProviderToken" {
		a.mu.Lock()
		a.token = ""
		a.mu.Unlock()
	}
	return fmt.Errorf("push: Apple answered %d %s", resp.StatusCode, e.Reason)
}

func client(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true, Proxy: http.ProxyFromEnvironment}}
}

// --- Google ---

// FCM sends through Firebase Cloud Messaging; ServiceAccount is the JSON key file from Google Cloud.
type FCM struct {
	ServiceAccount string
	Client         *http.Client
	Base           string // for tests; empty is Google's

	mu      sync.Mutex
	access  string
	expires time.Time
}

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

func (f *FCM) account() (serviceAccount, *rsa.PrivateKey, error) {
	var sa serviceAccount
	if err := json.Unmarshal([]byte(f.ServiceAccount), &sa); err != nil || sa.ClientEmail == "" || sa.ProjectID == "" {
		return sa, nil, errors.New("push: the Firebase service account is not a Google key file")
	}
	blk, _ := pem.Decode([]byte(sa.PrivateKey))
	if blk == nil {
		return sa, nil, errors.New("push: the service account has no private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return sa, nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return sa, nil, errors.New("push: the service account key is not RSA")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return sa, rk, nil
}

func (f *FCM) token(ctx context.Context, sa serviceAccount, key *rsa.PrivateKey) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access != "" && time.Now().Before(f.expires) {
		return f.access, nil
	}
	now := time.Now()
	assertion, err := jwt(map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"iss": sa.ClientEmail, "scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud": sa.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()},
		func(in []byte) ([]byte, error) {
			sum := sha256.Sum256(in)
			return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		})
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client(f.Client).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&t)
	if t.AccessToken == "" {
		return "", fmt.Errorf("push: Google refused the service account (%d %s)", resp.StatusCode, t.Error)
	}
	f.access, f.expires = t.AccessToken, now.Add(time.Duration(max(t.ExpiresIn, 120)-60)*time.Second)
	return f.access, nil
}

func (f *FCM) Send(ctx context.Context, m Message) error {
	sa, key, err := f.account()
	if err != nil {
		return err
	}
	tok, err := f.token(ctx, sa, key)
	if err != nil {
		return err
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	body, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token": m.Token,
		"data":  map[string]string{"kind": "call", "callId": m.CallID, "from": m.From, "fromName": m.FromName, "server": m.Server},
		"android": map[string]any{"priority": "high", "ttl": strconv.Itoa(int(ttl.Seconds())) + "s",
			"collapse_key": m.CallID},
	}})
	base := f.Base
	if base == "" {
		base = "https://fcm.googleapis.com"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/projects/"+url.PathEscape(sa.ProjectID)+"/messages:send", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client(f.Client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 404 || bytes.Contains(b, []byte("UNREGISTERED")) {
		return ErrGone
	}
	if resp.StatusCode == 401 {
		f.mu.Lock()
		f.access = ""
		f.mu.Unlock()
	}
	return fmt.Errorf("push: Google answered %d", resp.StatusCode)
}

// --- Relay ---

// Relay sends to a push relay (cmd/bvpush), which holds the Apple and Google credentials.
type Relay struct {
	URL, KeyID, Secret string
	Client             *http.Client
}

// Sign is the relay's request signature: HMAC-SHA256 of the timestamp, a dot and the body, in hex.
func Sign(secret, ts string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts + "."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Relay) Send(ctx context.Context, m Message) error {
	body, _ := json.Marshal(struct {
		Message
		TTLSeconds int `json:"ttlSeconds"`
	}{m, int(m.TTL.Seconds())})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.URL, "/")+"/v1/push", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BitVoice-Key", r.KeyID)
	req.Header.Set("X-BitVoice-Time", ts)
	req.Header.Set("X-BitVoice-Signature", Sign(r.Secret, ts, body))
	resp, err := client(r.Client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200, 202:
		return nil
	case 410:
		return ErrGone
	}
	return fmt.Errorf("push: the relay answered %d", resp.StatusCode)
}

// Verify checks a relay request's signature and age (five minutes either way). It returns the key ID.
func Verify(r *http.Request, body []byte, secretFor func(key string) (string, bool), now time.Time) (string, error) {
	key, ts, sig := r.Header.Get("X-BitVoice-Key"), r.Header.Get("X-BitVoice-Time"), r.Header.Get("X-BitVoice-Signature")
	secret, ok := secretFor(key)
	if !ok {
		return "", errors.New("unknown key")
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(n, 0)).Abs() > 5*time.Minute {
		return "", errors.New("stale or missing timestamp")
	}
	if !hmac.Equal([]byte(Sign(secret, ts, body)), []byte(sig)) {
		return "", errors.New("bad signature")
	}
	return key, nil
}

// Platforms routes each message to the sender for its platform.
type Platforms struct{ IOS, Android Sender }

func (p Platforms) Send(ctx context.Context, m Message) error {
	switch {
	case m.Platform == "ios" && p.IOS != nil:
		return p.IOS.Send(ctx, m)
	case m.Platform == "android" && p.Android != nil:
		return p.Android.Send(ctx, m)
	}
	return fmt.Errorf("push: nothing set up to reach %s devices", m.Platform)
}
