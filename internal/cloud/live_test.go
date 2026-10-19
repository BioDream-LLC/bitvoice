package cloud

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Against real AWS, when BITVOICE_TEST_AWS_BUCKET is set (with AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN, temporary keys): an archive upload to the bucket,
// a recording's name with spaces and a colon in it, and, when BITVOICE_TEST_AWS_SECRET names a
// Secrets Manager secret holding {"password": ...}, a vault reference read from it. Every request
// is signed by this package's own SigV4.
func TestLiveAWS(t *testing.T) {
	bucket := os.Getenv("BITVOICE_TEST_AWS_BUCKET")
	if bucket == "" {
		t.Skip("BITVOICE_TEST_AWS_BUCKET is not set")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	creds := Credentials{AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"), SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"), Token: os.Getenv("AWS_SESSION_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := &S3{Region: region, Bucket: bucket, Prefix: "recordings/2026", Creds: creds}
	if err := s.Put(ctx, "call 100 to 101 at 12:00.wav", []byte("RIFF....WAVEfmt "), "audio/wav"); err != nil {
		t.Fatalf("put: %v", err)
	}
	bad := &S3{Region: region, Bucket: bucket, Creds: Credentials{AccessKey: creds.AccessKey, SecretKey: "wrong" + creds.SecretKey, Token: creds.Token}}
	if err := bad.Put(ctx, "x.txt", []byte("x"), "text/plain"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a wrong key was taken: %v", err)
	}
	if id := os.Getenv("BITVOICE_TEST_AWS_SECRET"); id != "" {
		v := &Vault{Auth: VaultAuth{AWS: creds}}
		got, err := v.Resolve(ctx, "aws-sm:"+region+":"+id+"#password")
		if err != nil || got != "trunk-pass-from-the-vault" {
			t.Fatalf("vault: %q %v", got, err)
		}
	}
}
