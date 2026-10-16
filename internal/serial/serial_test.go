package serial

import (
	"bytes"
	"runtime"
	"testing"
	"time"
)

// A pseudo-terminal stands in for the cable: bytes written on one end come out raw on the other
// (no echo, no line editing, carriage returns and ^C as they are), and a read times out.
func TestPortOverPTY(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no serial ports here")
	}
	m, dev, err := PTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p, err := Open(Config{Device: dev, Baud: 9600, Parity: "even", DataBits: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	msg := []byte("\x02POS 1\r\n(717) 555-0100\r\n\x03\x03\x7f")
	if _, err := m.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 0, 64)
	buf := make([]byte, 64)
	p.SetReadDeadline(time.Now().Add(2 * time.Second))
	for len(got) < len(msg) {
		n, err := p.Read(buf)
		if err != nil {
			t.Fatalf("read %q: %v", got, err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q", got)
	}
	if _, err := p.Write([]byte("ANI\r")); err != nil {
		t.Fatal(err)
	}
	back := make([]byte, 16)
	m.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := m.Read(back)
	if string(back[:n]) != "ANI\r" {
		t.Fatalf("the other way: %q", back[:n])
	}
	p.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := p.Read(buf); err == nil {
		t.Fatal("no timeout")
	}
}

func TestParse(t *testing.T) {
	c, err := Parse("serial:/dev/ttyUSB0?baud=19200&parity=odd&stop=2")
	if err != nil || c.Device != "/dev/ttyUSB0" || c.Baud != 19200 || c.Parity != "odd" || c.StopBits != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Parse("serial:/dev/x?baud=1234"); err == nil {
		t.Fatal("an odd speed")
	}
	if c.String() != "serial:/dev/ttyUSB0?baud=19200&bits=8&parity=odd&stop=2" {
		t.Fatal(c.String())
	}
}
