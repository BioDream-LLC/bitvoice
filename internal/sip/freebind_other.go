//go:build !linux

package sip

import "net"

// Elsewhere an address must be held to be bound.
func listenFreeUDP(addr string) (*net.UDPConn, error) {
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp", a)
}

func listenFreeTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

func listenSharedUDP(addr string) (*net.UDPConn, error) { return listenFreeUDP(addr) }

func listenSharedTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
