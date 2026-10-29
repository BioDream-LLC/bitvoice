//go:build !windows

package main

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// lockFile opens path and takes an exclusive advisory lock on it: a second BitVoice cannot. The system
// lets go when the process ends, however it ends.
func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errAlreadyRunning
		}
		return nil, err
	}
	return f, nil
}

func readShared(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 4096))
}
