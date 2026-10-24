package webrtc

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/ice"
)

var resolved struct {
	sync.Mutex
	at  map[string]time.Time
	ips map[string]netip.Addr
}

// resolve finds a STUN or TURN server's address, kept a minute (a call never waits long on DNS).
func resolve(host string, port int) (netip.AddrPort, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(ip.Unmap(), uint16(port)), true
	}
	resolved.Lock()
	if resolved.at == nil {
		resolved.at, resolved.ips = map[string]time.Time{}, map[string]netip.Addr{}
	}
	ip, ok := resolved.ips[host]
	fresh := ok && time.Since(resolved.at[host]) < time.Minute
	resolved.Unlock()
	if !fresh {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if err != nil || len(addrs) == 0 {
			return netip.AddrPort{}, ok // the last known address, if any
		}
		ip = addrs[0].Unmap()
		resolved.Lock()
		resolved.ips[host], resolved.at[host] = ip, time.Now()
		resolved.Unlock()
	}
	return netip.AddrPortFrom(ip, uint16(port)), true
}

// localAddrs are the addresses offered as host candidates: this computer's own (not loopback or
// link-local), or loopback alone when the browser reached BitVoice on it.
func localAddrs(browserHost string) []netip.Addr {
	if ip, err := netip.ParseAddr(browserHost); err == nil && ip.IsLoopback() {
		return []netip.Addr{ip.Unmap()}
	}
	var out []netip.Addr
	ifaces, _ := net.InterfaceAddrs()
	for _, a := range ifaces {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := p.Addr().Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, ip)
	}
	if ip, err := netip.ParseAddr(browserHost); err == nil {
		ip = ip.Unmap()
		found := false
		for _, x := range out {
			found = found || x == ip
		}
		if !found {
			out = append([]netip.Addr{ip}, out...)
		}
	}
	return out
}

// OptionsFrom turns the settings into ICE options for a session with a browser that reached BitVoice
// at browserHost. Servers that cannot be resolved are left out.
func OptionsFrom(c config.ICE, browserHost string, peer ...netip.Addr) ICEOptions {
	if !c.Full {
		return ICEOptions{}
	}
	o := ICEOptions{Full: true, Hosts: localAddrs(browserHost), RelayOnly: c.RelayOnly}
	if len(peer) > 0 {
		o.Peer = peer[0]
	}
	for _, s := range c.STUN {
		if h, p, err := config.ICEHostPort(s); err == nil {
			if a, ok := resolve(h, p); ok {
				o.STUN = append(o.STUN, a)
			}
		}
	}
	for _, t := range c.TURN {
		if h, p, err := config.ICEHostPort(t.Server); err == nil {
			if a, ok := resolve(h, p); ok {
				o.TURN = append(o.TURN, ice.TURNServer{Addr: a, Username: t.Username, Password: t.Password})
			}
		}
	}
	return o
}

// Probe gathers candidates as a call would, without a call, to show on the settings screen what
// BitVoice will offer: its addresses, its address seen through each STUN server, its relays.
func Probe(c config.ICE, browserHost string, port func() (send func([]byte, netip.AddrPort), recv func(func([]byte, netip.AddrPort)), localPort int, done func())) []ice.Candidate {
	o := OptionsFrom(c, browserHost)
	o.Full = true
	send, recv, lp, done := port()
	defer done()
	ag := ice.New(ice.Config{Hosts: o.Hosts, Port: lp, STUN: o.STUN, TURN: o.TURN, RelayOnly: o.RelayOnly,
		LocalUfrag: "probe", LocalPwd: "probe-password-" + strconv.Itoa(lp), Send: send})
	defer ag.Close()
	recv(func(b []byte, from netip.AddrPort) { ag.Handle(b, from) })
	return ag.Gather()
}
