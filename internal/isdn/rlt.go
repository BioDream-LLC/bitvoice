package isdn

import (
	"errors"
	"time"
)

// DMS-100 Release Link Trunk transfer (Nortel's RLT): on an outgoing call, the PBX's SETUP says it can
// transfer (rltOperationInd), and the switch answers with an identifier for the call. To join two
// calls, the PBX names that identifier on the other call (rltThirdParty); the switch connects the two
// parties and clears both calls from the PBX, freeing its B-channels. As libpri encodes it: a Facility
// element with the protocol profile 0x11 and service 0x3E, the invoke identifier standing for the
// operation (1 and 2).

const (
	rltOperationInd = 1
	rltThirdParty   = 2

	rltErrBridgeFail     = 0x10
	rltErrCallIDNotFound = 0x11
	rltErrNotAllowed     = 0x12
)

// dms says whether the span is a Nortel DMS-100.
func (p *PRI) dms() bool { return p.cfg.Switch == "dms100" }

// dmsHeader is the Facility element's start for DMS-100: ROSE, without the extension bit, then the
// RLT service.
var dmsHeader = []byte{0x11, 0xBE}

func rltFacility(comp []byte) []byte { return append(append([]byte(nil), dmsHeader...), comp...) }

// rltAbility is the rltOperationInd invoke an outgoing SETUP carries.
func rltAbility() []byte {
	return rltFacility(invoke(rltOperationInd, berInt(rltOperationInd), nil))
}

// rltAbilityResult gives the call its identifier.
func rltAbilityResult(id int) []byte {
	res := berTLV(0x30, append(berInt(rltOperationInd), berIntT(0x80, int64(id))...))
	return rltFacility(berTLV(0xA2, append(berInt(rltOperationInd), res...)))
}

// rltTransfer is rltThirdParty naming the call to join to: SEQUENCE { [0] callId, [1] reason }.
func rltTransfer(id int) []byte {
	arg := berTLV(0x30, append(berIntT(0x80, int64(id&0xFFFFFF)), berIntT(0x81, 0)...))
	return rltFacility(invoke(rltThirdParty, berInt(rltThirdParty), arg))
}

// facHeader is a Facility element's protocol profile octets: two when the first lacks the extension
// bit (DMS-100), else one.
func facHeader(d []byte) []byte {
	if len(d) >= 2 && d[0]&0x80 == 0 {
		return d[:2]
	}
	if len(d) >= 1 {
		return d[:1]
	}
	return []byte{0x91}
}

// rltComponent handles the DMS-100 RLT parts of a Facility. It reports whether it took the component.
func (c *Call) rltComponent(comp Component, d []byte, reply bool) bool {
	p := c.p
	if !p.dms() {
		return false
	}
	switch {
	case comp.Kind == 0xA2 && comp.InvokeID == rltOperationInd && !p.cfg.Network:
		// The switch's answer to our SETUP: the call's identifier.
		if t, v, _, ok := berNext(comp.Arg); ok && t == 0x80 {
			p.mu.Lock()
			c.rltID = berIntVal(v)
			p.mu.Unlock()
		}
		return true
	case comp.Kind == 0xA1 && comp.Op == rltOperationInd && p.cfg.Network:
		p.mu.Lock()
		if c.rltID == 0 {
			p.rltNext = p.rltNext%0xFFFFFE + 1
			c.rltID = p.rltNext
		}
		id := c.rltID
		p.mu.Unlock()
		if reply {
			r := c.msg(MsgFacility)
			r.add(ieFacility, rltAbilityResult(id))
			pendingReply(c, r)
		}
		return true
	case comp.Kind == 0xA1 && comp.Op == rltThirdParty && p.cfg.Network:
		c.event(Event{Kind: "transfer-asked"})
		id := -1
		if t, seq, _, ok := berNext(comp.Arg); ok && t == 0x30 {
			if t, v, _, ok := berNext(seq); ok && t == 0x80 {
				id = berIntVal(v)
			}
		}
		var other *Call
		p.mu.Lock()
		for _, x := range p.calls {
			if x != c && x.rltID == id && id > 0 {
				other = x
			}
		}
		p.mu.Unlock()
		r := c.msg(MsgFacility)
		switch {
		case other == nil:
			r.add(ieFacility, rltFacility(berTLV(0xA3, append(berInt(rltThirdParty), berInt(rltErrCallIDNotFound)...))))
		case p.OnTransfer != nil && p.OnTransfer(c, other):
			r.add(ieFacility, rltFacility(berTLV(0xA2, berInt(rltThirdParty))))
		default:
			r.add(ieFacility, rltFacility(berTLV(0xA3, append(berInt(rltThirdParty), berInt(rltErrNotAllowed)...))))
		}
		_ = p.send(r)
		return true
	}
	return false
}

// ErrNotTransferable is returned for a DMS-100 transfer where the switch gave neither call an identifier.
var ErrNotTransferable = errors.New("isdn: the DMS-100 gave neither call a transfer identifier (one of them must be a call BitVoice placed)")

// rltTransferCalls asks the DMS-100 to join c and other (Call.Transfer on a DMS-100 span).
func (c *Call) rltTransferCalls(other *Call) error {
	p := c.p
	p.mu.Lock()
	bearer, id := c, other.rltID
	if id == 0 {
		bearer, id = other, c.rltID
	}
	p.mu.Unlock()
	if id == 0 {
		return ErrNotTransferable
	}
	ch := make(chan error, 1)
	p.mu.Lock()
	bearer.invokeWait, bearer.invokeID, bearer.invokeDone = true, rltThirdParty, ch
	p.mu.Unlock()
	m := bearer.msg(MsgFacility)
	m.add(ieFacility, rltTransfer(id))
	if err := p.send(m); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-time.After(8 * time.Second):
		p.mu.Lock()
		bearer.invokeWait, bearer.invokeDone = false, nil
		p.mu.Unlock()
		return errors.New("isdn: the DMS-100 did not answer the transfer")
	}
}
