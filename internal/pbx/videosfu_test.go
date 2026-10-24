package pbx

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
)

// rtpWith builds a video packet: payload type, SSRC, sequence, timestamp, an optional RID in a
// one-byte header extension (id 4), and a payload.
func rtpWith(pt int, ssrc uint32, seq uint16, ts uint32, rid string, payload []byte) []byte {
	p := make([]byte, 12)
	p[0], p[1] = 0x80, byte(pt)
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], ts)
	binary.BigEndian.PutUint32(p[8:], ssrc)
	if rid != "" {
		p[0] |= 0x10
		ext := []byte{byte(4<<4 | (len(rid) - 1))}
		ext = append(ext, rid...)
		for len(ext)%4 != 0 {
			ext = append(ext, 0)
		}
		h := []byte{0xBE, 0xDE, 0, byte(len(ext) / 4)}
		p = append(append(p, h...), ext...)
	}
	return append(p, payload...)
}

var (
	vp8Key   = []byte{0x90, 0x80, 0x12, 0x00} // X and S set; PictureID 0x12; P bit clear
	vp8Delta = []byte{0x90, 0x80, 0x13, 0x01} // P bit set
)

func TestRIDAndKeyframes(t *testing.T) {
	if r := ridOf(rtpWith(96, 1, 1, 1, "h", vp8Key), 4); r != "h" {
		t.Fatalf("one-byte RID %q", r)
	}
	two := rtpWith(96, 1, 1, 1, "", vp8Key)
	two[0] |= 0x10
	two = append(two[:12], append([]byte{0x10, 0x00, 0, 1, 7, 1, 'l', 0}, vp8Key...)...)
	if r := ridOf(two, 7); r != "l" {
		t.Fatalf("two-byte RID %q", r)
	}
	if ridOf(rtpWith(96, 1, 1, 1, "h", vp8Key), 5) != "" {
		t.Fatal("a RID read from another extension")
	}
	for name, c := range map[string]struct {
		codec   string
		payload []byte
		key     bool
	}{
		"vp8 key":         {"VP8", vp8Key, true},
		"vp8 delta":       {"VP8", vp8Delta, false},
		"vp8 long id":     {"VP8", []byte{0x90, 0x80, 0x81, 0x22, 0x00}, true},
		"vp8 continued":   {"VP8", []byte{0x80, 0x80, 0x12, 0x00}, false}, // S clear: not a partition start
		"h264 idr":        {"H264", []byte{0x65, 0x88}, true},
		"h264 slice":      {"H264", []byte{0x41, 0x9a}, false},
		"h264 stap sps":   {"H264", []byte{0x18, 0, 2, 0x67, 0x42, 0, 2, 0x68, 0xce}, true},
		"h264 fu-a start": {"H264", []byte{0x7c, 0x85}, true},
		"h264 fu-a later": {"H264", []byte{0x7c, 0x05}, false},
	} {
		if got := isKeyframe(c.codec, rtpWith(96, 1, 1, 1, "x", c.payload)); got != c.key {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// A viewer of a simulcast camera gets one layer, switches at the new layer's keyframe, and sees one
// steady stream: one SSRC and continuous sequence numbers across the switch.
func TestSFULayerSwitch(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	pool := &media.Pool{Host: "127.0.0.1", Min: 35200, Max: 35300}
	ep, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	ep.Latch(netip.MustParseAddrPort(sink.LocalAddr().String()))
	pub := &sfuPub{codec: webrtc.VideoCodec{Name: "VP8", PT: 96}, sc: webrtc.Simulcast{RIDs: []string{"h", "l"}, RIDExt: 4},
		layers: map[string]uint32{}, ssrcs: map[uint32]string{}, subs: map[string]*sfuSub{}}
	pubEP, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer pubEP.Close()
	pub.ep = pubEP
	sub := &sfuSub{id: "s", pub: pub, ep: ep, pt: 100, want: "high"}
	pub.subs["s"] = sub
	e := &Engine{}
	got := func() (uint32, uint16, []byte) {
		sink.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 1500)
		n, _, err := sink.ReadFromUDP(b)
		if err != nil {
			t.Fatal("nothing forwarded")
		}
		return binary.BigEndian.Uint32(b[8:]), binary.BigEndian.Uint16(b[2:]), b[12:n]
	}
	none := func() {
		sink.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		b := make([]byte, 1500)
		if n, _, err := sink.ReadFromUDP(b); err == nil {
			t.Fatalf("forwarded %x", b[:n])
		}
	}
	const hi, lo = 0x1111, 0x2222
	// The low layer starts first: until high is seen, the viewer who wants high gets low.
	e.sfuPacket(pub, rtpWith(96, lo, 500, 9000, "l", vp8Key))
	ssrc, first, _ := got()
	if ssrc != webrtc.VideoSSRC {
		t.Fatalf("ssrc %x", ssrc)
	}
	// High's first packet is a delta: low goes on until high's keyframe, then high.
	e.sfuPacket(pub, rtpWith(96, hi, 100, 1000, "h", vp8Delta))
	none()
	e.sfuPacket(pub, rtpWith(96, lo, 501, 12000, "", vp8Delta))
	if _, seq, _ := got(); seq != first+1 {
		t.Fatalf("low before high's keyframe: seq %d", seq)
	}
	e.sfuPacket(pub, rtpWith(96, hi, 101, 4000, "h", vp8Key))
	_, seq0, _ := got()
	if seq0 != first+2 {
		t.Fatalf("the numbers did not continue into high: %d after %d", seq0, first+1)
	}
	e.sfuPacket(pub, rtpWith(96, hi, 102, 7000, "", vp8Delta)) // the RID is not on every packet
	if _, seq, _ := got(); seq != seq0+1 {
		t.Fatalf("seq %d after %d", seq, seq0)
	}
	e.sfuPacket(pub, rtpWith(96, lo, 501, 12000, "", vp8Delta))
	none() // low is no longer this viewer's layer
	// The viewer asks for low: high goes on until low's next keyframe, then low, continuing the numbers.
	sub.mu.Lock()
	sub.want = "low"
	sub.mu.Unlock()
	e.sfuPacket(pub, rtpWith(96, lo, 502, 15000, "", vp8Delta))
	none()
	e.sfuPacket(pub, rtpWith(96, hi, 103, 10000, "", vp8Delta))
	if _, seq, _ := got(); seq != seq0+2 {
		t.Fatalf("high before the switch: seq %d", seq)
	}
	e.sfuPacket(pub, rtpWith(96, lo, 503, 18000, "", vp8Key))
	ssrc, seq, payload := got()
	if ssrc != webrtc.VideoSSRC || seq != seq0+3 || payload[3]&1 != 0 {
		t.Fatalf("after the switch: ssrc %x seq %d (want %d)", ssrc, seq, seq0+3)
	}
	// VP8 picture numbers run per layer; the viewer's run continues (a gap makes Chrome drop frames).
	// Forwarded so far: 0x12 0x13 (low), then high from 0x14 on: 0x14 0x15 0x15, so the switch is 0x16.
	if payload[len(payload)-2] != 0x16 {
		t.Fatalf("PictureID %#x after the switch, want 0x16", payload[len(payload)-2])
	}
	e.sfuPacket(pub, rtpWith(96, hi, 104, 13000, "", vp8Key))
	none() // high is no longer forwarded
	// A packet of another payload type (RTX) is never forwarded.
	e.sfuPacket(pub, rtpWith(97, lo, 504, 21000, "", vp8Delta))
	none()
}

// Layers are ranked by the size their keyframes state, whatever order the RIDs came in, and a layer
// that stopped is passed over while another flows.
func TestSFULayerRanking(t *testing.T) {
	key := func(w, h int) []byte {
		return []byte{0x90, 0x80, 0x12, 0x00, 0, 0, 0x9d, 0x01, 0x2a, byte(w), byte(w >> 8), byte(h), byte(h >> 8)}
	}
	if w, h := vp8Size(rtpWith(96, 1, 1, 1, "", key(640, 360))); w != 640 || h != 360 {
		t.Fatalf("vp8Size %dx%d", w, h)
	}
	if w, _ := vp8Size(rtpWith(96, 1, 1, 1, "", vp8Delta)); w != 0 {
		t.Fatal("a size read from a delta frame")
	}
	// The browser listed "a" first, but "a" is the small picture.
	p := &sfuPub{codec: webrtc.VideoCodec{Name: "VP8", PT: 96}, sc: webrtc.Simulcast{RIDs: []string{"a", "b", "c"}, RIDExt: 4},
		layers: map[string]uint32{}, ssrcs: map[uint32]string{}, subs: map[string]*sfuSub{}}
	e := &Engine{}
	e.sfuPacket(p, rtpWith(96, 10, 1, 1, "a", key(160, 90)))
	e.sfuPacket(p, rtpWith(96, 20, 1, 1, "b", key(640, 360)))
	e.sfuPacket(p, rtpWith(96, 30, 1, 1, "c", key(320, 180)))
	if h, l := p.layerSSRC("high"), p.layerSSRC("low"); h != 20 || l != 10 {
		t.Fatalf("high %d low %d", h, l)
	}
	// The largest stops (a browser short of upload drops it): high is the largest still flowing.
	p.seen[20].last = time.Now().Add(-5 * time.Second)
	if h := p.layerSSRC("high"); h != 30 {
		t.Fatalf("high after a layer stopped: %d", h)
	}
}

func TestREMB(t *testing.T) {
	b := remb(2_500_000, []uint32{0x11, 0x22})
	if len(b) != 28 || b[0] != 0x8f || b[1] != 206 || string(b[12:16]) != "REMB" || b[16] != 2 {
		t.Fatalf("%x", b)
	}
	exp, mant := int(b[17]>>2), uint64(b[17]&3)<<16|uint64(binary.BigEndian.Uint16(b[18:]))
	if got := mant << exp; got > 2_500_000 || got < 2_490_000 {
		t.Fatalf("rate %d", got)
	}
}
