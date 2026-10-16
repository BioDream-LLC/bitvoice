//go:build !darwin && !linux

package serial

import (
	"errors"
	"os"
)

// Open is not offered on this system.
func Open(c Config) (Port, error) {
	return nil, errors.New("serial ports are offered on Linux and macOS only; use a serial-to-network converter and its TCP address")
}

// PTY is not offered on this system.
func PTY() (*os.File, string, error) { return nil, "", errors.New("no pseudo-terminals here") }
