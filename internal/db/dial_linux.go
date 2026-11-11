package db

import "syscall"

// tcpUserTimeout is TCP_USER_TIMEOUT (linux/tcp.h), which package syscall does not name.
const tcpUserTimeout = 0x12

// limitUnacked gives up a connection whose sent data goes unacknowledged for longer than unacked, so a
// query on a connection to a database that went quiet fails instead of waiting out TCP's retries.
func limitUnacked(network, address string, c syscall.RawConn) error {
	var err error
	if cerr := c.Control(func(fd uintptr) {
		err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout, int(unacked.Milliseconds()))
	}); cerr != nil {
		return cerr
	}
	return err
}
