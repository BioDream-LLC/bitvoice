//go:build darwin || linux

package serial

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Open opens a serial port raw (no echo, no line editing, no flow control).
func Open(c Config) (Port, error) {
	if err := c.Check(); err != nil {
		return nil, err
	}
	c = c.withDefaults()
	f, err := os.OpenFile(c.Device, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := configure(f, c); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", c.Device, err)
	}
	return f, nil
}

func configure(f *os.File, c Config) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	err = rc.Control(func(fd uintptr) {
		var t syscall.Termios
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGet, uintptr(unsafe.Pointer(&t))); e != 0 {
			serr = e
			return
		}
		makeRaw(&t)
		t.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.PARODD | syscall.CSTOPB
		t.Cflag |= syscall.CREAD | syscall.CLOCAL
		if c.DataBits == 7 {
			t.Cflag |= syscall.CS7
		} else {
			t.Cflag |= syscall.CS8
		}
		switch c.Parity {
		case "even":
			t.Cflag |= syscall.PARENB
		case "odd":
			t.Cflag |= syscall.PARENB | syscall.PARODD
		}
		if c.StopBits == 2 {
			t.Cflag |= syscall.CSTOPB
		}
		if serr = setSpeed(&t, c.Baud); serr != nil {
			return
		}
		t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 1, 0
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSet, uintptr(unsafe.Pointer(&t))); e != 0 {
			serr = e
		}
	})
	if err != nil {
		return err
	}
	return serr
}

func makeRaw(t *syscall.Termios) {
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
}
