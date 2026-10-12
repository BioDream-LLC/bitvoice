package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var exampleCreds = Credentials{AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}

const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// The AWS Signature Version 4 test suite: get-vanilla and get-vanilla-query-order-key-case.
func TestSigV4Suite(t *testing.T) {
	at, _ := time.Parse("20060102T150405Z", "20150830T123600Z")
	for url, want := range map[string]string{
		"https://example.amazonaws.com/":                             "5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31",
		"https://example.amazonaws.com/?Param2=value2&Param1=value1": "b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500",
	} {
		r, _ := http.NewRequest("GET", url, nil)
		Sign(r, exampleCreds, "us-east-1", "service", emptyHash, at)
		got := r.Header.Get("Authorization")
		if !strings.HasSuffix(got, "Signature="+want) ||
			!strings.Contains(got, "Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date,") {
			t.Fatalf("%s:\n%s", url, got)
		}
	}
}

func TestS3PutAgainstFake(t *testing.T) {
	var path, auth, sha string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, sha = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Amz-Content-Sha256")
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	s := &S3{Region: "us-east-2", Bucket: "acme-phones", Prefix: "bitvoice/", Endpoint: srv.URL, Creds: exampleCreds}
	if err := s.Put(context.Background(), "recordings/2026-10/call one.wav", []byte("RIFF"), "audio/wav"); err != nil {
		t.Fatal(err)
	}
	if path != "/acme-phones/bitvoice/recordings/2026-10/call one.wav" || string(body) != "RIFF" || sha != sha256Hex([]byte("RIFF")) ||
		!strings.Contains(auth, "/us-east-2/s3/aws4_request") {
		t.Fatalf("%q %q %q", path, auth, sha)
	}
}

func TestAzurePut(t *testing.T) {
	var path, q, typ string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, q, typ = r.URL.Path, r.URL.RawQuery, r.Header.Get("x-ms-blob-type")
		w.WriteHeader(201)
	}))
	defer srv.Close()
	a := &AzureBlob{ContainerSAS: srv.URL + "/backups?sv=2023&sig=abc", Prefix: "site1", HTTP: srv.Client()}
	if err := a.Put(context.Background(), "config/x.json", []byte("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
	if path != "/backups/site1/config/x.json" || q != "sv=2023&sig=abc" || typ != "BlockBlob" {
		t.Fatalf("%q %q %q", path, q, typ)
	}
	if strings.Contains(a.Describe(), "sig=") {
		t.Fatal("the SAS secret shown")
	}
}
