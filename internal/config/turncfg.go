package config

// TURN is BitVoice's own TURN server (RFC 8656), for browser phones and WebRTC softphones on
// networks that block UDP or sit behind strict NATs: it relays to this server's own addresses only.
type TURN struct {
	Enabled bool `json:"enabled"`
	// Port is UDP and TCP (default 3478); TLSPort TURN over TLS with the web interface's certificate
	// (default 5349; -1 for none).
	Port    int `json:"port"`
	TLSPort int `json:"tlsPort"`
	// RelayMin and RelayMax are the UDP ports relays use (default 49152 to 49407).
	RelayMin int `json:"relayMin"`
	RelayMax int `json:"relayMax"`
	// Secret makes the short-lived credentials browsers are given; made when TURN is switched on.
	Secret string `json:"secret"`
}

// Default TURN ports.
const (
	DefaultTURNPort    = 3478
	DefaultTURNTLSPort = 5349
	DefaultRelayMin    = 49152
	DefaultRelayMax    = 49407
)

func (c *Config) validateTURN(p *Problems) {
	t := &c.System.TURN
	if t.Port == 0 {
		t.Port = DefaultTURNPort
	}
	if t.TLSPort == 0 {
		t.TLSPort = DefaultTURNTLSPort
	}
	if t.RelayMin == 0 && t.RelayMax == 0 {
		t.RelayMin, t.RelayMax = DefaultRelayMin, DefaultRelayMax
	}
	if t.Port < 1 || t.Port > 65535 {
		p.add("system.turn.port", "a port is 1 to 65535")
	}
	if t.TLSPort != -1 && (t.TLSPort < 1 || t.TLSPort > 65535 || t.TLSPort == t.Port) {
		p.add("system.turn.tlsPort", "a port is 1 to 65535, not the TURN port itself; -1 for no TLS")
	}
	if t.RelayMin < 1024 || t.RelayMax > 65535 || t.RelayMax < t.RelayMin || t.RelayMax-t.RelayMin < 15 {
		p.add("system.turn.relayMin", "relay ports are a range of at least 16 within 1024 to 65535")
	} else if t.RelayMin <= c.System.RTPMax && t.RelayMax >= c.System.RTPMin && c.System.RTPMin > 0 {
		p.add("system.turn.relayMin", "the relay ports overlap the media ports (%d to %d)", c.System.RTPMin, c.System.RTPMax)
	}
	if t.Enabled && t.Secret == "" {
		t.Secret = NewID() + NewID() + NewID()
	}
	if t.Secret != "" && len(t.Secret) < 24 {
		p.add("system.turn.secret", "the secret is at least 24 characters; empty makes one")
	}
}
