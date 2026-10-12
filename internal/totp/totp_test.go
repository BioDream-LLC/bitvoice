package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 rows (8 digits), and RFC 4226 appendix D.
func TestVectors(t *testing.T) {
	key := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "94287082", 1111111109: "07081804", 1111111111: "14050471",
		1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130"} {
		if got := hotp(key, uint64(unix)/30, 8); got != want {
			t.Errorf("t=%d: %s, want %s", unix, got, want)
		}
	}
	for i, want := range []string{"755224", "287082", "359152", "969429", "338314"} {
		if got := hotp(key, uint64(i), 6); got != want {
			t.Errorf("counter %d: %s, want %s", i, got, want)
		}
	}
}

func TestVerify(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	at := time.Unix(1111111111, 0)
	code, _ := Code(secret, at)
	if _, ok := Verify(secret, code, at.Add(25*time.Second)); !ok {
		t.Fatal("next step should still accept")
	}
	if _, ok := Verify(secret, code, at.Add(5*time.Minute)); ok {
		t.Fatal("old code accepted")
	}
	if _, ok := Verify(NewSecret(), "12345", at); ok {
		t.Fatal("short code accepted")
	}
}
