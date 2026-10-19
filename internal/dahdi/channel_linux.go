//go:build linux

package dahdi

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/biodream-llc/bitvoice/internal/isdn"
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
	ioGetRxBits    = ioR(43, 4)
	ioSetTxBits    = ioW(43, 4)
	_              = ioWR
)

const errLast = syscall.Errno(500) // ELAST: the driver has an event for us

type linuxChannel struct {
	d  *devFile
	fd uintptr // for set-up only, before the channel is handed out
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
	c := &linuxChannel{d: newDevFile(f)}
	c.fd = c.d.fd
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
	n, err := c.d.read(b)
	if err == errLast {
		return 0, ErrEvent
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (c *linuxChannel) Write(b []byte) (int, error) {
	n, err := c.d.write(b)
	if err == errLast {
		return 0, ErrEvent
	}
	return n, err
}

func (c *linuxChannel) intIoctl(req uintptr, v *int32) error {
	return c.d.ioctl(req, unsafe.Pointer(v))
}

func (c *linuxChannel) Event() (int, error) {
	var v int32
	err := c.intIoctl(ioGetEvent, &v)
	return int(v), err
}

func (c *linuxChannel) Hook(op int) error {
	v := int32(op)
	return c.intIoctl(ioHook, &v)
}

func (c *linuxChannel) Tone(t int) error {
	v := int32(t)
	return c.intIoctl(ioSendTone, &v)
}

func (c *linuxChannel) RxBits() (int, error) {
	var v int32
	err := c.intIoctl(ioGetRxBits, &v)
	return int(v), err
}

func (c *linuxChannel) TxBits(abcd int) error {
	v := int32(abcd)
	return c.intIoctl(ioSetTxBits, &v)
}

// Close returns once the channel is free again for anyone to open.
func (c *linuxChannel) Close() error {
	return c.d.close(func() {
		v := int32(3) // flush both ways
		_ = ioctlInt(c.d.fd, ioFlush, &v)
	})
}

var _ = eventPoll

// dChannel is a PRI's D-channel: the driver frames it (HDLC flags and FCS), and every frame read
// carries two FCS octets at its end, and every frame written two places for them, as libpri has it.
type dChannel struct {
	d  *devFile
	fd uintptr // for set-up only
}

func openDChannel(number int) (isdn.Link, error) {
	f, err := os.OpenFile("/dev/dahdi/channel", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("dahdi: %w (are the DAHDI drivers loaded?)", err)
	}
	c := &dChannel{d: newDevFile(f)}
	c.fd = c.d.fd
	n := int32(number)
	if err := ioctlInt(c.fd, ioSpecify, &n); err != nil {
		f.Close()
		return nil, fmt.Errorf("dahdi: D-channel %d: %w (is it a dchan, hardhdlc or bchan in /etc/dahdi/system.conf?)", number, err)
	}
	bi := [6]int32{0, 0, 16, 1024, 0, 0}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, c.fd, ioSetBufinfo, uintptr(unsafe.Pointer(&bi[0]))); e != 0 {
		f.Close()
		return nil, fmt.Errorf("dahdi: D-channel %d buffers: %w", number, e)
	}
	return c, nil
}

func (c *dChannel) ReadFrame() ([]byte, error) {
	buf := make([]byte, 1024)
	for {
		n, err := c.d.read(buf)
		if err == errLast {
			var v int32
			_ = c.d.ioctl(ioGetEvent, unsafe.Pointer(&v)) // alarms: the link layer notices by itself
			continue
		}
		if err != nil {
			return nil, err
		}
		if n > 2 {
			return append([]byte(nil), buf[:n-2]...), nil
		}
	}
}

func (c *dChannel) WriteFrame(f []byte) error {
	_, err := c.d.write(append(append([]byte(nil), f...), 0, 0))
	if err == errLast {
		return nil
	}
	return err
}

// Close returns once the D-channel is free again for anyone to open.
func (c *dChannel) Close() error { return c.d.close(nil) }
