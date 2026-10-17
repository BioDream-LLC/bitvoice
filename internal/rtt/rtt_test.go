package rtt

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// collect runs a sender, passing each packet through drop (true loses it) to a receiver.
func collect(t *testing.T, red int, drop func(i int) bool, typed ...string) string {
	t.Helper()
	r := &Receiver{T140: 98, Red: red}
	var mu sync.Mutex
	var got strings.Builder
	i := 0
	last := time.Now()
	done := make(chan struct{}, 64)
	s := &Sender{T140: 98, Red: red, SSRC: 7, Write: func(pkt []byte) {
		mu.Lock()
		defer mu.Unlock()
		if !drop(i) {
			got.WriteString(r.Packet(pkt))
		}
		i++
		last = time.Now()
		done <- struct{}{}
	}}
	defer s.Close()
	for _, w := range typed {
		s.Send(w)
		time.Sleep(Interval + 50*time.Millisecond)
	}
	// The redundant copies follow on the sender's timer, which runs late on a slow machine: wait until
	// it has been quiet for a while (it stops once everything was sent Level+1 times).
	time.Sleep(time.Duration(Level+1) * (Interval + 50*time.Millisecond))
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		mu.Lock()
		quiet := time.Since(last) > 3*Interval
		mu.Unlock()
		if quiet {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return got.String()
}

func TestRoundTrip(t *testing.T) {
	words := []string{"HEL", "LO ", "I need ", "help", " at 12 Main St", "\u2028", "é ✓"}
	want := strings.Join(words, "")
	if got := collect(t, 100, func(int) bool { return false }, words...); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := collect(t, 0, func(int) bool { return false }, words...); got != want {
		t.Fatalf("no redundancy: got %q", got)
	}
}

// One or two lost packets in a row are made good by the redundancy; three are marked lost.
func TestLoss(t *testing.T) {
	words := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := collect(t, 100, func(i int) bool { return i == 1 || i == 3 || i == 4 }, words...); got != "abcdefg" {
		t.Fatalf("got %q", got)
	}
	if got := collect(t, 100, func(i int) bool { return i >= 1 && i <= 3 }, words...); got != "a\uFFFDcdefg" { // packet 4 repeats c and d; b is gone
		t.Fatalf("got %q", got)
	}
	if got := collect(t, 0, func(i int) bool { return i == 2 }, words...); got != "ab\uFFFDdefg" {
		t.Fatalf("no redundancy: got %q", got)
	}
}

// The RFC 2198 layout, byte by byte: headers with F set, 14-bit offsets and 10-bit lengths, then the
// primary's one-byte header and the data oldest first.
func TestLayout(t *testing.T) {
	var pkts [][]byte
	s := &Sender{T140: 98, Red: 100, SSRC: 0x01020304, Write: func(p []byte) { pkts = append(pkts, p) }}
	s.mu.Lock()
	s.started, s.start = true, time.Now()
	s.gens, s.ts = [][]byte{[]byte("ab"), []byte("c")}, []uint32{1000, 1300}
	p := s.packet([]byte("de"), 1600)
	s.mu.Unlock()
	if p[1]&0x7f != 100 || p[1]&0x80 != 0 || binary.BigEndian.Uint32(p[4:]) != 1600 || binary.BigEndian.Uint32(p[8:]) != 0x01020304 {
		t.Fatalf("header % x", p[:12])
	}
	want := []byte{0x80 | 98, 600 >> 6, (600&0x3f)<<2 | 0, 2, 0x80 | 98, 300 >> 6, (300&0x3f)<<2 | 0, 1, 98, 'a', 'b', 'c', 'd', 'e'}
	if string(p[12:]) != string(want) {
		t.Fatalf("payload % x, want % x", p[12:], want)
	}
	blocks, ok := splitRed(p[12:], 98)
	if !ok || len(blocks) != 3 || string(blocks[0]) != "ab" || string(blocks[2]) != "de" {
		t.Fatalf("split %q %v", blocks, ok)
	}
}

// Damaged packets never panic and never yield text.
func FuzzReceiver(f *testing.F) {
	f.Add([]byte{0x80, 100, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0xe2, 0, 0, 2, 98, 'h', 'i'})
	f.Fuzz(func(t *testing.T, b []byte) {
		r := &Receiver{T140: 98, Red: 100}
		_ = r.Packet(b)
	})
}

// pcap writes packets as UDP datagrams to port 5004 in a pcap file (raw IPv4 link type).
func pcap(pkts [][]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0xa1b2c3d4)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = append(b, make([]byte, 8)...)
	b = binary.LittleEndian.AppendUint32(b, 65535)
	b = binary.LittleEndian.AppendUint32(b, 101) // LINKTYPE_RAW
	for i, p := range pkts {
		n := 28 + len(p)
		b = binary.LittleEndian.AppendUint32(b, uint32(i))
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
		ip := []byte{0x45, 0, byte(n >> 8), byte(n), 0, 0, 0, 0, 64, 17, 0, 0, 127, 0, 0, 1, 127, 0, 0, 1}
		var sum uint32
		for k := 0; k < 20; k += 2 {
			sum += uint32(ip[k])<<8 | uint32(ip[k+1])
		}
		for sum > 0xffff {
			sum = sum&0xffff + sum>>16
		}
		binary.BigEndian.PutUint16(ip[10:], ^uint16(sum))
		b = append(b, ip...)
		b = binary.BigEndian.AppendUint16(b, 5004)
		b = binary.BigEndian.AppendUint16(b, 5004)
		b = binary.BigEndian.AppendUint16(b, uint16(8+len(p)))
		b = binary.BigEndian.AppendUint16(b, 0)
		b = append(b, p...)
	}
	return b
}

