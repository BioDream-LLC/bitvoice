package sip

import (
	"context"
	"net"
	"syscall"
)

const soReusePort = 15 // SO_REUSEPORT

// Linux lets a socket bind an address the machine does not hold yet (IP_FREEBIND), beside the main
// socket on every address (SO_REUSEPORT on both; the kernel gives each packet to the closest match).
func freebind(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_IP, syscall.IP_FREEBIND, 1)
		if serr == nil {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}

func listenFreeUDP(addr string) (*net.UDPConn, error) {
	pc, err := (&net.ListenConfig{Control: freebind}).ListenPacket(context.Background(), "udp", addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func listenFreeTCP(addr string) (net.Listener, error) {
	return (&net.ListenConfig{Control: freebind}).Listen(context.Background(), "tcp", addr)
}

// reusePort lets the main sockets share their port with an extra address's.
func reusePort(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
	})
	if err != nil {
		return err
	}
	return serr
}

func listenSharedUDP(addr string) (*net.UDPConn, error) {
	pc, err := (&net.ListenConfig{Control: reusePort}).ListenPacket(context.Background(), "udp", addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func listenSharedTCP(addr string) (net.Listener, error) {
	return (&net.ListenConfig{Control: reusePort}).Listen(context.Background(), "tcp", addr)
}
