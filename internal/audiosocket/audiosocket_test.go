package audiosocket

import (
	"bytes"
	"testing"
)

func TestFramesAndUUID(t *testing.T) {
	b := AppendFrame(nil, KindDTMF, []byte("7"))
	if !bytes.Equal(b, []byte{0x03, 0, 1, '7'}) {
		t.Fatalf("% x", b)
	}
	f, err := ReadFrame(bytes.NewReader(b))
	if err != nil || f.Kind != KindDTMF || string(f.Payload) != "7" {
		t.Fatalf("%+v %v", f, err)
	}
	u, s := UUID("0123456789abcdef0123456789abcdef")
	if u[0] != 0x01 || s != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("%x %s", u, s)
	}
	if _, s := UUID("abc123"); len(s) != 36 {
		t.Fatalf("short id: %s", s)
	}
	if keyOf(10) != '*' || keyOf(11) != '#' || keyOf(3) != '3' || keyOf(16) != 0 {
		t.Fatal("keys")
	}
}

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0x10, 0, 2, 1, 2})
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ReadFrame(bytes.NewReader(b)) })
}