// Against Wireshark's RTP and RFC 2198 dissectors: what we send splits into the blocks we meant, with
// the offsets and lengths we meant.
func TestAgainstWireshark(t *testing.T) {
	tshark := os.Getenv("BITVOICE_TEST_TSHARK")
	if tshark == "" {
		t.Skip("set BITVOICE_TEST_TSHARK to a tshark program")
	}
	var pkts [][]byte
	var mu sync.Mutex
	s := &Sender{T140: 98, Red: 100, SSRC: 9, Write: func(p []byte) { mu.Lock(); pkts = append(pkts, p); mu.Unlock() }}
	for _, w := range []string{"HEL", "LO", "", "911"} {
		if w != "" {
			s.Send(w)
		}
		time.Sleep(Interval + 30*time.Millisecond)
	}
	time.Sleep(3 * (Interval + 30*time.Millisecond))
	s.Close()
	mu.Lock()
	f := filepath.Join(t.TempDir(), "rtt.pcap")
	if err := os.WriteFile(f, pcap(pkts), 0o600); err != nil {
		t.Fatal(err)
	}
	mu.Unlock()
	out, err := exec.Command(tshark, "-r", f, "-d", "udp.port==5004,rtp", "-d", "rtp.pt==100,rtp_rfc2198",
		"-T", "fields", "-E", "separator=|", "-e", "rtp.seq", "-e", "rtp.p_type", "-e", "rtp.block-length", "-e", "rtp.payload").Output()
	if err != nil {
		t.Fatal(err)
	}
	r := &Receiver{T140: 98, Red: 100}
	var text strings.Builder
	for _, p := range pkts {
		text.WriteString(r.Packet(p))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(pkts) {
		t.Fatalf("Wireshark saw %d packets, we sent %d:\n%s", len(lines), len(pkts), out)
	}
	var primaries strings.Builder
	for i, l := range lines {
		f := strings.Split(l, "|")
		pts := strings.Split(f[1], ",")
		if pts[0] != "100" || len(pts) != Level+2 {
			t.Fatalf("packet %d: payload types %q (want RED, then a T.140 type per block)", i, f[1])
		}
		lens := strings.Split(f[2], ",")
		payloads := strings.Split(f[3], ",")
		if len(lens) != Level || len(payloads) < 1 {
			t.Fatalf("packet %d: blocks %q, payloads %q", i, f[2], f[3])
		}
		// Wireshark lists each non-empty block's bytes; the last is the primary when it is not empty
		blocks, _ := splitRed(pkts[i][12:], 98)
		for k := 0; k < Level; k++ {
			if lens[k] != strconv.Itoa(len(blocks[k])) {
				t.Fatalf("packet %d block %d: Wireshark length %s, ours %d", i, k, lens[k], len(blocks[k]))
			}
		}
		if p := blocks[Level]; len(p) > 0 {
			hex := payloads[len(payloads)-1]
			if want := fmt.Sprintf("%x", p); hex != want {
				t.Fatalf("packet %d primary: Wireshark %s, ours %s", i, hex, want)
			}
			primaries.Write(p)
		}
	}
	if got := clean([]byte(primaries.String())); got != "HELLO911" || text.String() != "HELLO911" {
		t.Fatalf("primaries %q, received %q", got, text.String())
	}
}
