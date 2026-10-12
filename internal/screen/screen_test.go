package screen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sign(t *testing.T, key *ecdsa.PrivateKey, x5u, attest, orig, dest string, iat time.Time) string {
	h, _ := json.Marshal(map[string]string{"alg": "ES256", "ppt": "shaken", "typ": "passport", "x5u": x5u})
	p, _ := json.Marshal(map[string]any{"attest": attest, "orig": map[string]string{"tn": orig}, "dest": map[string][]string{"tn": {dest}}, "iat": iat.Unix(), "origid": "x"})
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(in))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig) + ";info=<" + x5u + ">;alg=ES256;ppt=shaken"
}

func TestCheck(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "SHAKEN test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(pemCert) }))
	defer srv.Close()
	k := &Checker{Client: srv.Client()}
	x5u := srv.URL + "/cert.pem"
	now := time.Now()
	good := sign(t, key, x5u, "A", "12125550100", "13125550199", now)
	if v := k.Check(good, "+1 212 555 0100", "+13125550199"); v.Verified != "passed" || v.Attest != "A" || !strings.Contains(v.Why, "not checked against the STI") {
		t.Fatalf("good: %+v", v)
	}
	if v := k.Check(good, "+12125550111", "+13125550199"); v.Verified != "failed" || !strings.Contains(v.Why, "not the number calling") {
		t.Fatalf("other caller: %+v", v)
	}
	old := sign(t, key, x5u, "A", "12125550100", "13125550199", now.Add(-5*time.Minute))
	if v := k.Check(old, "12125550100", "13125550199"); v.Verified != "failed" || !strings.Contains(v.Why, "seconds old") {
		t.Fatalf("old: %+v", v)
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	forged := sign(t, other, x5u, "A", "12125550100", "13125550199", now)
	if v := k.Check(forged, "12125550100", "13125550199"); v.Verified != "failed" || !strings.Contains(v.Why, "spoofed") {
		t.Fatalf("forged: %+v", v)
	}
	if v := k.Check(sign(t, key, "http://insecure.example/c.pem", "A", "12125550100", "13125550199", now), "12125550100", "13125550199"); v.Verified != "failed" {
		t.Fatalf("http cert: %+v", v)
	}
	// With STI roots set, a certificate not from them fails.
	k2 := &Checker{Client: srv.Client(), Roots: x509.NewCertPool()}
	if v := k2.Check(good, "12125550100", "13125550199"); v.Verified != "failed" || !strings.Contains(v.Why, "trusted STI") {
		t.Fatalf("untrusted: %+v", v)
	}
	if v := k.Check("garbage", "1", "2"); v.Verified != "failed" {
		t.Fatal("garbage passed")
	}
}

func TestFromHeaders(t *testing.T) {
	if v := FromHeaders("<sip:+12125550100;verstat=TN-Validation-Passed@x>", "", "B"); v.Verified != "passed" || v.Attest != "B" {
		t.Fatalf("%+v", v)
	}
	if v := FromHeaders("<sip:+1212@x>", "<tel:+12125550100;verstat=TN-Validation-Failed>", ""); v.Verified != "failed" {
		t.Fatalf("%+v", v)
	}
	if v := FromHeaders("<sip:+1212@x>", "", ""); v.Verified != "" {
		t.Fatalf("%+v", v)
	}
}
