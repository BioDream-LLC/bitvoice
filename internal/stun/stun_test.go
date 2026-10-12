package stun

import (
	"encoding/hex"
	"net/netip"
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

const password = "VOkJxbRl1RmTxUk/WvJxBt"

// RFC 5769 2.1: sample request.
var request = unhex(`00 01 00 58 21 12 a4 42 b7 e7 a7 01 bc 34 d6 86 fa 87 df ae
80 22 00 10 53 54 55 4e 20 74 65 73 74 20 63 6c 69 65 6e 74
00 24 00 04 6e 00 01 ff
80 29 00 08 93 2f f9 b1 51 26 3b 36
00 06 00 09 65 76 74 6a 3a 68 36 76 59 20 20 20
00 08 00 14 9a ea a7 0c bf d8 cb 56 78 1e f2 b5 b2 d3 f2 49 c1 b5 71 a2
80 28 00 04 e5 7a 3b cf`)

// RFC 5769 2.2: sample IPv4 response.
var response = unhex(`01 01 00 3c 21 12 a4 42 b7 e7 a7 01 bc 34 d6 86 fa 87 df ae
80 22 00 0b 74 65 73 74 20 76 65 63 74 6f 72 20
00 20 00 08 00 01 a1 47 e1 12 a6 43
00 08 00 14 2b 91 f5 99 fd 9e 90 c3 8c 74 89 f9 2a f9 ba 53 f0 6b e7 d7
80 28 00 04 c0 7d 4c 96`)

func TestRFC5769Request(t *testing.T) {
	m, err := Parse(request)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != BindingRequest || m.Username() != "evtj:h6vY" {
		t.Fatalf("type %x user %q", m.Type, m.Username())
	}
	if err := m.Check(password); err != nil {
		t.Fatal(err)
	}
	if err := m.Check("wrong"); err != ErrIntegrity {
		t.Fatal("wrong password accepted")
	}
	bad := append([]byte(nil), request...)
	bad[len(bad)-1] ^= 1
	if m2, _ := Parse(bad); m2.Check(password) != ErrIntegrity {
		t.Fatal("bad fingerprint accepted")
	}
}

func TestRFC5769Response(t *testing.T) {
	m, err := Parse(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Check(password); err != nil {
		t.Fatal(err)
	}
	if a, ok := m.MappedAddress(); !ok || a != netip.MustParseAddrPort("192.0.2.1:32853") {
		t.Fatalf("mapped %v", a)
	}
}

// Our own response to the sample request checks out with the same password and maps the source back.
func TestBindingResponse(t *testing.T) {
	req, _ := Parse(request)
	src := netip.MustParseAddrPort("[2001:db8::5]:40000")
	out, err := Parse(BindingResponse(req, src, password))
	if err != nil || out.Type != BindingSuccess || out.TxID != req.TxID {
		t.Fatal(err)
	}
	if err := out.Check(password); err != nil {
		t.Fatal(err)
	}
	if a, _ := out.MappedAddress(); a != src {
		t.Fatalf("mapped %v", a)
	}
	if IsSTUN([]byte{0x80, 0, 0, 0}) || IsSTUN([]byte{22, 254, 253}) {
		t.Fatal("RTP or DTLS taken for STUN")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(request)
	f.Add(response)
	f.Fuzz(func(t *testing.T, b []byte) {
		if m, err := Parse(b); err == nil {
			m.Check(password)
			m.MappedAddress()
		}
	})
}
