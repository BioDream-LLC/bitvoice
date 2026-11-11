//go:build !linux

package db

import "syscall"

// limitUnacked does nothing here: the deadlines on queries and keepalive bound the wait instead.
func limitUnacked(network, address string, c syscall.RawConn) error { return nil }
