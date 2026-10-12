package p25

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TIA-102.AAAD table 3-1: the MI sequence from 1234 5678 90AB CDEF.
func TestMISequence(t *testing.T) {
	// (The scanned table reads B as 8 and D as 0 in places; these are the values with that undone.)
	want := []uint64{0x6FE2802AA403828B, 0xFF35B1909449B835, 0x7EADFBE0D23ECDDF, 0x34B67525ADEE0EC7}
	mi := uint64(0x1234567890ABCDEF)
	for i, w := range want {
		mi = NextMI(mi)
		if mi != w {
			t.Fatalf("superframe %d: MI %016X, want %016X", i+1, mi, w)
		}
	}
}

// AAAD table 4-1 and annex C.4: the expanded input register and the first AES-256 output blocks.
func TestAESKeystream(t *testing.T) {
	if got := expand(0x1234567890ABCDEF, 128); !bytes.Equal(got, unhex(t, "1234567890ABCDEF6FE2802AA403828B")) {
		t.Fatalf("expansion %X", got)
	}
	k := Key{Alg: AlgAES256, Bytes: unhex(t, "0123456789ABCDEF23456789ABCDEF01456789ABCDEF01236789ABCDEF012345")}
	ks, err := k.Keystream(0x1234567890ABCDEF)
	if err != nil {
		t.Fatal(err)
	}
	// Block 2 is the first used; block 3 follows (the standard's table, with the scan's B/8 and D/0 undone).
	if !bytes.Equal(ks[:16], unhex(t, "A4A452206523E33BAD358AF571CB029A")) || !bytes.Equal(ks[16:32], unhex(t, "93AEFF9BDB7B317FB85B09F548D7CE61")) {
		t.Fatalf("keystream %X", ks[:32])
	}
}

// The capture's frames 12-17 carry the clear encryption sync: MI zero, ALGID $80, KID 0, encoded
// with the (24,16,9) Reed-Solomon code exactly as we encode it.
func TestEncryptionSyncFromCapture(t *testing.T) {
	var syms []byte
	for _, b := range rtpOf(t, "testdata/issi.pcap") {
		p, err := Parse(b)
		if err != nil {
			continue
		}
		for _, v := range p.Voice {
			if v.FT >= 0x6d && v.FT <= 0x72 {
				s := getES(v.Extra)
				syms = append(syms, s[:]...)
			}
		}
	}
	if len(syms) < 24 {
		t.Fatalf("only %d ES symbols in the capture", len(syms))
	}
	var cw [24]byte
	copy(cw[:], syms[:24])
	if want := esEncode(0, AlgClear, 0); cw != want {
		t.Fatalf("capture ES %v\nours        %v", cw, want)
	}
	mi, alg, kid, ok := esDecode(cw)
	if !ok || mi != 0 || alg != AlgClear || kid != 0 {
		t.Fatalf("decoded %x %x %x %v", mi, alg, kid, ok)
	}
}

func TestEncryptionSyncCorrectsErrors(t *testing.T) {
	cw := esEncode(0x1234567890ABCDEF, AlgAES256, 0x0102)
	for _, hits := range [][]int{{0}, {3, 17}, {1, 8, 15, 23}} {
		bad := cw
		for _, h := range hits {
			bad[h] ^= byte(h*7+5) & 63
		}
		mi, alg, kid, ok := esDecode(bad)
		if !ok || mi != 0x1234567890ABCDEF || alg != AlgAES256 || kid != 0x0102 {
			t.Fatalf("%d errors: %x %x %x %v", len(hits), mi, alg, kid, ok)
		}
	}
}
