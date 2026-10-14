package screen

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"time"
)

// Signer signs outgoing calls' caller ID with the provider's STI certificate (SHAKEN, RFC 8588): it
// makes the Identity header (RFC 8224) carrying a PASSporT signed with ES256. The signing itself is
// the standard library's ECDSA; BitVoice only builds the token.
type Signer struct {
	key *ecdsa.PrivateKey
	x5u string
}

// NewSigner reads a P-256 private key (PEM: EC PRIVATE KEY or PKCS #8) and checks it belongs to the
// certificate (PEM, the first one of the chain), which must be published at x5u.
func NewSigner(keyPEM, certPEM, x5u string) (*Signer, error) {
	kb, _ := pem.Decode([]byte(keyPEM))
	if kb == nil {
		return nil, errors.New("the signing key is not PEM")
	}
	var key *ecdsa.PrivateKey
	if k, err := x509.ParseECPrivateKey(kb.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err == nil {
		ek, ok := k8.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("the signing key is not an EC key")
		}
		key = ek
	} else {
		return nil, errors.New("the signing key could not be read")
	}
	if key.Curve.Params().Name != "P-256" {
		return nil, errors.New("SHAKEN needs a P-256 key (ES256)")
	}
	cb, _ := pem.Decode([]byte(certPEM))
	if cb == nil {
		return nil, errors.New("the certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, errors.New("the certificate could not be read")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("the key does not belong to the certificate")
	}
	if !strings.HasPrefix(x5u, "https://") {
		return nil, errors.New("the certificate address (x5u) must be an https:// address verifiers can fetch")
	}
	return &Signer{key: key, x5u: x5u}, nil
}

// passport is the PASSporT claims, with the keys in the lexicographic order RFC 8225 asks for.
type passport struct {
	Attest string `json:"attest"`
	Dest   struct {
		TN []string `json:"tn"`
	} `json:"dest"`
	IAT  int64 `json:"iat"`
	Orig struct {
		TN string `json:"tn"`
	} `json:"orig"`
	OrigID string `json:"origid"`
}

// Identity returns the Identity header value for a call from one number to another, with an
// attestation (A, B or C) and an origination identifier (a UUID naming where the call entered).
func (s *Signer) Identity(from, to, attest, origID string, now time.Time) (string, error) {
	h, _ := json.Marshal(struct {
		Alg string `json:"alg"`
		Ppt string `json:"ppt"`
		Typ string `json:"typ"`
		X5U string `json:"x5u"`
	}{"ES256", "shaken", "passport", s.x5u})
	var p passport
	p.Attest = attest
	p.Dest.TN = []string{digits(to)}
	p.IAT = now.Unix()
	p.Orig.TN = digits(from)
	p.OrigID = origID
	b, _ := json.Marshal(p)
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(signing))
	r, sv, err := ecdsa.Sign(rand.Reader, s.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ";info=<" + s.x5u + ">;alg=ES256;ppt=shaken", nil
}
