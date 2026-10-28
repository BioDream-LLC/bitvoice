//go:build !windows

package sip

import "net"

func listenUDP(ua *net.UDPAddr) (*net.UDPConn, error) { return net.ListenUDP("udp", ua) }

func listenTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
