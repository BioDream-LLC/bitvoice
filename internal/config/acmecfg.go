package config

import (
	"net/mail"
	"net/url"
	"slices"
	"strings"
)

// ACME gets the web interface's certificate from Let's Encrypt (or another ACME authority) and renews
// it: the names must reach this server from the internet, on port 80 (http-01) or on the web
// interface's port when that is 443 (tls-alpn-01).
type ACME struct {
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains"`
	Email   string   `json:"email"` // where the authority writes about expiry and problems
	// Directory is "" for Let's Encrypt, "staging" for its test service, or another authority's
	// directory URL.
	Directory string `json:"directory"`
	// Challenge is "" to choose (tls-alpn-01 on port 443, http-01 otherwise), http-01 or tls-alpn-01.
	Challenge string `json:"challenge"`
	// HTTPPort answers http-01 (default 80); the web interface's own port answers it too, for a router
	// that forwards port 80 there.
	HTTPPort int `json:"httpPort"`
}

func (c *Config) validateACME(p *Problems) {
	a := &c.System.ACME
	out := a.Domains[:0]
	for _, d := range a.Domains {
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if !hostRe.MatchString(d) || !strings.Contains(d, ".") || strings.HasPrefix(d, "*.") {
			p.add("system.acme.domains", "%q is not a domain name this server can prove (no wildcards, no addresses)", d)
		}
		out = append(out, d)
	}
	a.Domains = out
	if len(a.Domains) > 20 {
		p.add("system.acme.domains", "at most 20 names")
	}
	if a.Enabled && len(a.Domains) == 0 {
		p.add("system.acme.domains", "enter the name this server is reached by, such as pbx.example.com")
	}
	a.Email = strings.TrimSpace(a.Email)
	if a.Email != "" {
		if m, err := mail.ParseAddress(a.Email); err != nil || m.Address != a.Email {
			p.add("system.acme.email", "enter an email address, or leave it empty")
		}
	}
	switch a.Directory = strings.TrimSpace(a.Directory); a.Directory {
	case "", "staging":
	default:
		if u, err := url.Parse(a.Directory); err != nil || u.Scheme != "https" || u.Host == "" {
			p.add("system.acme.directory", "leave empty for Let's Encrypt, or enter the authority's https:// directory address")
		}
	}
	switch a.Challenge {
	case "", "http-01", "tls-alpn-01":
	default:
		p.add("system.acme.challenge", "choose http-01 or tls-alpn-01, or leave it to BitVoice")
	}
	if a.HTTPPort < 0 || a.HTTPPort > 65535 {
		p.add("system.acme.httpPort", "a port is 1 to 65535; 0 for 80")
	}
}
