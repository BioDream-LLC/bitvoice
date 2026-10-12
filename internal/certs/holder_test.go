package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func makePair(t *testing.T, name string, notAfter time.Time) (string, string) {
	t.Helper()
	ca, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caT := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: notAfter.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caT, &key.PublicKey, ca)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}))
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHolder(dir, c)
	if !h.Info().SelfSigned || h.Covers("sbc.example.com") {
		t.Fatalf("start %+v", h.Info())
	}
	cert, key := makePair(t, "sbc.example.com", time.Now().AddDate(0, 3, 0))
	_, otherKey := makePair(t, "other.example.com", time.Now().AddDate(0, 3, 0))
	if _, err := h.Install(cert, otherKey); err == nil || !strings.Contains(err.Error(), "not the one") {
		t.Fatalf("mismatched key: %v", err)
	}
	old, oldKey := makePair(t, "sbc.example.com", time.Now().Add(-time.Minute))
	if _, err := h.Install(old, oldKey); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired: %v", err)
	}
	info, err := h.Install(cert, key)
	if err != nil || info.SelfSigned || info.Issuer != "Test CA" || !h.Covers("sbc.example.com") {
		t.Fatalf("install %+v %v", info, err)
	}
	if got, _ := h.Client(nil); got == nil || len(got.Certificate) == 0 {
		t.Fatal("no client certificate")
	}
	// It survives a restart, and an installed certificate is not replaced by a self-signed one.
	c2, err := Load(dir, nil)
	if err != nil || !NewHolder(dir, c2).Covers("sbc.example.com") {
		t.Fatalf("after restart: %v", err)
	}
}
