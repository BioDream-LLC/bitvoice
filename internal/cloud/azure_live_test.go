package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Against Microsoft's own storage emulator, Azurite, run with https (BitVoice takes only https SAS
// addresses):
//
//	docker run -d -p 127.0.0.1:10000:10000 -v DIR:/certs mcr.microsoft.com/azure-storage/azurite \
//	  azurite-blob --blobHost 0.0.0.0 --cert /certs/cert.pem --key /certs/key.pem
//	BITVOICE_TEST_AZURITE=https://127.0.0.1:10000 BITVOICE_TEST_AZURITE_CA=DIR/cert.pem go test ./internal/cloud
//
// The test makes a container with an account SAS, then gives BitVoice a container SAS of the kind the
// Azure portal hands out (create and write only), and reads the blob back.
func TestLiveAzurite(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_AZURITE")
	if base == "" {
		t.Skip("set BITVOICE_TEST_AZURITE (and BITVOICE_TEST_AZURITE_CA) to an Azurite blob endpoint")
	}
	hc := trusting(t, os.Getenv("BITVOICE_TEST_AZURITE_CA"))
	// Azurite's documented development account.
	const account = "devstoreaccount1"
	key, _ := base64.StdEncoding.DecodeString("Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==")
	container := "bitvoice-test-" + time.Now().UTC().Format("150405")
	exp := time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05Z")
	const sv = "2023-11-03"

	// An account SAS to create the container.
	acct := strings.Join([]string{account, "rwdlc", "b", "sco", "", exp, "", "https", sv, "", ""}, "\n")
	asas := url.Values{"sv": {sv}, "ss": {"b"}, "srt": {"sco"}, "sp": {"rwdlc"}, "se": {exp}, "spr": {"https"}, "sig": {sign(key, acct)}}
	req, _ := http.NewRequest(http.MethodPut, base+"/"+account+"/"+container+"?restype=container&"+asas.Encode(), nil)
	req.Header.Set("x-ms-version", sv)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("making the container: %d", resp.StatusCode)
	}

	// A container SAS with create and write permission, as the portal makes one.
	res := "/blob/" + account + "/" + container
	svc := strings.Join([]string{"cw", "", exp, res, "", "", "https", sv, "c", "", "", "", "", "", "", ""}, "\n")
	csas := url.Values{"sv": {sv}, "sr": {"c"}, "sp": {"cw"}, "se": {exp}, "spr": {"https"}, "sig": {sign(key, svc)}}
	a := &AzureBlob{ContainerSAS: base + "/" + account + "/" + container + "?" + csas.Encode(), Prefix: "recordings/", HTTP: hc}
	if d := a.Describe(); strings.Contains(d, "sig=") || !strings.HasSuffix(d, container+"/recordings/") {
		t.Fatalf("Describe = %q", d)
	}
	body := bytes.Repeat([]byte("BitVoice recording "), 50000) // about 1 MB
	if err := a.Put(context.Background(), "2026/10/25/call 1.wav", body, "audio/wav"); err != nil {
		t.Fatal(err)
	}

	// Read it back with the account SAS: same bytes, same type.
	get, _ := http.NewRequest(http.MethodGet, base+"/"+account+"/"+container+"/recordings/2026/10/25/call%201.wav?"+asas.Encode(), nil)
	get.Header.Set("x-ms-version", sv)
	resp, err = hc.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, body) || resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("read back: %d, %d bytes, %q", resp.StatusCode, len(got), resp.Header.Get("Content-Type"))
	}

	// A SAS without write permission is refused with Azure's reason, not a bare status.
	ro := strings.Join([]string{"r", "", exp, res, "", "", "https", sv, "c", "", "", "", "", "", "", ""}, "\n")
	rsas := url.Values{"sv": {sv}, "sr": {"c"}, "sp": {"r"}, "se": {exp}, "spr": {"https"}, "sig": {sign(key, ro)}}
	bad := &AzureBlob{ContainerSAS: base + "/" + account + "/" + container + "?" + rsas.Encode(), HTTP: hc}
	err = bad.Put(context.Background(), "x.txt", []byte("x"), "text/plain")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "AuthorizationPermissionMismatch") {
		t.Fatalf("read-only SAS: %v", err)
	}
}

// Against Lowkey Vault, a Key Vault test double that also stands in for a machine's managed identity:
//
//	docker run -d -p 127.0.0.1:28443:8443 -p 127.0.0.1:28080:8080 nagyesta/lowkey-vault:7.3.127
//	BITVOICE_TEST_LOWKEY=https://127.0.0.1:28443 BITVOICE_TEST_LOWKEY_IDENTITY=http://127.0.0.1:28080 go test ./internal/cloud
func TestLiveLowkeyVault(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_LOWKEY")
	if base == "" {
		t.Skip("set BITVOICE_TEST_LOWKEY and BITVOICE_TEST_LOWKEY_IDENTITY to a Lowkey Vault")
	}
	// Lowkey Vault serves its own self-signed certificate; trust exactly that one.
	hc := trustServed(t, base)
	identity := os.Getenv("BITVOICE_TEST_LOWKEY_IDENTITY") + "/metadata/identity/oauth2/token"
	name := "trunk-password-" + time.Now().UTC().Format("150405")
	put := func(value string) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"value": value})
		req, _ := http.NewRequest(http.MethodPut, base+"/secrets/"+name+"?api-version=7.4", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+identityToken(t, identity))
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("storing the secret: %d", resp.StatusCode)
		}
	}
	put("s3cret one")
	v := &Vault{Auth: VaultAuth{HTTP: hc, AzureMetaURL: identity, AzureVault: func(string) string { return base }}}
	got, err := v.Resolve(context.Background(), "azure-kv:office/"+name)
	if err != nil || got != "s3cret one" {
		t.Fatalf("managed identity: %q %v", got, err)
	}
	// Cached for ten minutes: a new version is not read at once.
	put("s3cret two")
	if got, _ := v.Resolve(context.Background(), "azure-kv:office/"+name); got != "s3cret one" {
		t.Fatalf("cache: %q", got)
	}
	if _, err := v.Resolve(context.Background(), "azure-kv:office/no-such-secret"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing secret: %v", err)
	}
	// A new vault (no cache) reads the new version.
	v2 := &Vault{Auth: VaultAuth{HTTP: hc, AzureMetaURL: identity, AzureVault: func(string) string { return base }}}
	if got, err := v2.Resolve(context.Background(), "azure-kv:office/"+name); err != nil || got != "s3cret two" {
		t.Fatalf("new version: %q %v", got, err)
	}
}

func sign(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func trusting(t *testing.T, caFile string) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil || !pool.AppendCertsFromPEM(pem) {
			t.Fatalf("CA %s: %v", caFile, err)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

// trustServed fetches the certificate a test server presents and trusts it alone (by its key, since
// Lowkey Vault's certificate names localhost, not the address).
func trustServed(t *testing.T, base string) *http.Client {
	t.Helper()
	u, _ := url.Parse(base)
	conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	want := conn.ConnectionState().PeerCertificates[0].Raw
	conn.Close()
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || !bytes.Equal(raw[0], want) {
				return x509.CertificateInvalidError{Reason: x509.NotAuthorizedToSign}
			}
			return nil
		},
	}}}
}

func identityToken(t *testing.T, endpoint string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, endpoint+"?api-version=2018-02-01&resource="+url.QueryEscape("https://vault.azure.net"), nil)
	req.Header.Set("Metadata", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	return tok.AccessToken
}
