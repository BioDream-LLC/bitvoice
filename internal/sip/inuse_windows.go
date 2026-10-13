//go:build windows

package sip

import (
	"errors"
	"syscall"
)

// Winsock's own codes: Go's syscall.EADDRINUSE is a different number on Windows, so a check against it
// alone never matches there. WSAEACCES is what Windows returns for a port in one of its reserved ranges
// (Hyper-V and WinNAT reserve blocks of ports), which a free UDP port's number can fall into for TCP.
const (
	wsaeaddrinuse = syscall.Errno(10048)
	wsaeacces     = syscall.Errno(10013)
)

// addrInUse reports whether a listen failed because the port cannot be had, so another should be tried.
func addrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse) || errors.Is(err, wsaeacces) || errors.Is(err, syscall.EADDRINUSE)
}
