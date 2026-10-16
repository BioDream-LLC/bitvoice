//go:build linux

package serial

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const ioctlGet, ioctlSet = syscall.TCGETS, syscall.TCSETS

var speeds = map[int]uint32{1200: syscall.B1200, 2400: syscall.B2400, 4800: syscall.B4800, 9600: syscall.B9600, 19200: syscall.B19200,
	38400: syscall.B38400, 57600: syscall.B57600, 115200: syscall.B115200}

const cbaud = 0x100f // CBAUD | CBAUDEX

func setSpeed(t *syscall.Termios, baud int) error {
	b, ok := speeds[baud]
	if !ok {
		return fmt.Errorf("speed %d is not offered", baud)
	}
	t.Cflag = t.Cflag&^cbaud | b
	t.Ispeed, t.Ospeed = b, b
	return nil
}

// PTY opens a pseudo-terminal pair: the master end, and the device name of the other end, which
// Open takes like any serial port. For tests and for simulating serial equipment.
func PTY() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, "", e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, "", e
	}
	return m, "/dev/pts/" + strconv.Itoa(int(n)), nil
}
