package pbx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

func TestShakenSignsCallsOut(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "SHAKEN 1234"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	kb, _ := x509.MarshalECPrivateKey(k)
	// A: the trunk's caller ID, one of ours. B: no caller ID set, so the extension's own number goes out.
	for _, tc := range []struct{ cid, want, orig string }{{"+12125550100", "A", "12125550100"}, {"", "B", "100"}} {
		c := newCarrier(t)
		cfg := trunkConfig(c.stack.Port())
		cfg.Trunks[0].CallerID = tc.cid
		cfg.Trunks[0].Shaken.Sign = true
		cfg.Trunks[0].Shaken.Key = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
		cfg.Trunks[0].Shaken.Cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		cfg.Trunks[0].Shaken.X5U = "https://certs.example/sti.pem"
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		if cfg.Trunks[0].Shaken.OrigID == "" {
			t.Fatal("no origination ID made")
		}
		e, rec := startEngine(t, cfg)
		waitTrunk(t, e, "registered")
		a := registered(t, e, "100")[0]
		var d *dialog
		done := make(chan struct{})
		go func() { d, _ = a.dial("2125550199"); close(done) }()
		var inv *sip.Message
		select {
		case inv = <-c.invites:
		case <-time.After(15 * time.Second):
			t.Fatal("no INVITE reached the provider")
		}
		id := inv.Get("Identity")
		if !strings.HasSuffix(id, ";info=<https://certs.example/sti.pem>;alg=ES256;ppt=shaken") {
			t.Fatalf("Identity %q", id)
		}
		var claims struct {
			Attest string                `json:"attest"`
			Orig   struct{ TN string }   `json:"orig"`
			Dest   struct{ TN []string } `json:"dest"`
		}
		b, _ := base64.RawURLEncoding.DecodeString(strings.Split(id, ".")[1])
		_ = json.Unmarshal(b, &claims)
		if claims.Attest != tc.want || claims.Orig.TN != tc.orig || claims.Dest.TN[0] != "12125550199" {
			t.Fatalf("claims %+v", claims)
		}
		<-done
		a.send(d.request(sip.BYE))
		if r := rec.last(t); r.Signed != tc.want {
			t.Fatalf("CDR signed %q", r.Signed)
		}
	}
	// A key that is not the certificate's is refused when saved.
	cfg := trunkConfig(5060)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ob, _ := x509.MarshalECPrivateKey(other)
	cfg.Trunks[0].Shaken.Sign = true
	cfg.Trunks[0].Shaken.Key = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ob}))
	cfg.Trunks[0].Shaken.Cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "shaken.key") {
		t.Fatalf("mismatched key: %v", err)
	}
}

func TestAttestation(t *testing.T) {
	cfg := trunkConfig(5060)
	tr := &cfg.Trunks[0]
	if attestation(&cfg, tr, true, "+12125550100") != "A" || attestation(&cfg, tr, true, "+19995550000") != "B" || attestation(&cfg, tr, false, "+12125550100") != "C" {
		t.Fatal("automatic attestation")
	}
	tr.Shaken.Attest = "B"
	if attestation(&cfg, tr, true, "+12125550100") != "B" {
		t.Fatal("fixed attestation")
	}
}
