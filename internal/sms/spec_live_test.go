package sms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Against mocks built from the providers' own published OpenAPI specifications (Stoplight Prism 4 with
// --errors, which refuses a request the specification does not allow and answers in the shape it
// documents):
//
//	docker run -p 127.0.0.1:4010:4010 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4010 --errors /s/twilio_api_v2010.json
//	docker run -p 127.0.0.1:4011:4011 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4011 --errors /s/telnyx-spec3.json
//	BITVOICE_TEST_SPEC_TWILIO=http://127.0.0.1:4010 BITVOICE_TEST_SPEC_TELNYX=http://127.0.0.1:4011 go test ./internal/sms -run Spec
//
// twilio/twilio-oai spec/json/twilio_api_v2010.json; team-telnyx/openapi openapi/spec3.json.
func TestSpecTwilio(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_TWILIO")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_TWILIO to a Prism mock of Twilio's specification")
	}
	// Twilio's account SIDs are AC and 32 hex digits; the specification checks the pattern.
	p, err := NewWith("twilio", "AC0123456789abcdef0123456789abcdef", "token", base, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Send(context.Background(), "+15550002", "+15550001", "hello from BitVoice")
	if err != nil || !strings.HasPrefix(id, "SM") && !strings.HasPrefix(id, "MM") {
		t.Fatalf("send: %q %v", id, err)
	}
}

func TestSpecTelnyx(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_TELNYX")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_TELNYX to a Prism mock of Telnyx's specification")
	}
	// The specification's server is https://api.telnyx.com/v2, so the mock serves /messages: strip /v2.
	u, _ := url.Parse(base)
	rp := httputil.NewSingleHostReverseProxy(u)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v2")
		rp.ServeHTTP(w, r)
	}))
	defer front.Close()
	p, err := NewWith("telnyx", "", "KEY0123456789", front.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Send(context.Background(), "+15550002", "+15550001", "hello from BitVoice")
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
}

// Bandwidth's messaging specification (Bandwidth/api-docs site/specs/messaging.yml):
//
//	docker run -p 127.0.0.1:4012:4010 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4010 --errors /s/bandwidth-messaging.yml
//	BITVOICE_TEST_SPEC_BANDWIDTH=http://127.0.0.1:4012
func TestSpecBandwidth(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_BANDWIDTH")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_BANDWIDTH to a Prism mock of Bandwidth's specification")
	}
	// The specification's server is https://messaging.bandwidth.com/api/v2: strip /api/v2.
	front := strip(t, base, "/api/v2")
	p, err := NewWith("bandwidth", "user", "pass", front, "9900000", "93de2206-9669-4e07-948d-329f4b722ee2")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Send(context.Background(), "+15550002", "+15550001", "hello from BitVoice")
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
}

// Vonage's SMS specification (developer.vonage.com/api/v1/developer/api/file/sms?format=json&vendorId=vonage):
//
//	docker run -p 127.0.0.1:4013:4010 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4010 --errors /s/vonage-sms.json
//	BITVOICE_TEST_SPEC_VONAGE=http://127.0.0.1:4013
func TestSpecVonage(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_VONAGE")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_VONAGE to a Prism mock of Vonage's specification")
	}
	p, err := NewWith("vonage", "abcd1234", "abcdef0123456789", base, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Send(context.Background(), "+15550002", "+447700900000", "hello from BitVoice")
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
}

// strip serves base's mock behind a front that removes prefix from every path.
func strip(t *testing.T, base, prefix string) string {
	u, _ := url.Parse(base)
	rp := httputil.NewSingleHostReverseProxy(u)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front.URL
}

// TextMagic's specification, the one its own Go client is generated from
// (textmagic/textmagic-rest-go-v2 api/openapi.yaml):
//
//	docker run -p 127.0.0.1:4014:4010 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4010 --errors /s/textmagic.yaml
//	BITVOICE_TEST_SPEC_TEXTMAGIC=http://127.0.0.1:4014
func TestSpecTextMagic(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_TEXTMAGIC")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_TEXTMAGIC to a Prism mock of TextMagic's specification")
	}
	p, err := NewWith("textmagic", "user", "KEY0123456789", base, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Send(context.Background(), "+15550002", "+447860021130", "hello from BitVoice")
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
}
