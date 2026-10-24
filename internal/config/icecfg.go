package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ICE is how BitVoice itself connects browser media when it does not have a public address of its
// own (the server behind a NAT router without port forwarding, in an office or a cloud network).
// Off, BitVoice is ICE-lite: browsers reach it at the one address they used for the web interface.
type ICE struct {
	// Full makes BitVoice a full ICE agent (RFC 8445): it offers its own addresses, its address as
	// seen through the STUN servers, and relays on the TURN servers, and checks each with the browser.
	Full bool `json:"full"`
	// STUN servers, host:port (port 3478 when none is given).
	STUN []string `json:"stun,omitempty"`
	// TURN servers BitVoice relays its media through when nothing else gets through (UDP).
	TURN []ICERelay `json:"turn,omitempty"`
	// RelayOnly sends all browser media through the TURN servers (a policy some networks require).
	RelayOnly bool `json:"relayOnly,omitempty"`
}

// ICERelay is a TURN server BitVoice uses as a client.
type ICERelay struct {
	// Server is host:port (3478 when none is given); a turn: URL is taken too.
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// HostPort reads a STUN or TURN server as host and port: host, host:port, stun:host:port,
// turn:host:port?transport=udp.
func ICEHostPort(v string) (string, int, error) {
	v = strings.TrimSpace(v)
	for _, p := range []string{"stun:", "turn:", "stuns:", "turns:"} {
		if strings.HasPrefix(strings.ToLower(v), p) {
			if strings.HasSuffix(p, "s:") {
				return "", 0, fmt.Errorf("%s: BitVoice reaches STUN and TURN servers over UDP; give the server's UDP address", v)
			}
			v = v[len(p):]
		}
	}
	if i := strings.IndexByte(v, '?'); i >= 0 {
		if q := strings.ToLower(v[i+1:]); strings.Contains(q, "transport=tcp") {
			return "", 0, fmt.Errorf("%s: BitVoice reaches TURN servers over UDP", v)
		}
		v = v[:i]
	}
	host, port := v, 3478
	if h, p, err := net.SplitHostPort(v); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return "", 0, fmt.Errorf("%s: a bad port", v)
		}
		host, port = h, n
	}
	if host == "" || strings.ContainsAny(host, " /@") {
		return "", 0, fmt.Errorf("%s: give host:port", v)
	}
	return host, port, nil
}

func (c *Config) validateICE(p *Problems) {
	ic := &c.System.ICE
	for i := range ic.STUN {
		ic.STUN[i] = strings.TrimSpace(ic.STUN[i])
		if _, _, err := ICEHostPort(ic.STUN[i]); err != nil {
			p.add(fmt.Sprintf("system.ice.stun[%d]", i), "%s", err.Error())
		}
	}
	for i := range ic.TURN {
		t := &ic.TURN[i]
		f := fmt.Sprintf("system.ice.turn[%d]", i)
		t.Server, t.Username = strings.TrimSpace(t.Server), strings.TrimSpace(t.Username)
		if _, _, err := ICEHostPort(t.Server); err != nil {
			p.add(f+".server", "%s", err.Error())
		}
		if t.Username == "" {
			p.add(f+".username", "TURN servers need a username")
		}
		if t.Password == "" {
			p.add(f+".password", "and its password")
		}
	}
	if !ic.Full {
		return
	}
	if len(ic.STUN) == 0 && len(ic.TURN) == 0 {
		p.add("system.ice.stun", "give a STUN or a TURN server: behind NAT, BitVoice needs one to learn its public address")
	}
	if ic.RelayOnly && len(ic.TURN) == 0 {
		p.add("system.ice.relayOnly", "relaying every call needs a TURN server")
	}
}
