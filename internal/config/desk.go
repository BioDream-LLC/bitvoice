package config

import (
	"fmt"
	"regexp"
	"strings"
)

// DeskProtocols are the older phone protocols BitVoice speaks beside SIP, so phones and gateways
// that cannot do SIP keep working: Cisco's Skinny (SCCP) desk phones and MGCP gateways (analogue
// ports of ATAs and line cards). Each phone or line is one more device of an extension.
type DeskProtocols struct {
	SCCP SCCP `json:"sccp"`
	MGCP MGCP `json:"mgcp"`
}

// SCCP lets Cisco desk phones running SCCP (Skinny) firmware register here, on TCP port 2000.
type SCCP struct {
	Enabled bool `json:"enabled"`
	// Port is the TCP port phones connect to (2000 when 0).
	Port   int         `json:"port"`
	Phones []SCCPPhone `json:"phones"`
}

// SCCPPhone is one Skinny phone: its device name (SEP and its MAC address) and the extension its
// first line rings.
type SCCPPhone struct {
	Device    string `json:"device"`
	Extension string `json:"extension"`
}

// MGCP is the call agent for MGCP gateways (RFC 3435), on UDP port 2727; gateways listen on 2427.
type MGCP struct {
	Enabled bool `json:"enabled"`
	// Port is the call agent's UDP port (2727 when 0).
	Port     int           `json:"port"`
	Gateways []MGCPGateway `json:"gateways"`
}

// MGCPGateway is one gateway: the domain name it uses in its endpoint names (aaln/1@NAME), where it
// listens (its address; empty: where it last spoke from), and the extension of each line.
type MGCPGateway struct {
	Name  string     `json:"name"`
	Host  string     `json:"host"`
	Port  int        `json:"port"` // 2427 when 0
	Lines []MGCPLine `json:"lines"`
}

// MGCPLine is one endpoint of a gateway (aaln/1 is its first analogue port) and its extension.
type MGCPLine struct {
	Endpoint  string `json:"endpoint"`
	Extension string `json:"extension"`
}

var (
	sccpDeviceRe   = regexp.MustCompile(`^SEP[0-9A-F]{12}$`)
	mgcpEndpointRe = regexp.MustCompile(`^[A-Za-z0-9_/.\-*$]{1,64}$`)
)

func (c *Config) validateDesk(p *Problems) {
	d := &c.System.Desk
	ext := func(field, n string) {
		if n == "" {
			p.add(field, "choose the extension it belongs to")
		} else if c.FindExtension(n) == nil {
			p.add(field, "there is no extension %s", n)
		}
	}
	if d.SCCP.Port != 0 && (d.SCCP.Port < 1 || d.SCCP.Port > 65535) {
		p.add("system.desk.sccp.port", "use a port from 1 to 65535 (phones use 2000)")
	}
	seen := map[string]bool{}
	for i := range d.SCCP.Phones {
		ph := &d.SCCP.Phones[i]
		f := fmt.Sprintf("system.desk.sccp.phones[%d]", i)
		ph.Device = strings.ToUpper(strings.TrimSpace(ph.Device))
		if !strings.HasPrefix(ph.Device, "SEP") {
			ph.Device = "SEP" + strings.NewReplacer(":", "", "-", "", ".", "").Replace(ph.Device)
		}
		if !sccpDeviceRe.MatchString(ph.Device) {
			p.add(f+".device", "enter the phone's MAC address (12 hex digits) or its name, SEP and the MAC")
		} else if seen[ph.Device] {
			p.add(f+".device", "this phone is listed twice")
		}
		seen[ph.Device] = true
		ext(f+".extension", ph.Extension)
	}
	if d.MGCP.Port != 0 && (d.MGCP.Port < 1 || d.MGCP.Port > 65535) {
		p.add("system.desk.mgcp.port", "use a port from 1 to 65535 (call agents use 2727)")
	}
	names := map[string]bool{}
	for i := range d.MGCP.Gateways {
		g := &d.MGCP.Gateways[i]
		f := fmt.Sprintf("system.desk.mgcp.gateways[%d]", i)
		g.Name = strings.ToLower(strings.TrimSpace(g.Name))
		if g.Name == "" || !hostRe.MatchString(g.Name) {
			p.add(f+".name", "enter the gateway's domain name, as in its endpoint names (aaln/1@name)")
		} else if names[g.Name] {
			p.add(f+".name", "this gateway is listed twice")
		}
		names[g.Name] = true
		if g.Host != "" && !hostRe.MatchString(g.Host) {
			p.add(f+".host", "enter an address or host name, or leave it empty")
		}
		if g.Port != 0 && (g.Port < 1 || g.Port > 65535) {
			p.add(f+".port", "use a port from 1 to 65535 (gateways use 2427)")
		}
		if len(g.Lines) == 0 {
			p.add(f+".lines", "add at least one line")
		}
		eps := map[string]bool{}
		for j := range g.Lines {
			l := &g.Lines[j]
			lf := fmt.Sprintf("%s.lines[%d]", f, j)
			l.Endpoint = strings.ToLower(strings.TrimSpace(l.Endpoint))
			if !mgcpEndpointRe.MatchString(l.Endpoint) || strings.Contains(l.Endpoint, "@") {
				p.add(lf+".endpoint", "enter the endpoint's local name, such as aaln/1")
			} else if eps[l.Endpoint] {
				p.add(lf+".endpoint", "this endpoint is listed twice")
			}
			eps[l.Endpoint] = true
			ext(lf+".extension", l.Extension)
		}
	}
}
