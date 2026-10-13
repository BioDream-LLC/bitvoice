//go:build windows

package sip

import (
	"errors"
	"syscall"
)

// wsaeaddrinuse is Winsock's "address already in use"; Go's syscall.EADDRINUSE is a different number
// on Windows, so a check against it alone never matches there.
const wsaeaddrinuse = syscall.Errno(10048)

// addrInUse reports whether a listen failed because something else holds the address.
func addrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse) || errors.Is(err, syscall.EADDRINUSE)
}
