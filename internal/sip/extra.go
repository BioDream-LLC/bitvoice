package sip

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// An extra address is one more address the stack answers on, such as an address shared by several
// servers and held by one at a time. It may be bound before this machine holds it (Linux: freebind),
// so that it starts answering the moment the address arrives. A request that arrives on it is
// answered from it, and everything sent to that peer afterwards leaves from it, so phones and
// providers always see the address they used.

type extraAddr struct {
	addr netip.Addr
	udp  *net.UDPConn
	tcp  net.Listener
}

// ListenExtra opens UDP and TCP on addr at the stack's own port. Call after Listen, before Serve.
func (s *Stack) ListenExtra(addr netip.Addr) error {
	if s.udp == nil {
		return errors.New("sip: ListenExtra before Listen")
	}
	hp := net.JoinHostPort(addr.String(), strconv.Itoa(s.udpPort))
	u, err := listenFreeUDP(hp)
	if err != nil {
		return fmt.Errorf("sip: listen udp %s: %w", hp, err)
	}
	t, err := listenFreeTCP(hp)
	if err != nil {
		u.Close()
		return fmt.Errorf("sip: listen tcp %s: %w", hp, err)
	}
	s.mu.Lock()
	if s.extra == nil {
		s.extra = map[netip.Addr]*extraAddr{}
	}
	s.extra[addr] = &extraAddr{addr: addr, udp: u, tcp: t}
	s.mu.Unlock()
	return nil
}

func (s *Stack) serveExtra() {
	s.mu.Lock()
	var xs []*extraAddr
	for _, x := range s.extra {
		xs = append(xs, x)
	}
	s.mu.Unlock()
	for _, x := range xs {
		s.wg.Add(2)
		go s.readUDPOn(x.udp)
		go s.accept(x.tcp, "TCP")
	}
}

// udpFor is the socket to send to a peer from: the extra address it used, or the main socket.
func (s *Stack) udpFor(to Peer) *net.UDPConn {
	if to.Local.IsValid() {
		s.mu.Lock()
		x := s.extra[to.Local.Unmap()]
		s.mu.Unlock()
		if x != nil {
			return x.udp
		}
	}
	return s.udp
}

func (s *Stack) closeExtra() {
	s.mu.Lock()
	xs := s.extra
	s.mu.Unlock()
	for _, x := range xs {
		x.udp.Close()
		x.tcp.Close()
	}
}
