package isdn

import (
	"errors"
	"strconv"
	"time"
)

// Supplementary services: names, redirecting and connected numbers, hold and transfer. Names go as
// the Display element and, on National ISDN-2, also as a ROSE callingName invoke in a Facility element,
// as libpri sends them; transfers are ETSI explicit call transfer (EctExecute on the held call) on
// EuroISDN and Two B-Channel Transfer (InitiateTransfer) on National ISDN-2.

// More message types and elements.
const (
	MsgHold          = 0x24
	MsgHoldAck       = 0x28
	MsgHoldReject    = 0x30
	MsgRetrieve      = 0x31
	MsgRetrieveAck   = 0x33
	MsgRetrieveRej   = 0x37
	MsgFacility      = 0x62
	ieFacility       = 0x1C
	ieRedirectingNum = 0x74

	CauseFacilityRejected = 29
	CauseNotImplemented   = 69
)

// Redirect reasons (Q.931 4.5.21a, octet 3b).
const (
	RedirectUnknown       = 0
	RedirectBusy          = 1
	RedirectNoReply       = 2
	RedirectDeflection    = 10
	RedirectUnconditional = 15
)

// Setup is what an outgoing call carries.
type Setup struct {
	Called, Calling string
	Name            string // the caller's name
	Redirecting     string // the number the call was forwarded from
	RedirectReason  int
}

const maxName = 34 // what displays and switches take (libpri: MAX_DISPLAY_TEXT less the charset)

// euro says whether the switch is EuroISDN (names as plain display text, ETSI transfers).
func (p *PRI) euro() bool { return p.cfg.Switch == "euro" || p.cfg.Switch == "euroisdn" }

// nameFac says whether names also go as a ROSE callingName facility (National ISDN-2, 5ESS, Q.SIG).
func (p *PRI) nameFac() bool {
	return !p.euro() && p.cfg.Switch != "dms100"
}

// ni2 says whether transfers are Two B-Channel Transfer (National ISDN-2 and 5ESS).
func (p *PRI) ni2() bool {
	return p.cfg.Switch == "" || p.cfg.Switch == "ni2" || p.cfg.Switch == "5ess"
}

// berLen writes a BER length.
func berLen(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	return []byte{0x81, byte(n)}
}

func berTLV(tag byte, v []byte) []byte { return append(append([]byte{tag}, berLen(len(v))...), v...) }

func berInt(n int) []byte {
	b := []byte{byte(n)}
	for n >>= 8; n != 0; n >>= 8 {
		b = append([]byte{byte(n)}, b...)
	}
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return berTLV(0x02, b)
}

// invoke builds a ROSE invoke component.
func invoke(id int, op []byte, arg []byte) []byte {
	v := append(berInt(id), op...)
	return berTLV(0xA1, append(v, arg...))
}

// nameFacility is National ISDN-2's callingName: invoke of operation 0, the name as
// presentation-allowed [0], after the Interpretation APDU.
func nameFacility(id int, name string) []byte {
	return append([]byte{0x9F, 0x8B, 0x01, 0x00}, invoke(id, berInt(0), berTLV(0x80, []byte(name)))...)
}

// ectFacility is ETSI EctExecute (operation 6, no argument).
func ectFacility(id int) []byte { return append([]byte{0x91}, invoke(id, berInt(6), nil)...) }

// ni2OID is National ISDN-2's InitiateTransfer operation, 1.2.840.10005.0.8.
var ni2OID = []byte{0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x15, 0x00, 0x08}

// twoBCTFacility is National ISDN-2's InitiateTransfer naming the other call's reference.
func twoBCTFacility(id, otherRef int) []byte {
	return append([]byte{0x91}, invoke(id, ni2OID, berTLV(0x30, berInt(otherRef)))...)
}

// berNext reads one TLV: tag, contents, the rest.
func berNext(b []byte) (tag byte, v, rest []byte, ok bool) {
	if len(b) < 2 {
		return 0, nil, nil, false
	}
	tag = b[0]
	n, i := int(b[1]), 2
	if n&0x80 != 0 {
		k := n & 0x7F
		if k == 0 || k > 2 || len(b) < 2+k {
			return 0, nil, nil, false
		}
		n = 0
		for _, c := range b[2 : 2+k] {
			n = n<<8 | int(c)
		}
		i = 2 + k
	}
	if len(b) < i+n {
		return 0, nil, nil, false
	}
	return tag, b[i : i+n], b[i+n:], true
}

