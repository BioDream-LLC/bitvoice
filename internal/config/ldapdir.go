package config

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/ldap"
)

// LDAP reads the company directory (Active Directory, OpenLDAP and other LDAP servers): people
// with a phone number become extensions, groups fill ring groups, and people may sign in to the web
// interface with their directory password. BitVoice only reads the directory.
type LDAP struct {
	Enabled bool `json:"enabled"`
	// Kind is ad (Active Directory) or ldap (OpenLDAP and the rest): the defaults of the boxes below.
	Kind string `json:"kind"`
	// URL is ldaps://dc1.example.com (port 636) or ldap://... (389, with StartTLS).
	URL      string `json:"url"`
	StartTLS bool   `json:"startTls"`
	// CACert is the company's certificate authority (PEM), when the server's certificate is not
	// publicly trusted; empty uses the computer's trusted authorities.
	CACert string `json:"caCert,omitempty"`
	// BindDN and BindPassword are the account BitVoice reads the directory with.
	BindDN       string `json:"bindDn"`
	BindPassword string `json:"bindPassword"`
	// BaseDN is where people and groups are looked for (dc=example,dc=com).
	BaseDN string `json:"baseDn"`
	// UserFilter picks the people (RFC 4515), by default every person (Active Directory leaves out
	// disabled accounts too).
	UserFilter string `json:"userFilter"`
	// The attributes holding a person's sign-in name, name, email and phone number.
	LoginAttr  string `json:"loginAttr"`
	NameAttr   string `json:"nameAttr"`
	EmailAttr  string `json:"emailAttr"`
	NumberAttr string `json:"numberAttr"`
	// NumberDigits keeps the last digits of the number as the extension (4 of +1 212 555 0123 is 0123);
	// 0 keeps them all (2 to 8 digits).
	NumberDigits int `json:"numberDigits,omitempty"`
	// SyncExtensions makes and updates extensions for people with a number; DisableMissing turns off
	// the extensions made from the directory whose people are gone or disabled.
	SyncExtensions bool `json:"syncExtensions"`
	DisableMissing bool `json:"disableMissing"`
	// RingGroups fill ring groups with the extensions of a directory group's members.
	RingGroups []LDAPGroup `json:"ringGroups,omitempty"`
	// SyncMinutes is how often the directory is read (default 60).
	SyncMinutes int `json:"syncMinutes"`
	// SignIn lets people sign in to the web interface with their directory name and password:
	// members of AdminGroups as administrators, of ViewerGroups as viewers (anyone, with no viewer groups).
	SignIn       bool     `json:"signIn"`
	AdminGroups  []string `json:"adminGroups,omitempty"`
	ViewerGroups []string `json:"viewerGroups,omitempty"`
}

// LDAPGroup fills a ring group with a directory group's members.
type LDAPGroup struct {
	// Group is the group's DN or its name (cn).
	Group string `json:"group"`
	// RingGroup is the ring group's number.
	RingGroup string `json:"ringGroup"`
}

// LDAPDefaults are the boxes' defaults for a kind of directory.
func LDAPDefaults(kind string) (filter, login, name, email, number string) {
	if kind == "ad" {
		return "(&(objectCategory=person)(objectClass=user)(!(userAccountControl:1.2.840.113556.1.4.803:=2)))",
			"sAMAccountName", "displayName", "mail", "telephoneNumber"
	}
	return "(objectClass=inetOrgPerson)", "uid", "cn", "mail", "telephoneNumber"
}

func (c *Config) validateLDAP(p *Problems) {
	d := &c.System.LDAP
	d.URL, d.BindDN, d.BaseDN = strings.TrimSpace(d.URL), strings.TrimSpace(d.BindDN), strings.TrimSpace(d.BaseDN)
	if d.Kind != "ad" {
		d.Kind = "ldap"
	}
	f, login, name, email, number := LDAPDefaults(d.Kind)
	for _, x := range []struct {
		v   *string
		def string
	}{{&d.UserFilter, f}, {&d.LoginAttr, login}, {&d.NameAttr, name}, {&d.EmailAttr, email}, {&d.NumberAttr, number}} {
		if *x.v = strings.TrimSpace(*x.v); *x.v == "" {
			*x.v = x.def
		}
	}
	if d.SyncMinutes == 0 {
		d.SyncMinutes = 60
	}
	if !d.Enabled {
		return
	}
	u, err := url.Parse(d.URL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "ldap" && u.Scheme != "ldaps"):
		p.add("system.ldap.url", "use ldaps://server (or ldap://server with StartTLS)")
	case u.Scheme == "ldap" && !d.StartTLS && !loopbackHost(u.Hostname()):
		p.add("system.ldap.url", "passwords would cross the network unencrypted: use ldaps:// or switch StartTLS on")
	}
	if d.CACert != "" {
		pool := x509.NewCertPool()
		if b, _ := pem.Decode([]byte(d.CACert)); b == nil || !pool.AppendCertsFromPEM([]byte(d.CACert)) {
			p.add("system.ldap.caCert", "paste the certificate authority as PEM (-----BEGIN CERTIFICATE-----)")
		}
	}
	if d.BaseDN == "" {
		p.add("system.ldap.baseDn", "where people are, such as dc=example,dc=com")
	}
	if d.BindDN == "" {
		p.add("system.ldap.bindDn", "the account BitVoice reads the directory with (a DN, or user@domain on Active Directory)")
	}
	if d.BindPassword == "" {
		p.add("system.ldap.bindPassword", "the reading account's password")
	}
	if err := ldap.CheckFilter(d.UserFilter); err != nil {
		p.add("system.ldap.userFilter", "not a filter: write it as (&(objectClass=person)(department=Sales))")
	}
	if d.NumberDigits < 0 || d.NumberDigits > 8 || d.NumberDigits == 1 {
		p.add("system.ldap.numberDigits", "0 (every digit) or 2 to 8")
	}
	if d.SyncMinutes < 5 || d.SyncMinutes > 1440 {
		p.add("system.ldap.syncMinutes", "5 to 1440 minutes")
	}
	for i := range d.RingGroups {
		g := &d.RingGroups[i]
		g.Group, g.RingGroup = strings.TrimSpace(g.Group), strings.TrimSpace(g.RingGroup)
		f := fmt.Sprintf("system.ldap.ringGroups[%d]", i)
		if g.Group == "" {
			p.add(f+".group", "the group's name or DN")
		}
		if c.FindRingGroup(g.RingGroup) == nil {
			p.add(f+".ringGroup", "choose a ring group")
		}
	}
	if d.SignIn && len(d.AdminGroups) == 0 {
		p.add("system.ldap.adminGroups", "name at least one group whose members are administrators")
	}
}

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
