package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVaults(t *testing.T) {
	var awsCalls, kvCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("X-Amz-Target") == "secretsmanager.GetSecretValue":
			awsCalls++
			if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
				w.WriteHeader(403)
				return
			}
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			s := "plain-secret"
			if in["SecretId"] == "trunk" {
				s = `{"password":"from-json","user":"acct"}`
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": s})
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "app-secret" || r.Form.Get("scope") != "https://vault.azure.net/.default" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"kv-token","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/secrets/"):
			kvCalls++
			if r.Header.Get("Authorization") != "Bearer kv-token" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"value": "kv-" + strings.TrimPrefix(r.URL.Path, "/secrets/")})
		}
	}))
	defer srv.Close()
	v := &Vault{Auth: VaultAuth{AWS: Credentials{AccessKey: "AKID", SecretKey: "sk"}, AzureTenant: "t", AzureClient: "c", AzureSecret: "app-secret",
		AWSEndpoint: func(string) string { return srv.URL + "/" }, AzureLogin: srv.URL, AzureVault: func(string) string { return srv.URL }}}
	ctx := context.Background()
	for ref, want := range map[string]string{
		"aws-sm:us-east-1:email":              "plain-secret",
		"aws-sm:us-east-1:trunk#password":     "from-json",
		"azure-kv:bitvoice-kv/trunk-password": "kv-trunk-password",
		"not a reference":                     "not a reference",
	} {
		got, err := v.Resolve(ctx, ref)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", ref, got, err)
		}
	}
	_, _ = v.Resolve(ctx, "aws-sm:us-east-1:email")
	if awsCalls != 2 || kvCalls != 1 {
		t.Fatalf("not cached: aws %d, kv %d", awsCalls, kvCalls)
	}
	if _, err := v.Resolve(ctx, "aws-sm:us-east-1:trunk#nope"); err == nil {
		t.Fatal("a missing JSON key resolved")
	}
}
