package pbx

import (
	"crypto/subtle"
	"net/netip"
	"strings"
	"unicode"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// SetLinkToken sets the pass the web gateway puts on calls from call links (a secret of this run).
func (e *Engine) SetLinkToken(token string) { e.linkToken.Store(token) }

// linkCall finds the call link an INVITE from the web gateway came through: it carries the pass and
// the link's ID, and comes from this computer. nil when it is not one.
func (e *Engine) linkCall(cfg *config.Config, m *sip.Message, ip netip.Addr) *config.CallLink {
	h := m.Get("X-BitVoice-Link")
	if h == "" {
		return nil
	}
	tok, _ := e.linkToken.Load().(string)
	pass, id, ok := strings.Cut(h, ";")
	if tok == "" || !ok || !(ip.IsLoopback() || ip == e.LocalSIP().Addr()) || subtle.ConstantTimeCompare([]byte(pass), []byte(tok)) != 1 {
		return nil
	}
	for i := range cfg.System.CallLinks {
		if l := &cfg.System.CallLinks[i]; l.ID == id && l.Enabled {
			return l
		}
	}
	return nil
}

// guestName keeps what a web caller typed as their name printable and short.
func guestName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '"' && r != '<' && r != '>' && r != '\\' {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40])
	}
	return s
}

func firstNonEmptyStr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
