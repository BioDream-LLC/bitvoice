package pbx

import (
	"slices"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// A browser's offer (Opus, G.711, key presses at 48000 and 8000) passed to a G.711/G.722 trunk keeps
// key presses at 8000 only: the 48 kHz telephone-event belongs to Opus, which the trunk is not offered.
func TestShapeOfferDropsKeyPressesAtUnusedClock(t *testing.T) {
	offer := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=audio 4000 RTP/AVP 111 0 8 101 96\r\n" +
		"a=rtpmap:111 opus/48000/2\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\n" +
		"a=rtpmap:101 telephone-event/48000\r\na=fmtp:101 0-16\r\n" +
		"a=rtpmap:96 telephone-event/8000\r\na=fmtp:96 0-16\r\n"
	s, err := sdp.Parse([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	m := s.Audio()
	shapeOffer(m, []string{"PCMA", "PCMU", "G722"})
	var events []int
	for _, c := range m.Codecs() {
		if strings.EqualFold(c.Name, "telephone-event") {
			events = append(events, c.ClockRate)
		}
	}
	if !slices.Equal(events, []int{8000}) {
		t.Fatalf("telephone-event clocks %v, want [8000]; formats %v", events, m.Formats)
	}
	if slices.Contains(m.Formats, "101") || strings.Contains(string(s.Bytes()), "rtpmap:101") {
		t.Fatalf("PT 101 left in: %s", s.Bytes())
	}
	// Offered Opus as well, both clocks stay.
	s, _ = sdp.Parse([]byte(offer))
	m = s.Audio()
	shapeOffer(m, []string{"OPUS", "PCMU"})
	n := 0
	for _, c := range m.Codecs() {
		if strings.EqualFold(c.Name, "telephone-event") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("with Opus kept, want key presses at both clocks, got %d: %v", n, m.Formats)
	}
}