func berIntVal(v []byte) int {
	n := 0
	for _, c := range v {
		n = n<<8 | int(c)
	}
	return n
}

// Component is one ROSE component of a Facility element.
type Component struct {
	Kind     byte // 0xA1 invoke, 0xA2 return result, 0xA3 return error, 0xA4 reject
	InvokeID int
	Op       int    // a local operation's value (-1 when it is an object identifier)
	OID      []byte // an object identifier operation's encoding
	Arg      []byte
}

// parseFacility reads a Facility element's components (any protocol profile; the network extension
// header and interpretation APDU are skipped).
func parseFacility(d []byte) []Component {
	if len(d) < 1 {
		return nil
	}
	b := d[len(facHeader(d)):]
	var out []Component
	for len(b) > 0 {
		tag, v, rest, ok := berNext(b)
		if !ok {
			break
		}
		b = rest
		if tag < 0xA1 || tag > 0xA4 {
			continue // network facility extension, interpretation, ...
		}
		c := Component{Kind: tag, Op: -1}
		if t, iv, r, ok := berNext(v); ok && t == 0x02 {
			c.InvokeID, v = berIntVal(iv), r
		}
		if tag == 0xA1 || tag == 0xA2 {
			if tag == 0xA2 {
				if t, seq, _, ok := berNext(v); ok && t == 0x30 {
					v = seq // the result's operation and value
				}
			}
			if t, ov, r, ok := berNext(v); ok {
				switch t {
				case 0x02:
					c.Op, v = berIntVal(ov), r
				case 0x06:
					c.OID, v = append([]byte{0x06, byte(len(ov))}, ov...), r
				}
			}
		}
		c.Arg = v
		out = append(out, c)
	}
	return out
}

// facilityName finds a callingName invoke's name (National ISDN-2, Q.SIG).
func facilityName(d []byte) string {
	for _, c := range parseFacility(d) {
		if c.Kind != 0xA1 || c.Op != 0 || c.OID != nil {
			continue
		}
		// Name ::= CHOICE { presentationAllowed [0], presentationRestricted [2], nameNotAvailable [4], ... }
		// each simple (an octet string) or extended ([1], [3]: a sequence holding one).
		if t, v, _, ok := berNext(c.Arg); ok {
			switch t {
			case 0x80:
				return string(v)
			case 0xA1:
				if _, s, _, ok := berNext(v); ok {
					return string(s)
				}
			}
		}
	}
	return ""
}

// displayText reads a Display element (a leading character set octet is skipped).
func displayText(d []byte) string {
	if len(d) > 0 && d[0]&0x80 != 0 {
		d = d[1:]
	}
	return string(d)
}

func clipName(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r < 0x7F {
			b = append(b, byte(r))
		}
	}
	if len(b) > maxName {
		b = b[:maxName]
	}
	return string(b)
}

// redirectingIE is the redirecting number: type and plan, presentation allowed, the reason.
func redirectingIE(digits string, reason int) []byte {
	n := numberIE(digits, true) // [ton/npi] [0x80 presentation] digits
	return append([]byte{n[0], 0x00, 0x80 | byte(reason&0x0F)}, n[2:]...)
}

// parseRedirecting reads a redirecting number and its reason.
func parseRedirecting(d []byte) (string, int) {
	if len(d) < 1 {
		return "", 0
	}
	i, reason := 1, RedirectUnknown
	if d[0]&0x80 == 0 && len(d) > 1 { // octet 3a
		i = 2
		if d[1]&0x80 == 0 && len(d) > 2 { // octet 3b
			reason, i = int(d[2]&0x0F), 3
		}
	}
	digits := string(d[i:])
	if (d[0]>>4)&7 == 1 {
		digits = "+" + digits
	}
	return digits, reason
}

func (p *PRI) invokeID() int {
	p.invokes = p.invokes%0x7F + 1
	return p.invokes
}

// ErrState is returned for an operation the call's state does not allow.
var ErrState = errors.New("isdn: not possible in the call's state")

// Hold puts an active call on hold (HOLD, then HOLD ACKNOWLEDGE). The B-channel stays the call's.
func (c *Call) Hold() error {
	return c.holdOp(MsgHold, MsgHoldAck, true)
}

