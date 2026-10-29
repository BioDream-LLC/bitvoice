package webrtc

import (
	"strings"
	"testing"
)

// On Windows, a browser that reached BitVoice on loopback gets this computer's addresses as candidates
// too, after the loopback one; elsewhere, and for a browser on the network, just the address it reached.
func TestLiteCandidates(t *testing.T) {
	if c := liteCandidatesOn("linux", "127.0.0.1", 10002); len(c) != 1 {
		t.Fatalf("linux: %q", c)
	}
	liteCandidates := func(h string, p int) []string { return liteCandidatesOn("windows", h, p) }
	lan := liteCandidates("192.0.2.10", 10000)
	if len(lan) != 1 || lan[0] != "a=candidate:1 1 udp 2130706431 192.0.2.10 10000 typ host" {
		t.Fatalf("network browser: %q", lan)
	}
	lo := liteCandidates("127.0.0.1", 10002)
	if !strings.Contains(lo[0], " 127.0.0.1 10002 typ host") {
		t.Fatalf("first candidate %q", lo[0])
	}
	own := 0
	for _, a := range ownAddrs() {
		if !a.IsLoopback() && !a.IsLinkLocalUnicast() {
			own++
		}
	}
	if len(lo) != 1+min(own, 7) {
		t.Fatalf("%d candidates for %d addresses: %q", len(lo), own, lo)
	}
	for i, c := range lo[1:] {
		if strings.Contains(c, "127.0.0.1") || !strings.HasSuffix(c, " 10002 typ host") || !strings.HasPrefix(c, "a=candidate:"+string(rune('2'+i))+" 1 udp ") {
			t.Fatalf("candidate %d: %q", i+2, c)
		}
	}
}
