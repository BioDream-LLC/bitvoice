// Package screen judges incoming calls from outside: who vouched for the caller ID (STIR/SHAKEN),
// and whether the number is on a block list. It only reads what the call carries and what it is
// told; it never blocks an emergency call back or a number marked as allowed.
package screen

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Verdict is what screening found.
type Verdict struct {
	// Attest is the caller ID's attestation: A (the provider knows the caller may use the number),
	// B (knows the caller, not the number), C (only passed it on), or "" when none was given.
	Attest string `json:"attest,omitempty"`
	// Verified is passed, failed or "" (nothing to check): from the provider's verification result
	// (verstat) or, when the call carries the signed Identity header, from checking it here.
	Verified string `json:"verified,omitempty"`
	// Why says, in words, what was found.
	Why string `json:"why,omitempty"`
}

// FromHeaders reads the provider's verification result: the verstat parameter of the From or
// P-Asserted-Identity number (TN-Validation-Passed, TN-Validation-Failed, No-TN-Validation), and
// the attestation indicator some providers add (P-Attestation-Indicator).
func FromHeaders(from, pai, attestation string) Verdict {
	var v Verdict
	for _, h := range []string{pai, from} {
		low := strings.ToLower(h)
		switch {
		case strings.Contains(low, "verstat=tn-validation-passed"):
			v.Verified, v.Why = "passed", "the provider verified the caller ID"
		case strings.Contains(low, "verstat=tn-validation-failed"):
			v.Verified, v.Why = "failed", "the provider found the caller ID did not check out"
		}
		if v.Verified != "" {
			break
		}
	}
	if a := strings.ToUpper(strings.TrimSpace(attestation)); a == "A" || a == "B" || a == "C" {
		v.Attest = a
	}
	return v
}

// Passport is the content of a SHAKEN PASSporT (RFC 8225, RFC 8588).
type Passport struct {
	Attest string `json:"attest"`
	Orig   struct {
		TN string `json:"tn"`
	} `json:"orig"`
	Dest struct {
		TN []string `json:"tn"`
	} `json:"dest"`
	IAT    int64  `json:"iat"`
	OrigID string `json:"origid"`
}

type passportHeader struct {
	Alg string `json:"alg"`
	Ppt string `json:"ppt"`
	Typ string `json:"typ"`
	X5U string `json:"x5u"`
}

// Checker verifies Identity headers, keeping the providers' certificates for an hour.
type Checker struct {
	Client *http.Client
	// Roots, when set, are the STI certificate authorities a certificate must chain to. Without them
	// the signature is checked against the certificate the call names, which proves the call was not
	// changed on the way but not who signed it.
	Roots *x509.CertPool
	Now   func() time.Time

	mu    sync.Mutex
	certs map[string]cached
}

type cached struct {
	cert *x509.Certificate
	at   time.Time
	err  error
}

// Check verifies a SIP Identity header (RFC 8224) for a call from from to to: the signature, its
// age (a minute) and that the numbers in it are the call's.
func (k *Checker) Check(identity, from, to string) Verdict {
	jwt, params, _ := strings.Cut(identity, ";")
	jwt = strings.Trim(strings.TrimSpace(jwt), "<>")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return Verdict{Verified: "failed", Why: "the Identity header is not a signed PASSporT"}
	}
	var h passportHeader
	var p Passport
	if decodePart(parts[0], &h) != nil || decodePart(parts[1], &p) != nil {
		return Verdict{Verified: "failed", Why: "the Identity header could not be read"}
	}
	v := Verdict{Attest: strings.ToUpper(p.Attest)}
	if h.Alg != "ES256" || (h.Ppt != "" && h.Ppt != "shaken") {
		v.Verified, v.Why = "failed", "the Identity header uses an algorithm SHAKEN does not allow"
		return v
	}
	if x := infoParam(params); x != "" && h.X5U == "" {
		h.X5U = x
	}
	now := time.Now
	if k.Now != nil {
		now = k.Now
	}
	if age := now().Unix() - p.IAT; age > 60 || age < -60 {
		v.Verified, v.Why = "failed", fmt.Sprintf("the signature is %d seconds old (a minute is allowed)", age)
		return v
	}
	if !sameTN(p.Orig.TN, from) {
		v.Verified, v.Why = "failed", "the signed caller number is not the number calling"
		return v
	}
	if len(p.Dest.TN) > 0 && !anyTN(p.Dest.TN, to) {
		v.Verified, v.Why = "failed", "the signature was made for a call to another number"
		return v
	}
	cert, err := k.cert(h.X5U)
	if err != nil {
		v.Verified, v.Why = "failed", "the signing certificate could not be fetched: "+err.Error()
		return v
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		v.Verified, v.Why = "failed", "the signing certificate does not hold an ES256 key"
		return v
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		v.Verified, v.Why = "failed", "the signature is malformed"
		return v
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		v.Verified, v.Why = "failed", "the signature does not match: the caller ID may be spoofed"
		return v
	}
	v.Verified = "passed"
	v.Why = "the caller ID's signature checks out (attestation " + v.Attest + ")"
	if k.Roots == nil {
		v.Why += "; the certificate was not checked against the STI authorities, as none are set"
	}
	return v
}

func (k *Checker) cert(u string) (*x509.Certificate, error) {
	pu, err := url.Parse(u)
	if err != nil || pu.Scheme != "https" || pu.Host == "" {
		return nil, errors.New("the certificate address is not https")
	}
	k.mu.Lock()
	if c, ok := k.certs[u]; ok && time.Since(c.at) < time.Hour {
		k.mu.Unlock()
		return c.cert, c.err
	}
	k.mu.Unlock()
	cl := k.Client
	if cl == nil {
		cl = &http.Client{Timeout: 3 * time.Second}
	}
	var cert *x509.Certificate
	resp, err := cl.Get(u)
	if err == nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		cert, err = parseCertChain(b, k.Roots)
	}
	k.mu.Lock()
	if k.certs == nil {
		k.certs = map[string]cached{}
	}
	if len(k.certs) > 1000 {
		k.certs = map[string]cached{}
	}
	k.certs[u] = cached{cert, time.Now(), err}
	k.mu.Unlock()
	return cert, err
}

func parseCertChain(b []byte, roots *x509.CertPool) (*x509.Certificate, error) {
	var chain []*x509.Certificate
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			chain = append(chain, c)
		}
	}
	if len(chain) == 0 {
		return nil, errors.New("no certificate in the answer")
	}
	if roots != nil {
		inter := x509.NewCertPool()
		for _, c := range chain[1:] {
			inter.AddCert(c)
		}
		if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			return nil, errors.New("the certificate is not from a trusted STI authority")
		}
	}
	return chain[0], nil
}

func decodePart(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func infoParam(params string) string {
	for _, p := range strings.Split(params, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		if strings.EqualFold(k, "info") {
			return strings.Trim(v, "<>\"")
		}
	}
	return ""
}

func digits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// sameTN compares numbers by their last ten digits (+1 212 555 0100 and 2125550100 match).
func sameTN(a, b string) bool {
	da, db := digits(a), digits(b)
	if da == "" || db == "" {
		return false
	}
	if len(da) >= 10 && len(db) >= 10 {
		return da[len(da)-10:] == db[len(db)-10:]
	}
	return da == db
}

func anyTN(list []string, n string) bool {
	for _, x := range list {
		if sameTN(x, n) {
			return true
		}
	}
	return false
}
