package vip

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"
)

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// announce sends gratuitous ARP (a request and a reply naming the address as this interface's), three
// times, so switches and neighbours send traffic for it here at once. IPv6 neighbours learn it from the
// kernel's own advertisement when the address is added.
func announce(ifname string, ip netip.Addr) error {
	if !ip.Is4() {
		return nil
	}
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		return err
	}
	if len(ifi.HardwareAddr) != 6 {
		return errors.New("vip: the interface has no Ethernet address")
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethPARP)))
	if err != nil {
		return errors.New("vip: cannot announce the address: give BitVoice CAP_NET_RAW")
	}
	defer syscall.Close(fd)
	to := &syscall.SockaddrLinklayer{Protocol: htons(ethPARP), Ifindex: ifi.Index, Halen: 6,
		Addr: [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	for i := 0; i < 3; i++ {
		for _, op := range []uint16{1, 2} { // request, then reply: different devices heed different ones
			if err := syscall.Sendto(fd, frame(ifi.HardwareAddr, ip, op), 0, to); err != nil {
				return err
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
