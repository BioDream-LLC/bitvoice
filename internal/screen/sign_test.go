package screen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func stiCert(t *testing.T) (keyPEM, certPEM string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "SHAKEN 1234"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})),
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// What BitVoice signs, the verifier (its own, here) accepts; a changed number does not pass.
func TestSignThenVerify(t *testing.T) {
	keyPEM, certPEM := stiCert(t)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(certPEM)) }))
	defer ts.Close()
	if _, err := NewSigner(keyPEM, certPEM, "http://plain/cert.pem"); err == nil {
		t.Fatal("plain http x5u accepted")
	}
	other, _ := stiCert(t)
	if _, err := NewSigner(other, certPEM, ts.URL+"/cert.pem"); err == nil {
		t.Fatal("a key that is not the certificate's was accepted")
	}
	s, err := NewSigner(keyPEM, certPEM, ts.URL+"/cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Identity("+1 (212) 555-0100", "+13125550199", "A", "de305d54-75b4-431b-adb2-eb6b9e546014", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(id, ";info=<"+ts.URL+"/cert.pem>;alg=ES256;ppt=shaken") {
		t.Fatal(id)
	}
	k := &Checker{Client: ts.Client()}
	if v := k.Check(id, "+12125550100", "+13125550199"); v.Verified != "passed" || v.Attest != "A" {
		t.Fatalf("%+v", v)
	}
	if v := k.Check(id, "+12125550101", "+13125550199"); v.Verified != "failed" {
		t.Fatal("a call from another number passed")
	}
	// A tampered payload fails the signature.
	parts := strings.SplitN(id, ".", 3)
	if v := k.Check(parts[0]+"."+parts[1]+"x."+parts[2], "+12125550100", "+13125550199"); v.Verified != "failed" {
		t.Fatal("tampered token passed")
	}
}
