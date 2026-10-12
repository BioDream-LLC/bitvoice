package pbx

import (
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Fax between a fax gateway (an ATA with a fax machine, an analogue or ISDN gateway) and a provider,
// or between two gateways, goes over T.38 when both ends support it: when one side switches its call
// to T.38 (a re-INVITE with an image stream), BitVoice asks the other side to switch first, and relays
// the fax packets (UDPTL) between the two as they are, on ports of its own. When the other side
// cannot (it refuses), the first side is refused too and stays on G.711, which carries fax as sound.
// When the fax is done and one side switches back to sound, the other is switched back as well.

func imageMedia(s *sdp.Session) *sdp.Media {
	if s == nil {
		return nil
	}
	for _, m := range s.Media {
		if m.Type == "image" && m.Port != 0 {
			return m
		}
	}
	return nil
}

func (l *Leg) imageEP() *media.Endpoint {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.image
}

// imageEndpoint is this leg's fax port, opened on first use.
func (l *Leg) imageEndpoint() *media.Endpoint {
	if ep := l.imageEP(); ep != nil {
		return ep
	}
	host := ""
	if l.peer.Local.IsValid() {
		host = l.peer.Local.String()
	}
	ep, err := l.e.pool.AllocateOn(host)
	if err != nil {
		l.e.Log.Warn("no port for fax", "err", err)
		return nil
	}
	ep.SetRaw()
	l.mu.Lock()
	if l.image != nil || l.ended {
		l.mu.Unlock()
		ep.Close()
		return l.imageEP()
	}
	l.image = ep
	l.mu.Unlock()
	ep.Start()
	return ep
}

func (l *Leg) closeImage() {
	l.mu.Lock()
	ep := l.image
	l.image, l.onFax = nil, false
	l.mu.Unlock()
	if ep != nil {
		ep.Close()
	}
}

// faxDescription rewrites a description holding a T.38 stream for this party: BitVoice's address,
// this leg's fax port, sound refused, BitVoice's origin with a newer version.
func (l *Leg) faxDescription(from *sdp.Session, port int) *sdp.Session {
	s := from.Clone()
	stripICE(s)
	s.SetConnection(l.e.mediaHost(l.peer))
	done := false
	for _, m := range s.Media {
		if m.Type == "image" && m.Port != 0 && !done {
			m.Port, done = port, true
			continue
		}
		m.Port = 0
	}
	l.mu.Lock()
	prev := l.localSDP
	l.mu.Unlock()
	s.Lines = replaceOrigin(s.Lines, prev)
	return s
}

// faxSwitch answers a party's switch to T.38, after switching the other party.
func (l *Leg) faxSwitch(tx *sip.ServerTx, m *sip.Message, offer *sdp.Session) {
	refuse := func() { tx.Respond(sip.NewResponse(m, 488, "Not Acceptable Here")) }
	var other *Leg
	if l.call != nil {
		other = l.call.other(l)
	}
	if other == nil {
		refuse()
		return
	}
	other.mu.Lock()
	ok := other.answered && !other.ended
	other.mu.Unlock()
	if !ok {
		refuse()
		return
	}
	ep, oep := l.imageEndpoint(), other.imageEndpoint()
	if ep == nil || oep == nil {
		l.closeImage()
		other.closeImage()
		refuse()
		return
	}
	o := other.faxDescription(offer, oep.Port())
	ans, err := other.reinvite(o)
	im := imageMedia(ans)
	if err != nil || im == nil {
		l.closeImage()
		other.closeImage()
		if err != nil {
			l.e.Log.Info("fax stays on sound: the other side does not take T.38", "call", l.call.ID, "why", err)
		}
		refuse()
		return
	}
	oep.SetRemote(ans.Connection(im), im.Port)
	other.mu.Lock()
	if !other.onFax {
		other.preFax = other.localSDP
	}
	other.localSDP, other.onFax = o, true
	other.mu.Unlock()

	// The party gets the other side's answer: its T.38 settings, BitVoice's address and port.
	answer := l.faxDescription(ans, ep.Port())
	if oi := imageMedia(offer); oi != nil {
		ep.SetRemote(offer.Connection(oi), oi.Port)
	}
	media.Connect(ep, oep)
	l.mu.Lock()
	if !l.onFax {
		l.preFax = l.localSDP
	}
	l.localSDP, l.onFax = answer, true
	l.mu.Unlock()
	r := sip.NewResponse(m, 200, "")
	r.Set("Contact", l.e.localContact(l.peer, ""))
	r.Set("Content-Type", "application/sdp")
	r.Body = answer.Bytes()
	tx.Respond(r)
	l.e.Log.Info("fax switched to T.38", "call", l.call.ID)
	l.e.publish("call.fax", map[string]any{"call": l.call.ID, "t38": true})
}

// faxBack is a party going back to sound after a fax: the other party is switched back too.
func (l *Leg) faxBack() {
	l.mu.Lock()
	on := l.onFax
	l.mu.Unlock()
	if !on {
		return
	}
	l.closeImage()
	var other *Leg
	if l.call != nil {
		other = l.call.other(l)
	}
	if other == nil {
		return
	}
	other.mu.Lock()
	was, pre, cur := other.onFax, other.preFax, other.localSDP
	other.mu.Unlock()
	if !was || pre == nil {
		return
	}
	o := pre.Clone()
	o.Lines = replaceOrigin(o.Lines, cur)
	other.closeImage()
	ans, err := other.reinvite(o)
	if err != nil {
		l.e.Log.Warn("could not switch the other side back to sound after a fax", "err", err)
		return
	}
	other.setRemoteSDP(ans)
	other.answeredCrypto(ans)
	other.sendCodec(ans)
	other.mu.Lock()
	other.localSDP = o
	other.mu.Unlock()
}
