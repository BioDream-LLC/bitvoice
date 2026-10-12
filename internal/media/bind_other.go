//go:build !linux

package media

import "net"

func listenMedia(host string, port int) (*net.UDPConn, error) {
	return net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(host), Port: port})
}
