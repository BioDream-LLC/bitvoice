package sip

import (
	"context"
	"net"
	"syscall"
	"unsafe"
)

// soExclusiveAddrUse is Winsock's SO_EXCLUSIVEADDRUSE (~SO_REUSEADDR).
const soExclusiveAddrUse = ^int32(4)

// listenUDP takes the SIP port for this program alone. Windows otherwise lets a socket on every
// address share a port another program holds on one address (a softphone on 5060 on the computer's
// own address, say), and what phones send to that address goes to the other program. Exclusive, the
// bind fails and says the port is in use. TCP stays as it was: an exclusive listener cannot be opened
// again while its old connections wait out TIME_WAIT, which would stop a restart after an upgrade.
func listenUDP(ua *net.UDPAddr) (*net.UDPConn, error) {
	pc, err := (&net.ListenConfig{Control: exclusive}).ListenPacket(context.Background(), "udp", ua.String())
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func listenTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

func exclusive(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		on := int32(1)
		serr = syscall.Setsockopt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, (*byte)(unsafe.Pointer(&on)), 4)
	}); err != nil {
		return err
	}
	return serr
}
