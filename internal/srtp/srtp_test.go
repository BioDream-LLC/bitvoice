package srtp

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// RFC 3711 appendix B.3: key derivation.
func TestKeyDerivationVector(t *testing.T) {
	master := unhex("E1F97A0D3E018BE0D64FA32C06DE4139")
	salt := unhex("0EC675AD498AFEEBB6960B3AABE6")
	b, _ := aes.NewCipher(master)
	if got := deriveKey(b, salt, 0, 16); !bytes.Equal(got, unhex("C61E7A93744F39EE10734AFE3FF7A087")) {
		t.Fatalf("cipher key %X", got)
	}
	if got := deriveKey(b, salt, 2, 14); !bytes.Equal(got, unhex("30CBBC08863D8C85D49DB34A9AE1")) {
		t.Fatalf("cipher salt %X", got)
	}
	if got := deriveKey(b, salt, 1, 20); !bytes.Equal(got, unhex("CEBE321F6FF7716B6FD4AB49AF256A156D38BAA4")) {
		t.Fatalf("auth key %X", got)
	}
}

// RFC 3711 appendix B.2: the counter-mode keystream for a given session key and IV.
func TestKeystreamVector(t *testing.T) {
	k := keys{salt: unhex("F0F1F2F3F4F5F6F7F8F9FAFBFCFD")}
	k.block, _ = aes.NewCipher(unhex("2B7E151628AED2A6ABF7158809CF4F3C"))
	iv := k.iv(0, 0)
	if !bytes.Equal(iv, unhex("F0F1F2F3F4F5F6F7F8F9FAFBFCFD0000")) {
		t.Fatalf("iv %X", iv)
	}
	ks := make([]byte, 32)
	k.block.Encrypt(ks[:16], iv)
	iv[15] = 1
	k.block.Encrypt(ks[16:], iv)
	if !bytes.Equal(ks, unhex("E03EAD0935C95E80E166B16DD92B4EB4D23513162B02D0F72A43A2FE4A5F97AB")) {
		t.Fatalf("keystream %X", ks)
	}
}

func packet(seq uint16, ssrc uint32, payload string) []byte {
	p := make([]byte, 12)
	p[0], p[1] = 0x80, 0
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], uint32(seq)*160)
	binary.BigEndian.PutUint32(p[8:], ssrc)
	return append(p, payload...)
}

func pair(t *testing.T, suite string) (*Context, *Context) {
	key, salt := unhex("E1F97A0D3E018BE0D64FA32C06DE4139"), unhex("0EC675AD498AFEEBB6960B3AABE6")
	a, err := New(suite, key, salt)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New(suite, key, salt)
	return a, b
}

func TestRoundTripTamperReplay(t *testing.T) {
	for _, suite := range []string{AES128SHA80, AES128SHA32} {
		tx, rx := pair(t, suite)
		in := packet(1, 0xCAFEBABE, "hello, secure world")
		p, err := tx.Protect(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != len(in)+TagLen(suite) || bytes.Contains(p, []byte("hello")) {
			t.Fatalf("%s: not encrypted", suite)
		}
		out, err := rx.Unprotect(p)
		if err != nil || !bytes.Equal(out, in) {
			t.Fatalf("%s: %v %q", suite, err, out)
		}
		if _, err := rx.Unprotect(p); err != ErrReplay {
			t.Fatalf("%s: replay: %v", suite, err)
		}
		p2, _ := tx.Protect(packet(2, 0xCAFEBABE, "second"))
		p2[len(p2)-TagLen(suite)-1] ^= 1
		if _, err := rx.Unprotect(p2); err != ErrAuth {
			t.Fatalf("%s: tamper: %v", suite, err)
		}
	}
}

// Sequence numbers wrap at 65535: the rollover counter keeps the index rising on both sides.
func TestRollover(t *testing.T) {
	tx, rx := pair(t, AES128SHA80)
	for _, seq := range []uint16{65533, 65534, 65535, 0, 1, 2} {
		p, _ := tx.Protect(packet(seq, 7, "x"))
		if _, err := rx.Unprotect(p); err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
	}
	if s := rx.streams[7]; s.roc != 1 || s.highSeq != 2 {
		t.Fatalf("roc %d high %d", s.roc, s.highSeq)
	}
	// A packet from before the wrap that arrives after it still decrypts with the old counter.
	tx2, rx2 := pair(t, AES128SHA80)
	p1, _ := tx2.Protect(packet(65534, 9, "a"))
	late, _ := tx2.Protect(packet(65535, 9, "late"))
	after, _ := tx2.Protect(packet(0, 9, "b"))
	for i, p := range [][]byte{p1, after, late} {
		if _, err := rx2.Unprotect(p); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
	}
}

func TestRTCP(t *testing.T) {
	tx, rx := pair(t, AES128SHA32)
	rr := []byte{0x81, 201, 0, 7, 0, 0, 0, 5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24}
	p, err := tx.ProtectRTCP(rr)
	if err != nil || len(p) != len(rr)+14 {
		t.Fatal(err, len(p))
	}
	out, err := rx.UnprotectRTCP(p)
	if err != nil || !bytes.Equal(out, rr) {
		t.Fatalf("%v %x", err, out)
	}
	if _, err := rx.UnprotectRTCP(p); err != ErrReplay {
		t.Fatal(err)
	}
}

func FuzzUnprotect(f *testing.F) {
	tx, _ := New(AES128SHA80, unhex("E1F97A0D3E018BE0D64FA32C06DE4139"), unhex("0EC675AD498AFEEBB6960B3AABE6"))
	p, _ := tx.Protect(packet(1, 1, "seed"))
	f.Add(p)
	f.Fuzz(func(t *testing.T, b []byte) {
		rx, _ := New(AES128SHA80, unhex("E1F97A0D3E018BE0D64FA32C06DE4139"), unhex("0EC675AD498AFEEBB6960B3AABE6"))
		rx.Unprotect(b)
		rx.UnprotectRTCP(b)
	})
}
