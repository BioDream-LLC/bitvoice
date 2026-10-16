// Package serial opens serial ports (RS-232, USB serial adapters) in raw mode, for legacy links such
// as ALI: termios through the standard library's syscall package on Linux and macOS. Other systems
// get an error saying serial ports are not offered there.
package serial

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config is how a port is opened.
type Config struct {
	Device   string `json:"device"`   // /dev/ttyUSB0, /dev/cu.usbserial-1410
	Baud     int    `json:"baud"`     // 1200 to 115200 (9600 when 0)
	DataBits int    `json:"dataBits"` // 7 or 8 (8 when 0)
	Parity   string `json:"parity"`   // none, even, odd ("" is none)
	StopBits int    `json:"stopBits"` // 1 or 2 (1 when 0)
}

// Bauds are the speeds offered.
var Bauds = []int{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200}

// Check says what is wrong with a configuration, or nil.
func (c Config) Check() error {
	if !strings.HasPrefix(c.Device, "/dev/") {
		return errors.New("a serial device, like /dev/ttyUSB0")
	}
	if c.Baud != 0 && !contains(Bauds, c.Baud) {
		return fmt.Errorf("a speed of %v", Bauds)
	}
	if c.DataBits != 0 && c.DataBits != 7 && c.DataBits != 8 {
		return errors.New("7 or 8 data bits")
	}
	switch c.Parity {
	case "", "none", "even", "odd":
	default:
		return errors.New("parity none, even or odd")
	}
	if c.StopBits != 0 && c.StopBits != 1 && c.StopBits != 2 {
		return errors.New("1 or 2 stop bits")
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.Baud == 0 {
		c.Baud = 9600
	}
	if c.DataBits == 0 {
		c.DataBits = 8
	}
	if c.Parity == "" {
		c.Parity = "none"
	}
	if c.StopBits == 0 {
		c.StopBits = 1
	}
	return c
}

// String is the configuration as written in an address: serial:/dev/ttyUSB0?baud=9600&parity=even.
func (c Config) String() string {
	c = c.withDefaults()
	return fmt.Sprintf("serial:%s?baud=%d&bits=%d&parity=%s&stop=%d", c.Device, c.Baud, c.DataBits, c.Parity, c.StopBits)
}

// Parse reads an address written as String writes it.
func Parse(s string) (Config, error) {
	rest, ok := strings.CutPrefix(s, "serial:")
	if !ok {
		return Config{}, errors.New("not a serial: address")
	}
	dev, q, _ := strings.Cut(rest, "?")
	v, _ := url.ParseQuery(q)
	n := func(k string) int { i, _ := strconv.Atoi(v.Get(k)); return i }
	c := Config{Device: dev, Baud: n("baud"), DataBits: n("bits"), Parity: v.Get("parity"), StopBits: n("stop")}
	return c, c.Check()
}

func contains(l []int, x int) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

// Port is an open serial port. Reads and writes honour deadlines.
type Port interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}
