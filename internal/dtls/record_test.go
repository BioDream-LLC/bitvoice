package dtls

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		panic(err)
	}
	return b
}

// The published TLS 1.2 PRF (P_SHA256) test vector.
func TestPRFVector(t *testing.T) {
	got := prf(unhex("9b be 43 6b a9 40 f0 17 b1 76 52 84 9a 71 db 35"), "test label",
		unhex("a0 ba 9f 93 6c da 31 18 27 a6 f7 96 ff d5 19 8c"), 100)
	want := unhex(`e3 f2 29 ba 72 7b e1 7b 8d 12 26 20 55 7c d4 53 c2 aa b2 1d 07 c3 d4 95 32 9b 52 d4 e6 1e db 5a
6b 30 17 91 e9 0d 35 c9 c9 a4 6b 4e 14 ba f9 af 0f a0 22 f7 07 7d ef 17 ab fd 37 97 c0 56 4b ab
4f bc 91 66 6e 9d ef 9b 97 fc e3 4f 79 67 89 ba a4 80 82 d1 22 ee 42 c5 a7 2e 5a 51 10 ff f7 01
87 34 7b 66`)
	if !bytes.Equal(got, want) {
		t.Fatalf("%x", got)
	}
}

func TestGCMRecordRoundTrip(t *testing.T) {
	g, _ := newGCM(make([]byte, 16), []byte{1, 2, 3, 4})
	rec := g.seal(ctApplicationData, 1, 7, []byte("hello"))
	rs, err := parseRecords(rec)
	if err != nil || len(rs) != 1 || rs[0].epoch != 1 || rs[0].seq != 7 {
		t.Fatal(err, rs)
	}
	if p, err := g.open(rs[0]); err != nil || string(p) != "hello" {
		t.Fatal(err)
	}
	rs[0].seq = 8 // the sequence number is authenticated
	if _, err := g.open(rs[0]); err == nil {
		t.Fatal("tampered header accepted")
	}
}

func TestReassembly(t *testing.T) {
	m := handshakeMsg{typ: hsCertificate, seq: 3, body: bytes.Repeat([]byte{7}, 1000)}
	whole := m.marshal()
	// Two fragments, the second first.
	frag := func(off, n int) []byte {
		h := append([]byte(nil), whole[:handshakeHeader]...)
		h[6], h[7], h[8] = byte(off>>16), byte(off>>8), byte(off)
		h[9], h[10], h[11] = byte(n>>16), byte(n>>8), byte(n)
		return append(h, m.body[off:off+n]...)
	}
	var r reassembler
	if done, _ := r.add(frag(600, 400)); len(done) != 0 {
		t.Fatal("completed early")
	}
	done, err := r.add(frag(0, 600))
	if err != nil || len(done) != 1 || !bytes.Equal(done[0].marshal(), whole) {
		t.Fatal(err)
	}
}
