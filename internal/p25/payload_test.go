package p25

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// rtpOf reads the RTP payloads of the UDP packets to port 25000 in a classic pcap file (Ethernet
// or raw IPv4 frames).
func rtpOf(t *testing.T, path string) [][]byte {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	link := binary.LittleEndian.Uint32(data[20:])
	var out [][]byte
	for off := 24; off+16 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[off+8:]))
		frame := data[off+16 : off+16+n]
		off += 16 + n
		ip := frame
		if link == 1 {
			ip = frame[14:]
		}
		if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 17 {
			continue
		}
		udp := ip[int(ip[0]&0xf)*4:]
		if binary.BigEndian.Uint16(udp[2:]) != 25000 || len(udp) < 8+12 {
			continue
		}
		rtp := udp[8:binary.BigEndian.Uint16(udp[4:])]
		out = append(out, rtp[12:])
	}
	return out
}

// Every P25 payload in a capture of ISSI equipment parses, and is written back byte for byte.
func TestCapture(t *testing.T) {
	pkts := rtpOf(t, "testdata/issi.pcap")
	if len(pkts) < 50 {
		t.Fatalf("only %d RTP packets", len(pkts))
	}
	kinds := map[int]int{}
	voice := 0
	var group int
	for i, b := range pkts {
		p, err := Parse(b)
		if err != nil {
			t.Fatalf("packet %d: %v (% x)", i, err, b)
		}
		kinds[p.Packet.Type]++
		voice += len(p.Voice)
		if p.Header != nil {
			group = p.Header.Group
		}
		if back := p.Marshal(); !bytes.Equal(back, b) {
			t.Fatalf("packet %d does not round-trip:\n in  % x\n out % x", i, b, back)
		}
	}
	t.Logf("%d packets: %v; %d voice frames; talkgroup %d", len(pkts), kinds, voice, group)
	if kinds[PTTRequest] == 0 || kinds[PTTProgress] == 0 || voice == 0 {
		t.Fatalf("expected a PTT request and voice: %v", kinds)
	}
}

func TestInfoBits(t *testing.T) {
	var f IMBEBlock
	in := []byte{0xc2, 0xce, 0x40, 0x21, 0xb1, 0x6e, 0xb0, 0x00, 0x04, 0xfe, 0xbe}
	f.SetInfo(in)
	if out := f.Info(); !bytes.Equal(out[:10], in[:10]) || out[10]&0xfe != in[10]&0xfe {
		t.Fatalf("% x != % x", out, in)
	}
}
