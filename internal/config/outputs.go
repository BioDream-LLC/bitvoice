package config

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// SNMP is the read-only SNMPv2c agent for monitoring systems (Nagios, Zabbix, PRTG, LibreNMS), and
// the traps it sends.
type SNMP struct {
	Enabled bool `json:"enabled"`
	// Listen is the UDP address (":161" when empty).
	Listen string `json:"listen"`
	// Community is the v2c community string; AllowFrom the networks that may ask (required: the
	// community crosses the network in clear).
	Community string   `json:"community"`
	AllowFrom []string `json:"allowFrom"`
	// BaseOID is where BitVoice's own values are, under an enterprise number. BioDream LLC has none
	// yet, so the default is in Net-SNMP's experimental space (1.3.6.1.4.1.8072.9999.9999.7); use your
	// own enterprise's number to keep it stable in your monitoring.
	BaseOID  string `json:"baseOid"`
	Contact  string `json:"contact"`
	Location string `json:"location"`
	// Traps go to these managers when a trunk goes down or comes back, an emergency call is made, or
	// a server of a cluster stops answering.
	Traps []SNMPTrap `json:"traps"`
}

// SNMPTrap is a manager that receives traps.
type SNMPTrap struct {
	Host      string `json:"host"` // host or host:port (162 when no port)
	Community string `json:"community"`
}

// DefaultSNMPBase is BitVoice's base OID until BioDream LLC has an enterprise number.
const DefaultSNMPBase = "1.3.6.1.4.1.8072.9999.9999.7"

// CDROutputs are more places each call record goes as a call ends, beside Call history.
type CDROutputs struct {
	Syslog   CDRSyslog   `json:"syslog"`
	CSV      bool        `json:"csv"` // a CSV file a day, downloadable from Call history
	Postgres CDRPostgres `json:"postgres"`
}

// CDRSyslog sends each record as a syslog message (RFC 5424).
type CDRSyslog struct {
	Host      string `json:"host"` // empty: off
	Port      int    `json:"port"` // 514 (udp, tcp) or 6514 (tls) when 0
	Transport string `json:"transport"`
	Facility  int    `json:"facility"` // 16 (local0) to 23 (local7); 16 when 0
}

// CDRPostgres writes each record into a table of another PostgreSQL database, with Asterisk's cdr
// columns (calldate, clid, src, dst, duration, billsec, disposition, uniqueid ...), so the reports and
// billing tools made for Asterisk read it.
type CDRPostgres struct {
	URL   string `json:"url"` // postgres://user:password@host/db; empty: off
	Table string `json:"table"`
}

var tableRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func (c *Config) validateOutputs(p *Problems) {
	s := &c.System.SNMP
	if s.Enabled {
		if s.Listen == "" {
			s.Listen = ":161"
		}
		if _, port, err := net.SplitHostPort(s.Listen); err != nil {
			p.add("system.snmp.listen", "use an address and port such as :161")
		} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			p.add("system.snmp.listen", "use an address and port such as :161")
		}
		if len(s.Community) < 6 || strings.ContainsAny(s.Community, " \t") {
			p.add("system.snmp.community", "use a community of at least 6 characters, not public")
		} else if strings.EqualFold(s.Community, "public") || strings.EqualFold(s.Community, "private") {
			p.add("system.snmp.community", "public and private are the first guesses: choose another")
		}
		if len(s.AllowFrom) == 0 {
			p.add("system.snmp.allowFrom", "list the networks your monitoring asks from")
		}
		for _, a := range s.AllowFrom {
			if _, err := netip.ParsePrefix(a); err != nil {
				if _, err := netip.ParseAddr(a); err != nil {
					p.add("system.snmp.allowFrom", "%q is not an address or network such as 10.0.0.0/24", a)
				}
			}
		}
	}
	if s.BaseOID == "" {
		s.BaseOID = DefaultSNMPBase
	} else if !regexp.MustCompile(`^1\.3\.6\.1\.4\.1(\.[0-9]+)+$`).MatchString(strings.TrimPrefix(s.BaseOID, ".")) {
		p.add("system.snmp.baseOid", "use an OID under 1.3.6.1.4.1 (an enterprise number)")
	}
	for i, t := range s.Traps {
		f := fmt.Sprintf("system.snmp.traps[%d]", i)
		h := t.Host
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		if _, err := netip.ParseAddr(h); err != nil && !hostRe.MatchString(h) {
			p.add(f+".host", "enter the manager's address")
		}
		if t.Community == "" {
			p.add(f+".community", "enter the trap community")
		}
	}
	o := &c.System.CDROutputs
	if sl := &o.Syslog; sl.Host != "" {
		if _, err := netip.ParseAddr(sl.Host); err != nil && !hostRe.MatchString(sl.Host) {
			p.add("system.cdrOutputs.syslog.host", "enter the syslog server's address")
		}
		switch sl.Transport {
		case "":
			sl.Transport = "udp"
		case "udp", "tcp", "tls":
		default:
			p.add("system.cdrOutputs.syslog.transport", "choose udp, tcp or tls")
		}
		if sl.Port < 0 || sl.Port > 65535 {
			p.add("system.cdrOutputs.syslog.port", "a port is 1 to 65535")
		}
		if sl.Facility == 0 {
			sl.Facility = 16
		}
		if sl.Facility < 16 || sl.Facility > 23 {
			p.add("system.cdrOutputs.syslog.facility", "choose local0 to local7")
		}
	}
	if pg := &o.Postgres; pg.URL != "" {
		if !strings.HasPrefix(pg.URL, "postgres://") && !strings.HasPrefix(pg.URL, "postgresql://") {
			p.add("system.cdrOutputs.postgres.url", "use a postgres:// address")
		}
		if pg.Table == "" {
			pg.Table = "cdr"
		}
		if !tableRe.MatchString(pg.Table) {
			p.add("system.cdrOutputs.postgres.table", "use a table name of lower-case letters, digits and _")
		}
	}
}
