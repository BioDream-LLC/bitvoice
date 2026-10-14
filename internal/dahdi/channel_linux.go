//go:build linux

package dahdi

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Linux ioctl numbers: direction in the top two bits, then the argument's size, the type, the number.
func ioW(nr, size uintptr) uintptr  { return 1<<30 | size<<16 | 0xDA<<8 | nr }
func ioR(nr, size uintptr) uintptr  { return 2<<30 | size<<16 | 0xDA<<8 | nr }
func ioWR(nr, size uintptr) uintptr { return 3<<30 | size<<16 | 0xDA<<8 | nr }

var (
	ioSetBlocksize = ioW(1, 4)
	ioFlush        = ioW(3, 4)
	ioHook         = ioW(7, 4)
	ioGetEvent     = ioR(8, 4)
	ioSendTone     = ioW(21, 4)
	ioSetBufinfo   = ioW(27, 24)
	ioSpecify      = ioW(38, 4)
	ioSetLaw       = ioW(39, 4)
	_              = ioWR
)

const errLast = syscall.Errno(500) // ELAST: the driver has an event for us

type linuxChannel struct {
	f  *os.File
	fd uintptr
}

func ioctlInt(fd, req uintptr, v *int32) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(v))); e != 0 {
		return e
	}
	return nil
}

func openChannel(number int, alaw bool) (Channel, error) {
	f, err := os.OpenFile("/dev/dahdi/channel", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("dahdi: %w (are the DAHDI drivers loaded, and may this program use /dev/dahdi?)", err)
	}
	c := &linuxChannel{f: f, fd: f.Fd()}
	n := int32(number)
	if err := ioctlInt(c.fd, ioSpecify, &n); err != nil {
		f.Close()
		return nil, fmt.Errorf("dahdi: channel %d: %w (is it set up in /etc/dahdi/system.conf?)", number, err)
	}
	bs := int32(FrameBytes)
	if err := ioctlInt(c.fd, ioSetBlocksize, &bs); err != nil {
		f.Close()
		return nil, fmt.Errorf("dahdi: channel %d block size: %w", number, err)
	}
	law := int32(1)
	if alaw {
		law = 2
	}
	_ = ioctlInt(c.fd, ioSetLaw, &law)
	// Four buffers of 20 ms, played at once: little delay, and room for a late packet.
	bi := [6]int32{0, 0, 4, FrameBytes, 0, 0}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, c.fd, ioSetBufinfo, uintptr(unsafe.Pointer(&bi[0]))); e != 0 {
		f.Close()
		return nil, fmt.Errorf("dahdi: channel %d buffers: %w", number, e)
	}
	return c, nil
}

func (c *linuxChannel) Read(b []byte) (int, error) {
	n, err := syscall.Read(int(c.fd), b)
	if err == errLast {
		return 0, ErrEvent
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (c *linuxChannel) Write(b []byte) (int, error) {
	n, err := syscall.Write(int(c.fd), b)
	if err == errLast {
		return 0, ErrEvent
	}
	return n, err
}

func (c *linuxChannel) Event() (int, error) {
	var v int32
	err := ioctlInt(c.fd, ioGetEvent, &v)
	return int(v), err
}

func (c *linuxChannel) Hook(op int) error {
	v := int32(op)
	return ioctlInt(c.fd, ioHook, &v)
}

func (c *linuxChannel) Tone(t int) error {
	v := int32(t)
	return ioctlInt(c.fd, ioSendTone, &v)
}

func (c *linuxChannel) Close() error {
	v := int32(3) // flush both ways
	_ = ioctlInt(c.fd, ioFlush, &v)
	return c.f.Close()
}

var _ = eventPoll
