package i3

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"
	"time"
)

// NG911 elements sign what they send (log events, EIDOs) as JWS (RFC 7515) with certificates the
// PSAP Credentialing Agency (PCA) issues, the certificate chain in the x5c header. Only the standard
// library's signature primitives are used here; this is the envelope around them.

// Signed is a verified JWS: its payload, the certificate that signed it, and whether that
// certificate chains to the PCA roots given (false when no roots were given).
type Signed struct {
	Payload []byte
	Signer  *x509.Certificate
	Chained bool
	Alg     string
}

// Subject names a signer for people: its common name, else its first DNS name.
func (s Signed) Subject() string {
	if s.Signer == nil {
		return ""
	}
	if s.Signer.Subject.CommonName != "" {
		return s.Signer.Subject.CommonName
	}
	if len(s.Signer.DNSNames) > 0 {
		return s.Signer.DNSNames[0]
	}
	return s.Signer.Subject.String()
}

// VerifyJWS checks a compact JWS signed with the key of the first x5c certificate (ES256, ES384,
// ES512, RS256, RS384, RS512, PS256, PS384, PS512 or EdDSA). With roots, the certificate must chain to
// them (through the other x5c certificates) and be valid now; without, the signature alone is checked.
func VerifyJWS(compact string, roots *x509.CertPool, now time.Time) (Signed, error) {
	parts := strings.Split(strings.TrimSpace(compact), ".")
	if len(parts) != 3 {
		return Signed{}, errors.New("not a compact JWS")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Signed{}, errors.New("the JWS header is not base64url")
	}
	var h struct {
		Alg  string   `json:"alg"`
		X5c  []string `json:"x5c"`
		Crit []string `json:"crit"`
	}
	if err := json.Unmarshal(hb, &h); err != nil {
		return Signed{}, errors.New("the JWS header is not JSON")
	}
	if len(h.Crit) > 0 {
		return Signed{}, errors.New("the JWS has critical header parameters this server does not know")
	}
	if len(h.X5c) == 0 {
		return Signed{}, errors.New("the JWS carries no certificate (x5c)")
	}
	var certs []*x509.Certificate
	for _, c := range h.X5c {
		der, err := base64.StdEncoding.DecodeString(c)
		if err != nil {
			return Signed{}, errors.New("an x5c certificate is not base64")
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return Signed{}, fmt.Errorf("an x5c certificate cannot be read: %v", err)
		}
		certs = append(certs, cert)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Signed{}, errors.New("the JWS payload is not base64url")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Signed{}, errors.New("the JWS signature is not base64url")
	}
	if err := verifySig(h.Alg, certs[0].PublicKey, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return Signed{}, err
	}
	out := Signed{Payload: payload, Signer: certs[0], Alg: h.Alg}
	if roots != nil {
		inter := x509.NewCertPool()
		for _, c := range certs[1:] {
			inter.AddCert(c)
		}
		if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			return out, fmt.Errorf("the signer's certificate is not from the PCA: %v", err)
		}
		out.Chained = true
	}
	return out, nil
}

func hasher(alg string) (crypto.Hash, hash.Hash) {
	switch alg[len(alg)-3:] {
	case "384":
		return crypto.SHA384, sha512.New384()
	case "512":
		return crypto.SHA512, sha512.New()
	}
	return crypto.SHA256, sha256.New()
}

func verifySig(alg string, pub any, signed, sig []byte) error {
	bad := errors.New("the JWS signature does not match")
	if alg == "EdDSA" {
		k, ok := pub.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(k, signed, sig) {
			return bad
		}
		return nil
	}
	if len(alg) != 5 {
		return fmt.Errorf("the JWS algorithm %q is not one this server takes", alg)
	}
	ch, hh := hasher(alg)
	hh.Write(signed)
	sum := hh.Sum(nil)
	switch alg[:2] {
	case "ES":
		k, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return bad
		}
		n := (k.Params().BitSize + 7) / 8
		if len(sig) != 2*n {
			return bad
		}
		r, s := new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:])
		if !ecdsa.Verify(k, sum, r, s) {
			return bad
		}
	case "RS":
		k, ok := pub.(*rsa.PublicKey)
		if !ok || rsa.VerifyPKCS1v15(k, ch, sum, sig) != nil {
			return bad
		}
	case "PS":
		k, ok := pub.(*rsa.PublicKey)
		if !ok || rsa.VerifyPSS(k, ch, sum, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) != nil {
			return bad
		}
	default:
		return fmt.Errorf("the JWS algorithm %q is not one this server takes", alg)
	}
	return nil
}

// SignJWS signs a payload with a certificate's key (ES256 for a P-256 key, ES384 for P-384, RS256
// for RSA, EdDSA for Ed25519), the chain in x5c.
func SignJWS(payload []byte, cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		return "", errors.New("no signing certificate")
	}
	var alg string
	switch k := cert.PrivateKey.(type) {
	case *ecdsa.PrivateKey:
		switch k.Curve.Params().BitSize {
		case 256:
			alg = "ES256"
		case 384:
			alg = "ES384"
		default:
			return "", errors.New("the signing key's curve is not P-256 or P-384")
		}
	case *rsa.PrivateKey:
		alg = "RS256"
	case ed25519.PrivateKey:
		alg = "EdDSA"
	default:
		return "", errors.New("the signing key is not ECDSA, RSA or Ed25519")
	}
	x5c := make([]string, len(cert.Certificate))
	for i, der := range cert.Certificate {
		x5c[i] = base64.StdEncoding.EncodeToString(der)
	}
	hb, _ := json.Marshal(map[string]any{"alg": alg, "typ": "JOSE", "x5c": x5c})
	in := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(payload)
	var sig []byte
	switch k := cert.PrivateKey.(type) {
	case *ecdsa.PrivateKey:
		_, hh := hasher(alg)
		hh.Write([]byte(in))
		r, s, err := ecdsa.Sign(rand.Reader, k, hh.Sum(nil))
		if err != nil {
			return "", err
		}
		n := (k.Curve.Params().BitSize + 7) / 8
		sig = make([]byte, 2*n)
		r.FillBytes(sig[:n])
		s.FillBytes(sig[n:])
	case *rsa.PrivateKey:
		sum := sha256.Sum256([]byte(in))
		var err error
		if sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:]); err != nil {
			return "", err
		}
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, []byte(in))
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
