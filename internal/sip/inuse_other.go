//go:build !windows

package sip

import (
	"errors"
	"syscall"
)

// addrInUse reports whether a listen failed because something else holds the address.
func addrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
