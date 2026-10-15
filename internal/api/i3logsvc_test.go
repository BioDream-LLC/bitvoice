package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
)

func testPCA(t *testing.T, cn string) (rootPEM string, leaf tls.Certificate) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "PCA " + cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	l := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, _ := x509.CreateCertificate(rand.Reader, l, caCert, &k.PublicKey, caKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})), tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

// The logging service takes signed log events from allowed addresses, checks the signer against
// the PCA roots, and gives them back by ID and by call.
func TestI3LoggingService(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	root, signer := testPCA(t, "esrp.state.example.gov")
	_, impostor := testPCA(t, "esrp.state.example.gov")
	_, cfg := c.do("GET", "/api/settings", nil)
	core := cfg["system"].(map[string]any)["core"].(map[string]any)
	core["logging"], core["allowFrom"], core["pcaRoots"] = true, []string{"127.0.0.1"}, root
	if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	ev, _ := json.Marshal(map[string]any{"logEventType": "RouteLogEvent", "timestamp": time.Now().UTC().Format(time.RFC3339), "elementId": "esrp1", "agencyId": "state.example.gov", "callId": "urn:emergency:uid:callid:abc:esrp1"})
	post := func(cert tls.Certificate) (int, map[string]any) {
		jws, err := i3.SignJWS(ev, cert)
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.Post(ts.URL+"/i3log/LogEvents", "application/jose", strings.NewReader(jws))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if code, out := post(impostor); code != 400 || !strings.Contains(out["reason"].(string), "not from the PCA") {
		t.Fatalf("an impostor's event: %d %v", code, out)
	}
	code, out := post(signer)
	if code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	id := out["logEventId"].(string)
	r, _ := http.Get(ts.URL + "/i3log/LogEvents/" + url.PathEscape(id))
	b, _ := io.ReadAll(r.Body)
	if s, err := i3.VerifyJWS(string(b), nil, time.Now()); r.StatusCode != 200 || err != nil || !strings.Contains(string(s.Payload), "RouteLogEvent") {
		t.Fatalf("by ID: %d %v %s", r.StatusCode, err, b)
	}
	r, _ = http.Get(ts.URL + "/i3log/LogEvents?callId=" + url.QueryEscape("urn:emergency:uid:callid:abc:esrp1"))
	var list map[string]any
	json.NewDecoder(r.Body).Decode(&list)
	if list["count"] != float64(1) {
		t.Fatalf("by call %v", list)
	}
	if _, view := c.do("GET", "/api/core/i3log", nil); len(view["items"].([]any)) != 1 || view["items"].([]any)[0].(map[string]any)["chained"] != true {
		t.Fatalf("screen view %v", view)
	}
	if r, _ := http.Post(ts.URL+"/i3log/LogEvents", "application/jose", strings.NewReader(`{"logEventType":"x"}`)); r.StatusCode != 400 {
		t.Fatalf("an unsigned event: %d", r.StatusCode)
	}
	if r, _ := http.Get(ts.URL + "/i3log/Versions"); r.StatusCode != 200 {
		t.Fatalf("versions %d", r.StatusCode)
	}
}
