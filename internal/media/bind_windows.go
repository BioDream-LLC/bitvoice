package media

import (
	"context"
	"net"
	"strconv"
	"syscall"
	"unsafe"
)

// soExclusiveAddrUse is Winsock's SO_EXCLUSIVEADDRUSE (~SO_REUSEADDR).
const soExclusiveAddrUse = ^int32(4)

// listenMedia binds a media port for this program alone. Windows otherwise lets a socket on every
// address share a port another program holds on one address (NVIDIA's nvcontainer keeps
// 127.0.0.1:10010, for one), and packets to that address go to the other program: a call that is
// silent one way. Exclusive, the bind fails and the next port is tried; nothing can take the port
// over afterwards either.
func listenMedia(host string, port int) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: exclusive}
	pc, err := lc.ListenPacket(context.Background(), "udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

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
