package pbx

import (
	"bytes"
	"crypto/rand"
	"errors"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/srtp"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Secure media (SRTP with SDES keys, RFC 4568). Each leg has its own keys: BitVoice decrypts what one
// party sends and encrypts it again for the other, so an encrypted phone can talk to a plain trunk.

var errNoSuite = errors.New("no SRTP crypto suite in common")

// supportedCrypto picks the first offered crypto line BitVoice can use.
func supportedCrypto(m *sdp.Media) (sdp.Crypto, bool) {
	for _, c := range m.Cryptos() {
		if srtp.TagLen(c.Suite) > 0 && len(c.Key) == 30 {
			return c, true
		}
	}
	return sdp.Crypto{}, false
}

// offerAcceptable reports whether BitVoice can answer an offer: plain RTP, or SRTP with a suite it knows.
func offerAcceptable(s *sdp.Session) bool {
	a := s.Audio()
	if a == nil || !a.Secure() {
		return true
	}
	_, ok := supportedCrypto(a)
	return ok
}

func newMasterKey() []byte {
	k := make([]byte, 30)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

// secure sets the security of a description BitVoice sends this party. With offer set, the description
// answers it: SRTP when the offer was SRTP. Without, it is an offer: SRTP when the party is configured for it.
// Called without l.mu held.
func (l *Leg) secure(desc, offer *sdp.Session) error {
	a := desc.Audio()
	if a == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if offer != nil {
		oa := offer.Audio()
		if oa == nil || !oa.Secure() {
			a.Proto = "RTP/AVP"
			a.SetCrypto(nil)
			l.setKeys(nil, "", nil)
			return nil
		}
		c, ok := supportedCrypto(oa)
		if !ok {
			return errNoSuite
		}
		if l.localKey == nil || l.suite != c.Suite {
			l.localKey = newMasterKey()
		}
		a.Proto = oa.Proto
		a.SetCrypto([]sdp.Crypto{{Tag: c.Tag, Suite: c.Suite, Key: l.localKey}})
		l.setKeys(c.Key, c.Suite, l.localKey)
		return nil
	}
	if !l.wantSRTP {
		a.Proto = "RTP/AVP"
		a.SetCrypto(nil)
		return nil
	}
	if l.localKey == nil {
		l.localKey, l.suite = newMasterKey(), srtp.AES128SHA80
	}
	a.Proto = "RTP/SAVP"
	a.SetCrypto([]sdp.Crypto{{Tag: 1, Suite: srtp.AES128SHA80, Key: l.localKey}})
	return nil
}

// refusesPlain reports whether an answer must be refused: this party has to encrypt (Require
// encryption) and the answer has no SRTP BitVoice can use.
func (l *Leg) refusesPlain(ans *sdp.Session) bool {
	if !l.needSRTP {
		return false
	}
	a := ans.Audio()
	if a == nil || !a.Secure() {
		return true
	}
	_, ok := supportedCrypto(a)
	return !ok
}

// plainOffer reports whether an offer (or a call with none) would leave the sound unencrypted.
func plainOffer(offer *sdp.Session) bool {
	if offer == nil {
		return true
	}
	a := offer.Audio()
	return a != nil && !a.Secure() // text-only (MSRP) calls have no sound to encrypt
}

// answered applies the keys in an answer to BitVoice's offer.
func (l *Leg) answeredCrypto(ans *sdp.Session) {
	l.videoRemote(ans, true)
	a := ans.Audio()
	if a == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !a.Secure() || l.localKey == nil {
		l.setKeys(nil, "", nil)
		return
	}
	if c, ok := supportedCrypto(a); ok {
		l.setKeys(c.Key, c.Suite, l.localKey)
	}
}

// setKeys installs SRTP contexts when the keys change; unchanged keys keep the running contexts (their
// rollover counters and replay windows). Called with l.mu held.
func (l *Leg) setKeys(remote []byte, suite string, local []byte) {
	if remote == nil {
		if l.remoteKey != nil {
			l.ep.SetSRTP(nil, nil)
		}
		l.remoteKey, l.suite = nil, ""
		return
	}
	if bytes.Equal(remote, l.remoteKey) && suite == l.suite && l.ep.Secure() {
		return
	}
	in, err1 := srtp.New(suite, remote[:16], remote[16:])
	out, err2 := srtp.New(suite, local[:16], local[16:])
	if err1 != nil || err2 != nil {
		return
	}
	l.remoteKey, l.suite = remote, suite
	l.ep.SetSRTP(in, out)
}

// trunkName is a trunk's name for messages, or "The provider" when it is no longer configured.
func (e *Engine) trunkName(id string) string {
	if t := e.Config().FindTrunk(id); t != nil {
		return t.Name
	}
	return "The provider"
}

// silentSides lists the parties of an answered call that sent no sound the whole call long. A firewall
// or NAT that lets SIP through and keeps the media out is nearly always why; it is easy to miss, because
// the call itself connects. Parties that said they would send nothing (a paging speaker, a call on hold),
// faxes over T.38, text-only calls and calls too short to judge are left out.
func (e *Engine) silentSides(info CallInfo, c *Call, end time.Time) []store.Side {
	c.mu.Lock()
	answer, textOnly, paging := c.answer, c.textOnly, c.paging != nil
	c.mu.Unlock()
	after := e.noSoundAfter
	if after == 0 {
		after = 5 * time.Second
	}
	if answer.IsZero() || textOnly || paging || end.Sub(answer) < after {
		return nil
	}
	cfg := e.Config()
	var out []store.Side
	for _, l := range info.Legs {
		if l.Media.Packets > 0 || l.Ringing || l.OnHold || l.Fax || l.silentByChoice || l.Ext == "" && l.Trunk == "" {
			continue
		}
		s := store.Side{Ext: l.Ext, Trunk: l.Trunk, Name: l.Party}
		if t := cfg.FindTrunk(l.Trunk); t != nil {
			s.Name = t.Name
		}
		out = append(out, s)
	}
	return out
}
