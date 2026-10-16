package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
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
	post := func(cert tls.Certificate) (int, []byte) {
		jws, err := i3.SignJWS(ev, cert)
		if err != nil {
			t.Fatal(err)
		}
		flat, _ := i3.Flat(jws) // the i3 form: flattened JSON
		b, _ := json.Marshal(flat)
		nenaCheck(t, "Logging-Service/loggingservice.yaml", "/LogEvents", "POST", "request", b)
		r, err := http.Post(ts.URL+"/i3log/LogEvents", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out
	}
	if code, out := post(impostor); code != 434 || !strings.Contains(string(out), "not from the PCA") {
		t.Fatalf("an impostor's event: %d %s", code, out)
	}
	code, out := post(signer)
	if code != 201 {
		t.Fatalf("%d %s", code, out)
	}
	nenaCheck(t, "Logging-Service/loggingservice.yaml", "/LogEvents", "POST", "201", out)
	var id string
	json.Unmarshal(out, &id)
	get := func(path string) (int, []byte) {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	code, b := get("/i3log/LogEvents/" + url.PathEscape(id))
	var flat i3.FlatJWS
	json.Unmarshal(b, &flat)
	if s, err := i3.VerifyJWS(flat.Compact(), nil, time.Now()); code != 200 || err != nil || !strings.Contains(string(s.Payload), "RouteLogEvent") {
		t.Fatalf("by ID: %d %v %s", code, err, b)
	}
	nenaCheck(t, "Logging-Service/loggingservice.yaml", "/LogEvents/{logEventId}", "GET", "200", b)
	code, b = get("/i3log/LogEvents?logEventType=RouteLogEvent&limit=5&start=1")
	var list map[string]any
	json.Unmarshal(b, &list)
	if code != 200 || list["count"] != float64(1) || list["totalCount"] != float64(1) {
		t.Fatalf("list %d %s", code, b)
	}
	nenaCheck(t, "Logging-Service/loggingservice.yaml", "/LogEvents", "GET", "200", b)
	for _, q := range []struct{ path, op string }{
		{"/i3log/LogEventIds?callId=" + url.QueryEscape("urn:emergency:uid:callid:abc:esrp1"), "/LogEventIds"},
		{"/i3log/CallIds", "/CallIds"},
		{"/i3log/AgencyIds?callId=" + url.QueryEscape("urn:emergency:uid:callid:abc:esrp1"), "/AgencyIds"},
		{"/i3log/Versions", "/Versions"},
	} {
		code, b := get(q.path)
		if code != 200 {
			t.Fatalf("%s: %d %s", q.path, code, b)
		}
		nenaCheck(t, "Logging-Service/loggingservice.yaml", q.op, "GET", "200", b)
	}
	if code, _ := get("/i3log/Conversations?callId=" + url.QueryEscape("urn:emergency:uid:callid:abc:esrp1")); code != 464 {
		t.Fatalf("a call with no text: %d", code)
	}
	if code, _ := get("/i3log/CallIds?startTime=2026-01-02T00:00:00Z&endTime=2026-01-01T00:00:00Z"); code != 466 {
		t.Fatalf("end before start: %d", code)
	}
	if _, view := c.do("GET", "/api/core/i3log", nil); len(view["items"].([]any)) != 1 || view["items"].([]any)[0].(map[string]any)["chained"] != true {
		t.Fatalf("screen view %v", view)
	}
	// Unsigned events are taken (alg none), marked as not traced to the PCA.
	unsigned := i3.FlatJWS{Protected: base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)), Payload: base64.RawURLEncoding.EncodeToString(ev)}
	ub, _ := json.Marshal(unsigned)
	if r, _ := http.Post(ts.URL+"/i3log/LogEvents", "application/json", bytes.NewReader(ub)); r.StatusCode != 201 {
		t.Fatalf("an unsigned event: %d", r.StatusCode)
	}
	if r, _ := http.Post(ts.URL+"/i3log/LogEvents", "application/json", strings.NewReader(`{"logEventType":"x"}`)); r.StatusCode != 460 {
		t.Fatalf("not a JWS: %d", r.StatusCode)
	}
}
