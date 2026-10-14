package pbx

import (
	"crypto/sha256"
	"slices"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/screen"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// signers caches each trunk's STIR/SHAKEN signer, made again when its key, certificate or address change.
type signers struct {
	mu sync.Mutex
	m  map[string]signerEntry
}

type signerEntry struct {
	sum [32]byte
	s   *screen.Signer
	err error
}

// ShakenX5U is where a trunk's certificate is published when its settings name no address: this
// server's own web address, set by the web server (empty until it knows it).
func (e *Engine) shakenX5U(cfg *config.Config, t *config.Trunk) string {
	if t.Shaken.X5U != "" {
		return t.Shaken.X5U
	}
	if e.WebBase == nil {
		return ""
	}
	if b := e.WebBase(); b != "" {
		return b + "/stir/" + t.ID + ".pem"
	}
	return ""
}

func (e *Engine) signerFor(cfg *config.Config, t *config.Trunk) (*screen.Signer, error) {
	x5u := e.shakenX5U(cfg, t)
	sum := sha256.Sum256([]byte(t.Shaken.Key + "\x00" + t.Shaken.Cert + "\x00" + x5u))
	e.sign.mu.Lock()
	defer e.sign.mu.Unlock()
	if en, ok := e.sign.m[t.ID]; ok && en.sum == sum {
		return en.s, en.err
	}
	s, err := screen.NewSigner(t.Shaken.Key, t.Shaken.Cert, x5u)
	if e.sign.m == nil {
		e.sign.m = map[string]signerEntry{}
	}
	e.sign.m[t.ID] = signerEntry{sum, s, err}
	return s, err
}

// ownNumber says whether a number is one of this system's: a number routed in, or a caller ID set on a
// trunk or an extension by an administrator.
func ownNumber(cfg *config.Config, n string) bool {
	if n == "" {
		return false
	}
	match := func(m string) bool { return m != "" && sameNumber(m, n) > 0 }
	return slices.ContainsFunc(cfg.InboundRoutes, func(r config.InboundRoute) bool { return match(r.DID) }) ||
		slices.ContainsFunc(cfg.Trunks, func(t config.Trunk) bool { return match(t.CallerID) }) ||
		slices.ContainsFunc(cfg.Extensions, func(x config.Extension) bool { return match(x.CallerID) })
}

// attestation is the SHAKEN attestation for a call out: what the trunk says, or A for this system's
// own numbers from its own extensions, B for other numbers from its extensions, and C for calls it
// passes on from outside (a forward, a transfer of an outside caller).
func attestation(cfg *config.Config, t *config.Trunk, fromExt bool, cid string) string {
	if t.Shaken.Attest != "" {
		return t.Shaken.Attest
	}
	switch {
	case !fromExt:
		return "C"
	case ownNumber(cfg, cid):
		return "A"
	}
	return "B"
}

// signCall adds the Identity header to a call out through a trunk that signs. Emergency calls are
// signed too: SHAKEN applies to them, and a failure to sign never stops a call.
func (c *Call) signCall(cfg *config.Config, t *config.Trunk, inv *sip.Message, cid, to string) {
	if t == nil || !t.Shaken.Sign {
		return
	}
	s, err := c.e.signerFor(cfg, t)
	if err != nil || s == nil {
		c.e.Log.Warn("STIR/SHAKEN: call not signed", "trunk", t.Name, "err", err)
		return
	}
	c.mu.Lock()
	fromExt := c.callerExt != ""
	c.mu.Unlock()
	att := attestation(cfg, t, fromExt, cid)
	id, err := s.Identity(cid, to, att, t.Shaken.OrigID, time.Now())
	if err != nil {
		c.e.Log.Warn("STIR/SHAKEN: call not signed", "trunk", t.Name, "err", err)
		return
	}
	inv.Set("Identity", id)
	c.mu.Lock()
	c.signed = att
	c.mu.Unlock()
}
