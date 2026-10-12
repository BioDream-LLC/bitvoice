package certs

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Holder keeps the certificate in use, so an uploaded one replaces it without a restart.
type Holder struct {
	dir string
	cur atomic.Pointer[tls.Certificate]
}

// NewHolder holds c, which was loaded from dir.
func NewHolder(dir string, c tls.Certificate) *Holder {
	h := &Holder{dir: dir}
	h.cur.Store(&c)
	return h
}

// Get serves the certificate to TLS clients (tls.Config.GetCertificate).
func (h *Holder) Get(*tls.ClientHelloInfo) (*tls.Certificate, error) { return h.cur.Load(), nil }

// Client presents the certificate when BitVoice connects out (tls.Config.GetClientCertificate):
// Microsoft Teams Direct Routing checks it.
func (h *Holder) Client(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return h.cur.Load(), nil
}

// Info describes a certificate for people.
type Info struct {
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	Names      []string  `json:"names"`
	NotAfter   time.Time `json:"notAfter"`
	SelfSigned bool      `json:"selfSigned"`
}

// Info describes the certificate in use.
func (h *Holder) Info() Info {
	c := h.cur.Load()
	if c == nil || len(c.Certificate) == 0 {
		return Info{}
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return Info{}
	}
	return describe(leaf)
}

func describe(leaf *x509.Certificate) Info {
	names := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	return Info{Subject: leaf.Subject.CommonName, Issuer: leaf.Issuer.CommonName, Names: names, NotAfter: leaf.NotAfter,
		SelfSigned: bytes.Equal(leaf.RawIssuer, leaf.RawSubject)}
}

// Covers says whether the certificate in use is valid for a host name (wildcards included).
func (h *Holder) Covers(name string) bool {
	c := h.cur.Load()
	if c == nil || len(c.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	return err == nil && leaf.VerifyHostname(name) == nil
}

// Install checks a certificate (with its chain) and private key in PEM form, saves them, and uses them
// from now on.
func (h *Holder) Install(certPEM, keyPEM string) (Info, error) {
	certPEM, keyPEM = strings.TrimSpace(certPEM)+"\n", strings.TrimSpace(keyPEM)+"\n"
	c, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		switch {
		case !strings.Contains(certPEM, "BEGIN CERTIFICATE"):
			return Info{}, errors.New("paste the certificate in PEM form (it starts with -----BEGIN CERTIFICATE-----)")
		case !strings.Contains(keyPEM, "PRIVATE KEY"):
			return Info{}, errors.New("paste the private key in PEM form (it starts with -----BEGIN PRIVATE KEY-----), without a password")
		case strings.Contains(err.Error(), "does not match"):
			return Info{}, errors.New("the private key is not the one for this certificate")
		}
		return Info{}, errors.New("the certificate and key could not be read: " + err.Error())
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return Info{}, errors.New("the certificate could not be read")
	}
	if time.Now().After(leaf.NotAfter) {
		return Info{}, errors.New("this certificate expired on " + leaf.NotAfter.Format("2 January 2006"))
	}
	if time.Now().Before(leaf.NotBefore) {
		return Info{}, errors.New("this certificate is not valid until " + leaf.NotBefore.Format("2 January 2006"))
	}
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return Info{}, err
	}
	// Write the key first, then the certificate, each by rename so a crash never leaves a mixed pair.
	for _, f := range []struct {
		name, body string
		mode       os.FileMode
	}{{"key.pem", keyPEM, 0o600}, {"cert.pem", certPEM, 0o644}} {
		tmp := filepath.Join(h.dir, f.name+".new")
		if err := os.WriteFile(tmp, []byte(f.body), f.mode); err != nil {
			return Info{}, err
		}
		if err := os.Rename(tmp, filepath.Join(h.dir, f.name)); err != nil {
			return Info{}, err
		}
	}
	c.Leaf = leaf
	h.cur.Store(&c)
	return describe(leaf), nil
}

// Pin is the SHA-256 (hex) of the certificate in use: other servers accept exactly this one.
func (h *Holder) Pin() string {
	c := h.cur.Load()
	if c == nil || len(c.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(c.Certificate[0])
	return hex.EncodeToString(sum[:])
}
