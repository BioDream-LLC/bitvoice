package msrp

import (
	"bufio"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pair runs a session on one end of a pipe and returns the other end, as the TCC.
func pair(t *testing.T) (*Session, net.Conn, chan Message) {
	t.Helper()
	a, b := net.Pipe()
	s := New("msrp://psap.example.gov:2855/s1;tcp", "msrp://tcc.example.net:7777/t1;tcp")
	got := make(chan Message, 8)
	s.OnMessage = func(m Message) { got <- m }
	go s.Serve(a)
	t.Cleanup(func() { s.Close(); b.Close() })
	return s, b, got
}

// response reads one frame the session wrote.
func response(t *testing.T, r *bufio.Reader) *frame {
	t.Helper()
	f, err := read(r)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The SEND of RFC 4975 section 7.1 (the example's paths changed to this session's), and its 200.
func TestRFC4975Example(t *testing.T) {
	_, c, got := pair(t)
	r := bufio.NewReader(c)
	go c.Write([]byte("MSRP a786hjs2 SEND\r\nTo-Path: msrp://psap.example.gov:2855/s1;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\n" +
		"Message-ID: 87652491\r\nByte-Range: 1-25/25\r\nContent-Type: text/plain\r\n\r\nHey Bob, are you there?\r\n-------a786hjs2$\r\n"))
	f := response(t, r)
	if f.status != 200 || f.tid != "a786hjs2" || f.get("To-Path") != "msrp://tcc.example.net:7777/t1;tcp" || f.get("From-Path") != "msrp://psap.example.gov:2855/s1;tcp" {
		t.Fatalf("response %+v", f)
	}
	m := <-got
	if text, ok := m.Text(); !ok || text != "Hey Bob, are you there?" {
		t.Fatalf("message %+v", m)
	}
}

// A message in two chunks, a body holding something like an end line, and CPIM-wrapped text.
func TestChunksAndCPIM(t *testing.T) {
	_, c, got := pair(t)
	r := bufio.NewReader(c)
	go func() {
		w := func(s string) { _, _ = c.Write([]byte(s)) }
		w("MSRP t1 SEND\r\nTo-Path: msrp://psap.example.gov:2855/s1;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\nMessage-ID: m1\r\nByte-Range: 1-10/20\r\nContent-Type: text/plain\r\n\r\n0123456789\r\n-------t1+\r\n")
		w("MSRP t2 SEND\r\nTo-Path: msrp://psap.example.gov:2855/s1;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\nMessage-ID: m1\r\nByte-Range: 11-20/20\r\nContent-Type: text/plain\r\n\r\nab-------t1$\r\n-------t2$\r\n")
		w("MSRP t3 SEND\r\nTo-Path: msrp://psap.example.gov:2855/s1;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\nMessage-ID: m2\r\nByte-Range: 1-*/*\r\nContent-Type: message/cpim\r\n\r\n" +
			"From: <sip:+12125550199@tcc.example.net>\r\nTo: <urn:service:sos>\r\nDateTime: 2026-10-15T12:00:00Z\r\n\r\nContent-Type: text/plain\r\n\r\nPLEASE HELP\r\n-------t3$\r\n")
		w("MSRP t4 SEND\r\nTo-Path: msrp://psap.example.gov:2855/s1;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\nMessage-ID: m3\r\nByte-Range: 1-*/*\r\nContent-Type: application/im-iscomposing+xml\r\n\r\n<isComposing/>\r\n-------t4$\r\n")
	}()
	for _, tid := range []string{"t1", "t2", "t3", "t4"} {
		if f := response(t, r); f.status != 200 || f.tid != tid {
			t.Fatalf("response %+v", f)
		}
	}
	if m := <-got; string(m.Body) != "0123456789ab-------t1$" {
		t.Fatalf("chunked %q", m.Body)
	}
	if text, _ := (<-got).Text(); text != "PLEASE HELP" {
		t.Fatalf("cpim %q", text)
	}
	if text, ok := (<-got).Text(); ok {
		t.Fatalf("is-composing read as text %q", text)
	}
}

// A request for another session is refused with 481 and the connection closed.
func TestOtherSession(t *testing.T) {
	_, c, _ := pair(t)
	r := bufio.NewReader(c)
	go c.Write([]byte("MSRP x SEND\r\nTo-Path: msrp://psap.example.gov:2855/other;tcp\r\nFrom-Path: msrp://tcc.example.net:7777/t1;tcp\r\nMessage-ID: 1\r\nByte-Range: 1-1/1\r\nContent-Type: text/plain\r\n\r\nA\r\n-------x$\r\n"))
	if f := response(t, r); f.status != 481 {
		t.Fatalf("response %+v", f)
	}
}

// What the session sends is a well-formed SEND the far end can read.
func TestSend(t *testing.T) {
	s, c, _ := pair(t)
	r := bufio.NewReader(c)
	if !s.Wait(time.Second) {
		t.Fatal("not connected")
	}
	go s.Send("WHAT IS YOUR ADDRESS?")
	f := response(t, r)
	if f.method != "SEND" || f.flag != '$' || string(f.body) != "WHAT IS YOUR ADDRESS?" || f.get("Byte-Range") != "1-21/21" || f.get("To-Path") != s.Remote || !strings.HasPrefix(f.get("Content-Type"), "text/plain") {
		t.Fatalf("sent %+v", f)
	}
}

func FuzzRead(f *testing.F) {
	f.Add([]byte("MSRP a SEND\r\nTo-Path: x\r\n\r\nhi\r\n-------a$\r\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bufio.NewReader(strings.NewReader(string(b)))
		for i := 0; i < 4; i++ {
			if _, err := read(r); err != nil {
				return
			}
		}
	})
}

// tcpPcap writes a byte stream as TCP segments from port 2855 (raw IPv4 link type).
func tcpPcap(segs [][]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0xa1b2c3d4)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = append(b, make([]byte, 8)...)
	b = binary.LittleEndian.AppendUint32(b, 65535)
	b = binary.LittleEndian.AppendUint32(b, 101)
	seq := uint32(1000)
	for i, p := range segs {
		n := 40 + len(p)
		b = binary.LittleEndian.AppendUint32(b, uint32(i))
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
		ip := []byte{0x45, 0, byte(n >> 8), byte(n), 0, 0, 0, 0, 64, 6, 0, 0, 127, 0, 0, 1, 127, 0, 0, 2}
		var sum uint32
		for k := 0; k < 20; k += 2 {
			sum += uint32(ip[k])<<8 | uint32(ip[k+1])
		}
		for sum > 0xffff {
			sum = sum&0xffff + sum>>16
		}
		binary.BigEndian.PutUint16(ip[10:], ^uint16(sum))
		b = append(b, ip...)
		tcp := make([]byte, 20)
		binary.BigEndian.PutUint16(tcp[0:], 2855)
		binary.BigEndian.PutUint16(tcp[2:], 7777)
		binary.BigEndian.PutUint32(tcp[4:], seq)
		binary.BigEndian.PutUint32(tcp[8:], 1)
		tcp[12] = 5 << 4
		tcp[13] = 0x18 // PSH, ACK
		binary.BigEndian.PutUint16(tcp[14:], 65535)
		b = append(b, tcp...)
		b = append(b, p...)
		seq += uint32(len(p))
	}
	return b
}

// Against Wireshark's MSRP dissector: what the session sends parses as the SEND and the 200 we meant.
func TestAgainstWireshark(t *testing.T) {
	tshark := os.Getenv("BITVOICE_TEST_TSHARK")
	if tshark == "" {
		t.Skip("set BITVOICE_TEST_TSHARK to a tshark program")
	}
	a, b := net.Pipe()
	s := New("msrp://127.0.0.1:2855/s1;tcp", "msrp://127.0.0.2:7777/t1;tcp")
	go s.Serve(a)
	defer s.Close()
	var segs [][]byte
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := b.Read(buf)
			if err != nil {
				close(done)
				return
			}
			segs = append(segs, append([]byte(nil), buf[:n]...))
		}
	}()
	s.Wait(time.Second)
	_ = s.Send("ARE YOU SAFE? ✓")
	go b.Write([]byte("MSRP q1 SEND\r\nTo-Path: msrp://127.0.0.1:2855/s1;tcp\r\nFrom-Path: msrp://127.0.0.2:7777/t1;tcp\r\nMessage-ID: 1\r\nByte-Range: 1-3/3\r\nContent-Type: text/plain\r\n\r\nYES\r\n-------q1$\r\n"))
	time.Sleep(200 * time.Millisecond)
	s.Close()
	<-done
	f := filepath.Join(t.TempDir(), "msrp.pcap")
	if err := os.WriteFile(f, tcpPcap(segs), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tshark, "-r", f, "-d", "tcp.port==2855,msrp", "-T", "fields", "-E", "separator=|",
		"-e", "msrp.method", "-e", "msrp.status.code", "-e", "msrp.byte.range", "-e", "msrp.to.path", "-e", "msrp.end.line").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "SEND||1-17/17|msrp://127.0.0.2:7777/t1;tcp|") || !strings.HasPrefix(lines[1], "|200||msrp://127.0.0.2:7777/t1;tcp|") {
		t.Fatalf("Wireshark read:\n%s", out)
	}
}