// Retrieve takes a held call back.
func (c *Call) Retrieve() error {
	return c.holdOp(MsgRetrieve, MsgRetrieveAck, false)
}

func (c *Call) holdOp(t, ack byte, hold bool) error {
	p := c.p
	p.mu.Lock()
	if c.state != stActive || c.held == hold || c.waitFor != 0 {
		p.mu.Unlock()
		return ErrState
	}
	c.waitFor = ack
	ch := make(chan error, 1)
	c.waitDone = ch
	p.mu.Unlock()
	m := c.msg(t)
	if !hold {
		m.add(ieChannelID, channelID(c.Channel, true))
	}
	if err := p.send(m); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-time.After(4 * time.Second): // T4 of Q.932
		p.mu.Lock()
		c.waitFor, c.waitDone = 0, nil
		p.mu.Unlock()
		return errors.New("isdn: no answer to " + map[bool]string{true: "HOLD", false: "RETRIEVE"}[hold])
	}
}

// Transfer asks the network to join this call to other and free both B-channels: on EuroISDN this
// call is put on hold first and EctExecute sent on it, on National ISDN-2 an InitiateTransfer (Two
// B-Channel Transfer) names the other call. The network then clears both calls from us.
func (c *Call) Transfer(other *Call) error {
	p := c.p
	if other == nil || other.p != p || other == c {
		return errors.New("isdn: transfer needs another call on the same span")
	}
	p.mu.Lock()
	okState := c.state == stActive && other.state == stActive || (c.state == stActive && (other.state == stDelivered || other.state == stOutProceeding))
	p.mu.Unlock()
	if !okState {
		return ErrState
	}
	if p.dms() {
		return c.rltTransferCalls(other)
	}
	if !p.ni2() && !p.euro() {
		return errors.New("isdn: transfers on this switch type are not offered")
	}
	id := p.invokeID()
	var fac []byte
	if p.ni2() {
		ref := other.ref
		if !other.ours {
			ref |= 0x8000
		}
		fac = twoBCTFacility(id, ref)
	} else {
		if !c.isHeld() {
			if err := c.Hold(); err != nil {
				return err
			}
		}
		fac = ectFacility(id)
	}
	ch := make(chan error, 1)
	p.mu.Lock()
	c.invokeWait, c.invokeID, c.invokeDone = true, id, ch
	p.mu.Unlock()
	m := c.msg(MsgFacility)
	m.add(ieFacility, fac)
	if err := p.send(m); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-time.After(8 * time.Second):
		p.mu.Lock()
		c.invokeWait, c.invokeDone = false, nil
		p.mu.Unlock()
		if p.ni2() {
			return nil // National ISDN-2 switches answer a transfer by clearing the calls, not always with a result
		}
		return errors.New("isdn: the network did not answer the transfer")
	}
}

func (c *Call) isHeld() bool {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	return c.held
}

// supplementary handles the hold, retrieve and facility messages for a call. It reports whether
// it took the message.
func (c *Call) supplementary(m *Message) bool {
	p := c.p
	switch m.Type {
	case MsgHoldAck, MsgRetrieveAck, MsgHoldReject, MsgRetrieveRej:
		p.mu.Lock()
		want, done := c.waitFor, c.waitDone
		var err error
		switch {
		case m.Type == MsgHoldAck && want == MsgHoldAck:
			c.held = true
		case m.Type == MsgRetrieveAck && want == MsgRetrieveAck:
			c.held = false
		case m.Type == MsgHoldReject || m.Type == MsgRetrieveRej:
			cause := CauseNormalUnspecified
			if d, ok := m.Get(ieCause); ok {
				cause = parseCause(d)
			}
			err = &CauseError{Cause: cause}
		default:
			done = nil
		}
		if done != nil {
			c.waitFor, c.waitDone = 0, nil
		}
		p.mu.Unlock()
		if done != nil {
			done <- err
			if err == nil {
				kind := "held"
				if m.Type == MsgRetrieveAck {
					kind = "retrieved"
				}
				c.event(Event{Kind: kind})
			}
		}
		return true
	case MsgHold, MsgRetrieve:
		p.mu.Lock()
		accept := p.OnTransfer != nil && c.state == stActive && c.held == (m.Type == MsgRetrieve)
		if accept {
			c.held = m.Type == MsgHold
		}
		p.mu.Unlock()
		if accept {
			// Held only to be transferred: the B-channel stays the call's.
			if m.Type == MsgHold {
				_ = p.send(c.msg(MsgHoldAck))
			} else {
				r := c.msg(MsgRetrieveAck)
				r.add(ieChannelID, channelID(c.Channel, true))
				_ = p.send(r)
			}
			return true
		}
		// Being held by the other side is not offered: refuse, so its timer does not run out.
		r := c.msg(MsgHoldReject)
		if m.Type == MsgRetrieve {
			r = c.msg(MsgRetrieveRej)
		}
		r.add(ieCause, causeIE(CauseNotImplemented, p.cfg.Network))
		_ = p.send(r)
		return true
	case MsgFacility:
		for _, d := range m.GetAll(ieFacility) {
			c.facility(d, true)
		}
		return true
	}
	// Results and charges come in other messages too (a transfer's result and the final charge in the
	// DISCONNECT that follows).
	if m.Type != MsgSetup {
		for _, d := range m.GetAll(ieFacility) {
			c.facility(d, false)
		}
	}
	return false
}

