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
