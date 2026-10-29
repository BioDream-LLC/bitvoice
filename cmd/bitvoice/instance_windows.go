//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall does not name.
const errSharingViolation syscall.Errno = 32

// lockFile opens path for writing with no one else allowed to write it while it is open: a second
// BitVoice gets a sharing violation. Windows lets go when the process ends, however it ends.
func lockFile(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, errSharingViolation) {
		return nil, errAlreadyRunning
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// readShared reads a file another process holds open for writing.
func readShared(path string) ([]byte, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 4096))
}
