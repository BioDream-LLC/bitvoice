package i3

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pki makes a root (the PCA) and a leaf it issued, for the given key.
func pki(t *testing.T, leafKey any, pub any) (*x509.CertPool, tls.Certificate) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test PCA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "esrp.state.example.gov"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return roots, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: leafKey}
}

func TestJWS(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, k := range map[string][2]any{"ES256": {ec, &ec.PublicKey}, "RS256": {rk, &rk.PublicKey}} {
		roots, cert := pki(t, k[0], k[1])
		jws, err := SignJWS([]byte(`{"logEventType":"CallStartLogEvent"}`), cert)
		if err != nil {
			t.Fatal(name, err)
		}
		s, err := VerifyJWS(jws, roots, time.Now())
		if err != nil || !s.Chained || s.Alg != name || s.Subject() != "esrp.state.example.gov" || string(s.Payload) != `{"logEventType":"CallStartLogEvent"}` {
			t.Fatalf("%s: %+v %v", name, s, err)
		}
		other, _ := pki(t, k[0], k[1])
		if _, err := VerifyJWS(jws, other, time.Now()); err == nil || !strings.Contains(err.Error(), "not from the PCA") {
			t.Fatalf("%s: another PCA's root took it: %v", name, err)
		}
		if _, err := VerifyJWS(jws, roots, time.Now().Add(2*time.Hour)); err == nil {
			t.Fatalf("%s: an expired certificate was taken", name)
		}
		p := strings.Split(jws, ".")
		forged := p[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"logEventType":"CallEndLogEvent"}`)) + "." + p[2]
		if _, err := VerifyJWS(forged, nil, time.Now()); err == nil {
			t.Fatalf("%s: a changed payload passed", name)
		}
	}
}

// A JWS signed by OpenSSL (not by this package) verifies.
func TestJWSFromOpenSSL(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("no openssl")
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	roots, cert := pki(t, ec, &ec.PublicKey)
	dir := t.TempDir()
	keyDER, _ := x509.MarshalECPrivateKey(ec)
	os.WriteFile(filepath.Join(dir, "k.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","x5c":["` + base64.StdEncoding.EncodeToString(cert.Certificate[0]) + `"]}`))
	in := head + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"hello":"PCA"}`))
	os.WriteFile(filepath.Join(dir, "in"), []byte(in), 0o600)
	der, err := exec.Command(openssl, "dgst", "-sha256", "-sign", filepath.Join(dir, "k.pem"), filepath.Join(dir, "in")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:])
	s, err := VerifyJWS(in+"."+base64.RawURLEncoding.EncodeToString(sig), roots, time.Now())
	if err != nil || string(s.Payload) != `{"hello":"PCA"}` || !s.Chained {
		t.Fatalf("%+v %v", s, err)
	}
}
