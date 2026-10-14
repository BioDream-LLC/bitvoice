package ws

import (
	"bufio"
	"bytes"
	"net"
	"testing"
	"time"
)

// sink is a connection that takes writes and reads nothing.
type sink struct{ net.Conn }

func (sink) Write(b []byte) (int, error)      { return len(b), nil }
func (sink) Close() error                     { return nil }
func (sink) SetReadDeadline(time.Time) error  { return nil }
func (sink) SetWriteDeadline(time.Time) error { return nil }

func masked(op byte, fin bool, payload []byte) []byte {
	h := op
	if fin {
		h |= 0x80
	}
	out := []byte{h, 0x80 | byte(len(payload)), 9, 8, 7, 6}
	for i, b := range payload {
		out = append(out, b^[]byte{9, 8, 7, 6}[i%4])
	}
	return out
}

// Fragmented text with a ping between the pieces reads as one message; unmasked frames are refused.
func TestRead(t *testing.T) {
	data := append(masked(OpText, false, []byte("REGISTER sip:")), masked(opPing, true, []byte("p"))...)
	data = append(data, masked(0, true, []byte("x SIP/2.0"))...)
	c := &Conn{c: sink{}, r: bufio.NewReader(bytes.NewReader(data)), MaxSize: 1024}
	op, msg, err := c.Read(time.Second)
	if err != nil || op != OpText || string(msg) != "REGISTER sip:x SIP/2.0" {
		t.Fatalf("%d %q %v", op, msg, err)
	}
	c = &Conn{c: sink{}, r: bufio.NewReader(bytes.NewReader([]byte{0x81, 3, 'a', 'b', 'c'})), MaxSize: 1024}
	if _, _, err := c.Read(time.Second); err == nil {
		t.Fatal("an unmasked frame was accepted")
	}
	big := masked(OpText, true, bytes.Repeat([]byte("a"), 100))
	c = &Conn{c: sink{}, r: bufio.NewReader(bytes.NewReader(big)), MaxSize: 50}
	if _, _, err := c.Read(time.Second); err == nil {
		t.Fatal("a message over the limit was accepted")
	}
}

func FuzzRead(f *testing.F) {
	f.Add(masked(OpText, true, []byte("hello")))
	f.Add([]byte{0x81, 0xFF, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, b []byte) {
		c := &Conn{c: sink{}, r: bufio.NewReader(bytes.NewReader(b)), MaxSize: 4096}
		for range 4 {
			if _, _, err := c.Read(time.Second); err != nil {
				return
			}
		}
	})
}
