// Package vip holds an address shared by several servers: the one holding it adds it to a network
// interface and tells the network (gratuitous ARP), so traffic for it arrives here at once.
package vip

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"strings"
)

// Address is one shared address on one interface.
type Address struct {
	Prefix    netip.Prefix
	Interface string
}

// Parse reads "192.0.2.50/24" and finds the interface whose network holds it, unless one is named.
func Parse(prefix, iface string) (Address, error) {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return Address{}, errors.New("vip: the shared address needs its network size, such as 192.0.2.50/24")
	}
	a := Address{Prefix: p, Interface: iface}
	if iface == "" {
		if a.Interface, err = interfaceFor(p.Addr()); err != nil {
			return Address{}, err
		}
	}
	return a, nil
}

func interfaceFor(ip netip.Addr) (string, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if p, err := netip.ParsePrefix(n.String()); err == nil && p.Masked().Contains(ip) && i.Flags&net.FlagLoopback == 0 {
					return i.Name, nil
				}
			}
		}
	}
	return "", errors.New("vip: no network interface is on the shared address's network: name the interface")
}

// Held says whether this machine holds the address now.
func (a Address) Held() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, x := range addrs {
		if n, ok := x.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok && ip.Unmap() == a.Prefix.Addr() {
				return true
			}
		}
	}
	return false
}

// Take adds the address to the interface and announces it.
func (a Address) Take() error {
	if !a.Held() {
		if err := ip("addr", "add", a.Prefix.String(), "dev", a.Interface); err != nil {
			return err
		}
	}
	return announce(a.Interface, a.Prefix.Addr())
}

// Release removes the address from the interface.
func (a Address) Release() error {
	if !a.Held() {
		return nil
	}
	return ip("addr", "del", a.Prefix.String(), "dev", a.Interface)
}

func ip(args ...string) error {
	path, err := exec.LookPath("ip")
	if err != nil {
		return errors.New("vip: the ip command is not installed (iproute2)")
	}
	out, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Operation not permitted") {
			return errors.New("vip: BitVoice may not change network addresses: give it CAP_NET_ADMIN (systemd: AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW)")
		}
		return errors.New("vip: ip " + strings.Join(args, " ") + ": " + msg)
	}
	return nil
}

const ethPARP = 0x0806

// frame is a gratuitous ARP packet: op 1 a request, 2 a reply.
func frame(mac net.HardwareAddr, ip netip.Addr, op uint16) []byte {
	b := make([]byte, 42)
	copy(b[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(b[6:12], mac)
	binary.BigEndian.PutUint16(b[12:14], ethPARP)
	binary.BigEndian.PutUint16(b[14:16], 1)      // Ethernet
	binary.BigEndian.PutUint16(b[16:18], 0x0800) // IPv4
	b[18], b[19] = 6, 4
	binary.BigEndian.PutUint16(b[20:22], op)
	a := ip.As4()
	copy(b[22:28], mac)
	copy(b[28:32], a[:])
	if op == 2 {
		copy(b[32:38], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	}
	copy(b[38:42], a[:]) // gratuitous: the target address is the address itself
	return b
}
