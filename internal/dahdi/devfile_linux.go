//go:build linux

package dahdi

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// errClosed is what a read or write on a closed channel returns.
var errClosed = errors.New("dahdi: channel closed")

// devFile is an open /dev/dahdi/channel. Reads wait in poll for at most pollEvery, so a close is seen
// at once; Close waits for any read or write in progress, then closes the file, so when it returns the
// channel is free for the next program (or the next bridge) to open. A plain blocking read would keep
// the driver's channel open until data came (a quiet D-channel: up to T203, ten seconds), and a read
// that started after the close could land on a reused descriptor number.
type devFile struct {
	f  *os.File
	fd uintptr

	mu     sync.Mutex
	closed bool
	busy   sync.WaitGroup
}

const pollEvery = 100 * time.Millisecond

func newDevFile(f *os.File) *devFile { return &devFile{f: f, fd: f.Fd()} }

// enter marks a read or write in progress; false when the file is closed.
func (d *devFile) enter() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.busy.Add(1)
	return true
}

// read waits until the driver has data or an event, then reads.
func (d *devFile) read(b []byte) (int, error) {
	for {
		if !d.enter() {
			return 0, errClosed
		}
		ready, err := pollIn(int(d.fd), pollEvery)
		if err != nil || !ready {
			d.busy.Done()
			if err != nil && err != syscall.EINTR {
				return 0, err
			}
			continue
		}
		n, err := syscall.Read(int(d.fd), b)
		d.busy.Done()
		return n, err
	}
}

func (d *devFile) write(b []byte) (int, error) {
	if !d.enter() {
		return 0, errClosed
	}
	defer d.busy.Done()
	return syscall.Write(int(d.fd), b)
}

// ioctl runs one ioctl on the open file.
func (d *devFile) ioctl(req uintptr, arg unsafe.Pointer) error {
	if !d.enter() {
		return errClosed
	}
	defer d.busy.Done()
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, d.fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// close stops new reads and writes, waits for those in progress (a read returns within pollEvery),
// then closes the file. flush runs first, while the file is still open.
func (d *devFile) close(flush func()) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()
	d.busy.Wait()
	if flush != nil {
		flush()
	}
	return d.f.Close()
}

// pollIn reports whether fd has data or an event (POLLIN, POLLPRI) within wait.
func pollIn(fd int, wait time.Duration) (bool, error) {
	const pollIn, pollPri = 0x1, 0x2
	pfd := struct {
		fd      int32
		events  int16
		revents int16
	}{fd: int32(fd), events: pollIn | pollPri}
	ts := syscall.NsecToTimespec(wait.Nanoseconds())
	n, _, e := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&pfd)), 1, uintptr(unsafe.Pointer(&ts)), 0, 0, 0)
	if e != 0 {
		return false, e
	}
	return n > 0, nil
}