// facility handles a Facility element's components; reply says whether an invoke BitVoice does not
// offer is answered (not in clearing messages).
func (c *Call) facility(d []byte, reply bool) {
	p := c.p
	{
		for _, comp := range parseFacility(d) {
			if c.rltComponent(comp, d, reply) || (comp.Kind == 0xA1 && c.chargeInvoke(comp, d, reply)) {
				continue
			}
			switch comp.Kind {
			case 0xA2, 0xA3, 0xA4:
				p.mu.Lock()
				done := c.invokeDone
				if !c.invokeWait || c.invokeID != comp.InvokeID {
					done = nil
				} else {
					c.invokeWait, c.invokeDone = false, nil
				}
				p.mu.Unlock()
				if done != nil {
					if comp.Kind == 0xA2 {
						done <- nil
					} else {
						done <- errors.New("isdn: the network refused the transfer")
					}
				}
			case 0xA1:
				if name := facilityName(d); name != "" {
					p.mu.Lock()
					c.Name = name
					p.mu.Unlock()
					continue
				}
				if reply && c.transferAsked(comp, d) {
					continue
				}
				if !reply {
					continue
				}
				// An operation BitVoice does not offer: return error "not available".
				r := c.msg(MsgFacility)
				r.add(ieFacility, append(append([]byte(nil), facHeader(d)...), berTLV(0xA3, append(berInt(comp.InvokeID), berInt(3)...))...))
				pendingReply(c, r)
			}
		}
	}
}

// CauseError is a refusal with a Q.850 cause.
type CauseError struct{ Cause int }

func (e *CauseError) Error() string { return "isdn: refused, cause " + strconv.Itoa(e.Cause) }

// transferAsked handles a transfer asked of the network side: it finds the other call and asks
// OnTransfer. It reports whether it answered.
func (c *Call) transferAsked(comp Component, d []byte) bool {
	p := c.p
	var other *Call
	switch {
	case comp.OID != nil && string(comp.OID) == string(ni2OID):
		c.event(Event{Kind: "transfer-asked"})
		// The argument: SEQUENCE { callReference INTEGER }, the other call as the sender numbers it.
		if t, seq, _, ok := berNext(comp.Arg); ok && t == 0x30 {
			if t, iv, _, ok := berNext(seq); ok && t == 0x02 {
				ref := berIntVal(iv)
				p.mu.Lock()
				other = p.calls[callKey{ref & 0x7FFF, ref&0x8000 != 0}]
				p.mu.Unlock()
			}
		}
	case comp.OID == nil && comp.Op == 6 && p.euro():
		c.event(Event{Kind: "transfer-asked"})
		p.mu.Lock()
		if c.held {
			for _, x := range p.calls {
				if x != c && x.state == stActive && !x.held {
					other = x
					break
				}
			}
		}
		p.mu.Unlock()
	default:
		return false
	}
	prof := facHeader(d)
	r := c.msg(MsgFacility)
	if other != nil && p.OnTransfer != nil && p.OnTransfer(c, other) {
		r.add(ieFacility, append(append([]byte(nil), prof...), berTLV(0xA2, berInt(comp.InvokeID))...))
	} else {
		r.add(ieFacility, append(append([]byte(nil), prof...), berTLV(0xA3, append(berInt(comp.InvokeID), berInt(3)...))...))
	}
	_ = p.send(r)
	return true
}
