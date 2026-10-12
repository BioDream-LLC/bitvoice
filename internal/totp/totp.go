// Package totp implements time-based one-time passwords (RFC 6238, HOTP of RFC 4226) as authenticator
// apps use them: SHA-1, 6 digits, 30-second steps. HMAC-SHA1 is the standard library's.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret in base32, the form authenticator apps take.
func NewSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// hotp is RFC 4226's HOTP value with n digits.
func hotp(key []byte, counter uint64, n int) string {
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(c[:])
	h := m.Sum(nil)
	off := h[len(h)-1] & 0x0f
	v := binary.BigEndian.Uint32(h[off:]) & 0x7fffffff
	mod := uint32(1)
	for range n {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", n, v%mod)
}

func decode(secret string) ([]byte, error) {
	return b32.DecodeString(strings.ToUpper(strings.ReplaceAll(strings.TrimRight(secret, "="), " ", "")))
}

// Code returns the 6-digit code for a time.
func Code(secret string, t time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(t.Unix())/30, 6), nil
}

// Verify accepts the code for now or one step either side (clock drift), and returns the step it matched
// so a caller can refuse the same code twice.
func Verify(secret, code string, t time.Time) (uint64, bool) {
	key, err := decode(secret)
	code = strings.TrimSpace(code)
	if err != nil || len(code) != 6 {
		return 0, false
	}
	step := uint64(t.Unix()) / 30
	for _, s := range []uint64{step, step - 1, step + 1} {
		if subtle.ConstantTimeCompare([]byte(hotp(key, s, 6)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// URI is the otpauth:// link an authenticator app reads from a QR code.
func URI(issuer, account, secret string) string {
	v := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}
