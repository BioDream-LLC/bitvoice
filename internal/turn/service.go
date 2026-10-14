package turn

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
)

// Settings say which ports the service listens on.
type Settings struct {
	Enabled            bool
	Port, TLSPort      int // TLSPort -1 for none
	RelayMin, RelayMax int
	Secret             string
}

// Service runs a Server on its UDP, TCP and TLS ports, reopening them when the settings change.
type Service struct {
	Server *Server
	// TLS is the web interface's TLS configuration (its certificate), for TURN over TLS.
	TLS *tls.Config

	mu     sync.Mutex
	cur    Settings
	closer []func() error
	errs   []string
	secret []byte
}

// Apply opens, moves or closes the ports.
func (v *Service) Apply(s Settings) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secret = []byte(s.Secret)
	if v.Server.Secret == nil {
		v.Server.Secret = func() []byte { v.mu.Lock(); defer v.mu.Unlock(); return v.secret }
	}
	v.Server.PortMin, v.Server.PortMax = s.RelayMin, s.RelayMax
	same := s.Enabled == v.cur.Enabled && s.Port == v.cur.Port && s.TLSPort == v.cur.TLSPort
	v.cur = s
	if same && (len(v.closer) > 0 || !s.Enabled) {
		return
	}
	for _, c := range v.closer {
		_ = c()
	}
	v.closer, v.errs = nil, nil
	if !s.Enabled {
		return
	}
	addr := ":" + strconv.Itoa(s.Port)
	if pc, err := net.ListenPacket("udp", addr); err == nil {
		v.closer = append(v.closer, pc.Close)
		go func() { _ = v.Server.ServeUDP(pc) }()
	} else {
		v.errs = append(v.errs, fmt.Sprintf("UDP port %d: %v", s.Port, err))
	}
	if ln, err := net.Listen("tcp", addr); err == nil {
		v.closer = append(v.closer, ln.Close)
		go func() { _ = v.Server.ServeStream(ln, "tcp") }()
	} else {
		v.errs = append(v.errs, fmt.Sprintf("TCP port %d: %v", s.Port, err))
	}
	if s.TLSPort > 0 && v.TLS != nil {
		if ln, err := tls.Listen("tcp", ":"+strconv.Itoa(s.TLSPort), v.TLS); err == nil {
			v.closer = append(v.closer, ln.Close)
			go func() { _ = v.Server.ServeStream(ln, "tls") }()
		} else {
			v.errs = append(v.errs, fmt.Sprintf("TLS port %d: %v", s.TLSPort, err))
		}
	}
}

// Status says whether it runs, on which ports, how many relays are open and what failed.
func (v *Service) Status() map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	return map[string]any{"enabled": v.cur.Enabled, "running": v.cur.Enabled && len(v.closer) > 0,
		"port": v.cur.Port, "tlsPort": v.cur.TLSPort, "relays": v.Server.Count(), "problems": append([]string{}, v.errs...)}
}

// Close closes the ports.
func (v *Service) Close() {
	v.Apply(Settings{})
}

// LocalRoute is a Server.Route for this computer: its own addresses are relayed to as they are, and
// its public address (behind a NAT) is reached at loopback.
func LocalRoute(public func() netip.Addr) func(netip.Addr) (netip.Addr, bool) {
	return func(a netip.Addr) (netip.Addr, bool) {
		if a.IsLoopback() {
			return a, true
		}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, x := range addrs {
				if p, err := netip.ParsePrefix(x.String()); err == nil && p.Addr().Unmap() == a {
					return a, true
				}
			}
		}
		if p := public(); p.IsValid() && p == a {
			if a.Is6() {
				return netip.IPv6Loopback(), true
			}
			return netip.MustParseAddr("127.0.0.1"), true
		}
		return a, false
	}
}
