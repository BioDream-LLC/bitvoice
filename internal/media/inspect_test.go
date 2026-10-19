package media

import (
	"net/netip"
	"testing"
	"time"
)

// Each rule on its own: the source the SDP gave, the payload types offered, header lengths (CSRCs,
// extension, padding; RFC 3550 5.1), size, and packets a second; reports at 1, 10, 100.
func TestInspector(t *testing.T) {
	var got []Finding
	in := NewInspector(InspectRules{Source: netip.MustParseAddr("192.0.2.10"), PayloadTypes: []int{0, 101}, PerSecond: 5, MaxBytes: 300,
		Report: func(f Finding) { got = append(got, f) }})
	ok := netip.MustParseAddr("192.0.2.10")
	pkt := func(pt byte, n int) []byte { p := make([]byte, n); p[0], p[1] = 0x80, pt; return p }
	now := time.Unix(1000, 0)
	if !in.Check(pkt(0, 172), ok, now) || !in.Check(pkt(101, 16), netip.MustParseAddr("::ffff:192.0.2.10"), now) {
		t.Fatal("good packets refused")
	}
	if in.Check(pkt(0, 172), netip.MustParseAddr("198.51.100.1"), now) {
		t.Fatal("media from another address taken")
	}
	if in.Check(pkt(8, 172), ok, now) || in.Check(pkt(0, 301), ok, now) {
		t.Fatal("payload type or size")
	}
	bad := [][]byte{
		func() []byte { p := pkt(0, 20); p[0] |= 0x03; return p }(),                      // 3 CSRCs need 24 bytes
		func() []byte { p := pkt(0, 20); p[0] |= 0x10; p[14], p[15] = 0, 9; return p }(), // extension past the end
		func() []byte { p := pkt(0, 20); p[0] |= 0x20; p[19] = 0; return p }(),           // padding of 0
		func() []byte { p := pkt(0, 20); p[0] |= 0x20; p[19] = 15; return p }(),          // padding into the header
	}
	for i, b := range bad {
		if in.Check(b, ok, now) {
			t.Errorf("malformed %d taken", i)
		}
	}
	if p := pkt(0, 40); func() bool { p[0] |= 0x30; p[14], p[15] = 0, 2; p[39] = 4; return !in.Check(p, ok, now) }() {
		t.Error("a packet with an extension and padding that fit was refused")
	}
	for i := 0; i < 20; i++ {
		in.Check(pkt(0, 172), ok, now.Add(time.Duration(i)*time.Millisecond))
	}
	c := in.Counts()
	if c["source"] != 1 || c["payload"] != 1 || c["size"] != 1 || c["malformed"] != 4 || c["flood"] != 18 {
		t.Fatalf("counts %v", c)
	}
	if !in.Check(pkt(0, 172), ok, now.Add(1100*time.Millisecond)) {
		t.Fatal("the next second refused")
	}
	kinds := map[string][]int{}
	for _, f := range got {
		kinds[f.Kind] = append(kinds[f.Kind], f.Count)
	}
	if len(kinds["flood"]) != 2 || kinds["flood"][1] != 10 || len(kinds["malformed"]) != 1 {
		t.Fatalf("reports %v", kinds)
	}
}
