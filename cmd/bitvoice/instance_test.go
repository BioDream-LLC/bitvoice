package main

import (
	"errors"
	"testing"
)

// A second claim on a data folder fails while the first holds it, and finds its address; once the
// first lets go, the folder can be claimed again.
func TestClaimData(t *testing.T) {
	dir := t.TempDir()
	a, err := claimData(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.SetURL("https://127.0.0.1:8443")
	if _, err := claimData(dir); !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("second claim: %v", err)
	}
	if u := runningURL(dir); u != "https://127.0.0.1:8443" {
		t.Fatalf("url %q", u)
	}
	a.SetURL("https://127.0.0.1:8444") // shorter or longer, the old address does not linger
	if u := runningURL(dir); u != "https://127.0.0.1:8444" {
		t.Fatalf("url %q", u)
	}
	a.Close()
	b, err := claimData(dir)
	if err != nil {
		t.Fatalf("after the first stopped: %v", err)
	}
	b.Close()
}
