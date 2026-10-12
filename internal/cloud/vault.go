package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Secrets kept in a cloud vault instead of in BitVoice's configuration: any secret field may hold a
// reference, resolved when the configuration is put to use and cached for ten minutes.
//
//	aws-sm:<region>:<secret id or ARN>[#<JSON key>]     AWS Secrets Manager
//	azure-kv:<vault name>/<secret name>                  Azure Key Vault
//
// AWS uses the keys given (or the machine's instance role); Azure an app registration (tenant, client
// ID and secret) or, with none, the machine's managed identity.

// VaultAuth is how BitVoice reaches the vaults.
type VaultAuth struct {
	AWS          Credentials // empty: the instance role
	AzureTenant  string
	AzureClient  string
	AzureSecret  string
	HTTP         *http.Client
	AWSEndpoint  func(region string) string // for tests
	AzureLogin   string                     // for tests: the token endpoint base
	AzureVault   func(vault string) string  // for tests
	AzureMetaURL string                     // for tests: the managed identity endpoint
}

// IsReference reports whether a value is a vault reference.
func IsReference(v string) bool {
	return strings.HasPrefix(v, "aws-sm:") || strings.HasPrefix(v, "azure-kv:")
}

// Vault resolves references with a cache.
type Vault struct {
	Auth  VaultAuth
	mu    sync.Mutex
	cache map[string]cached
	azure struct {
		token   string
		expires time.Time
	}
}

type cached struct {
	value string
	err   error
	at    time.Time
}

func (v *Vault) client() *http.Client {
	if v.Auth.HTTP != nil {
		return v.Auth.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Resolve returns the secret a reference names (other values are returned as they are).
func (v *Vault) Resolve(ctx context.Context, ref string) (string, error) {
	if !IsReference(ref) {
		return ref, nil
	}
	v.mu.Lock()
	if c, ok := v.cache[ref]; ok && (c.err == nil && time.Since(c.at) < 10*time.Minute || c.err != nil && time.Since(c.at) < time.Minute) {
		v.mu.Unlock()
		return c.value, c.err // a failure is not retried for a minute, so nothing waits on it each time
	}
	v.mu.Unlock()
	var val string
	var err error
	if strings.HasPrefix(ref, "aws-sm:") {
		val, err = v.aws(ctx, strings.TrimPrefix(ref, "aws-sm:"))
	} else {
		val, err = v.azureKV(ctx, strings.TrimPrefix(ref, "azure-kv:"))
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", ref, err)
		val = ""
	}
	v.mu.Lock()
	if v.cache == nil {
		v.cache = map[string]cached{}
	}
	v.cache[ref] = cached{val, err, time.Now()}
	v.mu.Unlock()
	return val, err
}

func (v *Vault) aws(ctx context.Context, spec string) (string, error) {
	region, rest, ok := strings.Cut(spec, ":")
	if !ok || region == "" || rest == "" {
		return "", errors.New("write aws-sm:<region>:<secret id>")
	}
	id, key, _ := strings.Cut(rest, "#")
	creds := v.Auth.AWS
	if creds.AccessKey == "" {
		c, err := InstanceCredentials(ctx, v.client())
		if err != nil {
			return "", fmt.Errorf("no AWS keys set and no instance role: %w", err)
		}
		creds = c
	}
	endpoint := "https://secretsmanager." + region + ".amazonaws.com/"
	if v.Auth.AWSEndpoint != nil {
		endpoint = v.Auth.AWSEndpoint(region)
	}
	body, _ := json.Marshal(map[string]string{"SecretId": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	Sign(req, creds, region, "secretsmanager", sha256Hex(body), time.Now())
	resp, err := v.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		SecretString string `json:"SecretString"`
		Message      string `json:"message"`
		Type         string `json:"__type"`
	}
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("AWS answered %d %s %s", resp.StatusCode, out.Type, out.Message)
	}
	if key == "" {
		return out.SecretString, nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(out.SecretString), &m) != nil {
		return "", errors.New("the secret is not JSON, so #" + key + " cannot be read from it")
	}
	s, ok := m[key].(string)
	if !ok {
		return "", errors.New("the secret has no text value " + key)
	}
	return s, nil
}

// azureToken gets a Key Vault token: client credentials, or the managed identity.
func (v *Vault) azureToken(ctx context.Context) (string, error) {
	v.mu.Lock()
	if v.azure.token != "" && time.Now().Before(v.azure.expires) {
		t := v.azure.token
		v.mu.Unlock()
		return t, nil
	}
	v.mu.Unlock()
	var req *http.Request
	a := v.Auth
	if a.AzureClient != "" {
		base := a.AzureLogin
		if base == "" {
			base = "https://login.microsoftonline.com"
		}
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.AzureClient}, "client_secret": {a.AzureSecret},
			"scope": {"https://vault.azure.net/.default"}}
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+url.PathEscape(a.AzureTenant)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		meta := a.AzureMetaURL
		if meta == "" {
			meta = "http://169.254.169.254/metadata/identity/oauth2/token"
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, meta+"?api-version=2018-02-01&resource="+url.QueryEscape("https://vault.azure.net"), nil)
		req.Header.Set("Metadata", "true")
	}
	hc := v.client()
	if a.AzureClient == "" && v.Auth.HTTP == nil {
		hc = &http.Client{Timeout: 3 * time.Second} // the managed identity answers at once, or not at all
	}
	resp, err := hc.Do(req)
	if err != nil {
		if a.AzureClient == "" {
			return "", errors.New("no Azure app is set and this machine has no managed identity")
		}
		return "", fmt.Errorf("signing in to Azure: %w", err)
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
		Error       string          `json:"error"`
		Description string          `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&t)
	if t.AccessToken == "" {
		return "", fmt.Errorf("Azure refused the sign-in: %s %s", t.Error, t.Description)
	}
	secs := 3600
	_, _ = fmt.Sscan(strings.Trim(string(t.ExpiresIn), `"`), &secs)
	v.mu.Lock()
	v.azure.token, v.azure.expires = t.AccessToken, time.Now().Add(time.Duration(max(secs, 120)-60)*time.Second)
	v.mu.Unlock()
	return t.AccessToken, nil
}

func (v *Vault) azureKV(ctx context.Context, spec string) (string, error) {
	vault, name, ok := strings.Cut(spec, "/")
	if !ok || vault == "" || name == "" {
		return "", errors.New("write azure-kv:<vault>/<secret>")
	}
	tok, err := v.azureToken(ctx)
	if err != nil {
		return "", err
	}
	base := "https://" + vault + ".vault.azure.net"
	if v.Auth.AzureVault != nil {
		base = v.Auth.AzureVault(vault)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/secrets/"+url.PathEscape(name)+"?api-version=7.4", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := v.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Value string `json:"value"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Key Vault answered %d %s", resp.StatusCode, out.Error.Message)
	}
	return out.Value, nil
}
