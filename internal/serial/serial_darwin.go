//go:build darwin

package serial

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const ioctlGet, ioctlSet = syscall.TIOCGETA, syscall.TIOCSETA

func setSpeed(t *syscall.Termios, baud int) error {
	t.Ispeed, t.Ospeed = uint64(baud), uint64(baud) // macOS takes the speed as a number
	return nil
}

// PTY opens a pseudo-terminal pair: the master end, and the device name of the other end, which
// Open takes like any serial port. For tests and for simulating serial equipment.
func PTY() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	fd := m.Fd()
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCPTYGRANT, 0); e != 0 {
		m.Close()
		return nil, "", e
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCPTYUNLK, 0); e != 0 {
		m.Close()
		return nil, "", e
	}
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		m.Close()
		return nil, "", e
	}
	n := bytes.IndexByte(name[:], 0)
	if n <= 0 {
		m.Close()
		return nil, "", errors.New("no pty name")
	}
	return m, string(name[:n]), nil
}
