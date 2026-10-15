package isdn

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Advice of charge (ETSI EN 300 182, EuroISDN only): what a call costs, told by the network during
// the call (AOC-D) and at its end (AOC-E), in money or in charging units. BitVoice reads it on the
// user side, and gives it on the network side. Requests for it (ChargingRequest) go in SETUP when
// Config.RequestCharge is set.

// ETSI ROSE operations.
const (
	opChargingRequest = 30
	opAOCSCurrency    = 31
	opAOCDCurrency    = 33
	opAOCDUnits       = 34
	opAOCECurrency    = 35
	opAOCEUnits       = 36
)

// Charging cases a ChargingRequest names.
const (
	chargeAtSetup  = 0
	chargeDuring   = 1
	chargeAtTheEnd = 2
)

// Charge is one advice of charge.
type Charge struct {
	Final bool   // AOC-E, the call's whole charge; else AOC-D, a figure during the call
	Kind  string // currency, units, free or unavailable
	// Currency and Amount × 10^(Multiplier-3): Multiplier 0 is thousandths, 1 hundredths, 3 units, 6 thousands.
	Currency   string
	Amount     int64
	Multiplier int
	Units      int64
	Total      bool // AOC-D: the running total so far (else the amount since the last one)
}

// Money is the amount in plain decimal (Amount scaled by Multiplier), like "1.50".
func (c Charge) Money() string {
	e := c.Multiplier - 3
	if e >= 0 {
		v := c.Amount
		for ; e > 0; e-- {
			v *= 10
		}
		return strconv.FormatInt(v, 10)
	}
	s := strconv.FormatInt(c.Amount, 10)
	d := -e
	for len(s) <= d {
		s = "0" + s
	}
	return s[:len(s)-d] + "." + s[len(s)-d:]
}

// String says it in words: "EUR 1.50", "5 units", "free".
func (c Charge) String() string {
	switch c.Kind {
	case "currency":
		return strings.TrimSpace(c.Currency + " " + c.Money())
	case "units":
		if c.Units == 1 {
			return "1 unit"
		}
		return fmt.Sprintf("%d units", c.Units)
	case "free":
		return "free"
	}
	return "not available"
}

// ParseMoney turns "1.50" into an amount and multiplier (hundredths here), for Charge.
func ParseMoney(s string) (amount int64, multiplier int, err error) {
	s = strings.TrimSpace(s)
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 3 {
		return 0, 0, errors.New("isdn: at most three decimal places")
	}
	v, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || v < 0 {
		return 0, 0, errors.New("isdn: not an amount: " + s)
	}
	return v, 3 - len(frac), nil
}

// berIntT is an integer under any tag.
func berIntT(tag byte, n int64) []byte {
	b := []byte{byte(n)}
	for v := n >> 8; v != 0 && v != -1; v >>= 8 {
		b = append([]byte{byte(v)}, b...)
	}
	if n >= 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return berTLV(tag, b)
}

func berNull(tag byte) []byte { return []byte{tag, 0} }

// recordedCurrency is RecordedCurrency under a tag: [1] currency, [2] Amount { [1] amount, [2] multiplier }.
func recordedCurrency(tag byte, c Charge) []byte {
	amount := append(berIntT(0x81, c.Amount), berIntT(0x82, int64(c.Multiplier))...)
	cur := c.Currency
	if len(cur) > 10 {
		cur = cur[:10]
	}
	return berTLV(tag, append(berTLV(0x81, []byte(cur)), berTLV(0xA2, amount)...))
}

// recordedUnits is a RecordedUnitsList under a tag, one record.
func recordedUnits(tag byte, n int64) []byte {
	return berTLV(tag, berTLV(0x30, berIntT(0x02, n)))
}

// chargeFacility is an AOC-D or AOC-E invoke, as libpri encodes them.
func chargeFacility(id int, c Charge) []byte {
	var op int
	var arg []byte
	switch {
	case !c.Final && c.Kind == "units":
		op = opAOCDUnits
	case !c.Final:
		op = opAOCDCurrency
	case c.Kind == "units":
		op = opAOCEUnits
	default:
		op = opAOCECurrency
	}
	switch c.Kind {
	case "unavailable", "":
		arg = berNull(0x05)
	case "free":
		if c.Final {
			arg = berTLV(0x30, berNull(0x81))
		} else {
			arg = berNull(0x81)
		}
	default:
		var rec []byte
		if c.Kind == "units" {
			rec = recordedUnits(0xA1, c.Units)
		} else {
			rec = recordedCurrency(0xA1, c)
		}
		if c.Final {
			arg = berTLV(0x30, berTLV(0x30, rec))
		} else {
			total := int64(0)
			if c.Total {
				total = 1
			}
			arg = berTLV(0x30, append(rec, berIntT(0x82, total)...))
		}
	}
	return append([]byte{0x91}, invoke(id, berInt(op), arg)...)
}

// chargingRequest asks for advice of charge in one charging case.
func chargingRequest(id, when int) []byte {
	return append([]byte{0x91}, invoke(id, berInt(opChargingRequest), berIntT(0x0A, int64(when)))...)
}

// parseCharge reads an AOC-D or AOC-E invoke's argument.
func parseCharge(op int, arg []byte) (Charge, bool) {
	c := Charge{Final: op == opAOCECurrency || op == opAOCEUnits}
	units := op == opAOCDUnits || op == opAOCEUnits
	tag, v, _, ok := berNext(arg)
	if !ok {
		return c, false
	}
	switch tag {
	case 0x05:
		c.Kind = "unavailable"
		return c, true
	case 0x81:
		c.Kind = "free"
		return c, true
	case 0x30:
	default:
		return c, false
	}
	if c.Final {
		// AOCE...Info: CHOICE { specific SEQUENCE, freeOfCharge [1] }, then a charging association.
		t, inner, _, ok := berNext(v)
		if !ok {
			return c, false
		}
		if t == 0x81 {
			c.Kind = "free"
			return c, true
		}
		if t != 0x30 {
			return c, false
		}
		v = inner
	}
	for len(v) > 0 {
		t, x, rest, ok := berNext(v)
		if !ok {
			break
		}
		v = rest
		switch t {
		case 0xA1:
			if units {
				c.Kind = "units"
				// SEQUENCE OF RecordedUnits { CHOICE { INTEGER, NULL }, type OPTIONAL }: added up.
				for len(x) > 0 {
					rt, rv, rrest, ok := berNext(x)
					if !ok {
						break
					}
					x = rrest
					if rt != 0x30 {
						continue
					}
					if ut, uv, _, ok := berNext(rv); ok && ut == 0x02 {
						c.Units += int64(berIntVal(uv))
					}
				}
			} else {
				c.Kind = "currency"
				for len(x) > 0 {
					ct, cv, crest, ok := berNext(x)
					if !ok {
						break
					}
					x = crest
					switch ct {
					case 0x81:
						c.Currency = string(cv)
					case 0xA2:
						for len(cv) > 0 {
							at, av, arest, ok := berNext(cv)
							if !ok {
								break
							}
							cv = arest
							switch at {
							case 0x81:
								c.Amount = int64(berIntVal(av))
							case 0x82:
								c.Multiplier = berIntVal(av)
							}
						}
					}
				}
			}
		case 0x82:
			if !c.Final {
				c.Total = berIntVal(x) == 1
			}
		}
	}
	return c, c.Kind != ""
}

// ErrNoCharge is returned where advice of charge is not offered (not EuroISDN).
var ErrNoCharge = errors.New("isdn: advice of charge is EuroISDN's; this span's switch type has none")

// SendCharge gives the other side advice of charge during the call (AOC-D), in a FACILITY.
func (c *Call) SendCharge(ch Charge) error {
	p := c.p
	if !p.euro() {
		return ErrNoCharge
	}
	ch.Final = false
	m := c.msg(MsgFacility)
	m.add(ieFacility, chargeFacility(p.invokeID(), ch))
	return p.send(m)
}

// SetFinalCharge sets the call's whole charge (AOC-E), given in the DISCONNECT or RELEASE that ends it.
func (c *Call) SetFinalCharge(ch Charge) error {
	if !c.p.euro() {
		return ErrNoCharge
	}
	ch.Final = true
	c.p.mu.Lock()
	c.finalCharge = &ch
	c.p.mu.Unlock()
	return nil
}

// addFinalCharge puts the AOC-E, if one is set and not yet sent, in a clearing message. Called
// without p.mu.
func (c *Call) addFinalCharge(m *Message) {
	c.p.mu.Lock()
	ch := c.finalCharge
	c.finalCharge = nil
	c.p.mu.Unlock()
	if ch != nil {
		m.add(ieFacility, chargeFacility(c.p.invokeID(), *ch))
	}
}

// chargeInvoke handles an advice of charge or a request for it. It reports whether it was one.
func (c *Call) chargeInvoke(comp Component, d []byte, reply bool) bool {
	p := c.p
	if !p.euro() || comp.OID != nil {
		return false
	}
	switch comp.Op {
	case opAOCDCurrency, opAOCDUnits, opAOCECurrency, opAOCEUnits:
		ch, ok := parseCharge(comp.Op, comp.Arg)
		if !ok {
			return true
		}
		p.mu.Lock()
		c.Charge = &ch
		p.mu.Unlock()
		c.event(Event{Kind: "charge", Charge: &ch})
		return true
	case opAOCSCurrency:
		return true // tariffs at the start: read, and not kept (the D and E figures are what is billed)
	case opChargingRequest:
		when := -1
		if t, v, _, ok := berNext(comp.Arg); ok && t == 0x0A {
			when = berIntVal(v)
		}
		p.mu.Lock()
		switch when {
		case chargeDuring:
			c.ChargeAsked |= AskDuring
		case chargeAtTheEnd:
			c.ChargeAsked |= AskAtTheEnd
		}
		offer := p.cfg.OfferCharge && (when == chargeDuring || when == chargeAtTheEnd)
		p.mu.Unlock()
		if !reply {
			return true
		}
		r := c.msg(MsgFacility)
		if offer {
			// Return result: chargingInfoFollows (NULL).
			res := berTLV(0x30, append(berInt(opChargingRequest), berNull(0x05)...))
			r.add(ieFacility, append([]byte{0x91}, berTLV(0xA2, append(berInt(comp.InvokeID), res...))...))
		} else {
			// Return error: notAvailable (3), as for any service not offered.
			r.add(ieFacility, append([]byte{0x91}, berTLV(0xA3, append(berInt(comp.InvokeID), berInt(3)...))...))
		}
		pendingReply(c, r)
		return true
	}
	return false
}

// What a caller asked to be told (Call.ChargeAsked).
const (
	AskDuring   = 1
	AskAtTheEnd = 2
)

// pendingReply sends a reply to something in SETUP once CALL PROCEEDING has gone, or at once.
func pendingReply(c *Call, r *Message) {
	c.p.mu.Lock()
	if c.state == stCallPresent {
		c.afterProceeding = append(c.afterProceeding, r)
		c.p.mu.Unlock()
		return
	}
	c.p.mu.Unlock()
	_ = c.p.send(r)
}

// ChargeContentType is advice of charge over SIP: the XML of 3GPP TS 24.647 (ETSI AOC), sent in an
// INFO during the call and in the BYE that ends it.
const ChargeContentType = "application/vnd.etsi.aoc+xml"

type aocXML struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/aoc aoc"`
	D       *aocPart `xml:"aoc-d,omitempty"`
	E       *aocPart `xml:"aoc-e,omitempty"`
}

type aocPart struct {
	ChargingInfo string      `xml:"charging-info,omitempty"`
	Recorded     aocRecorded `xml:"recorded-charges"`
}

type aocRecorded struct {
	Currency *struct {
		ID     string `xml:"currency-id"`
		Amount string `xml:"currency-amount"`
	} `xml:"recorded-currency-units,omitempty"`
	Units *struct {
		Unit []struct {
			Value int64 `xml:"unit-value"`
		} `xml:"recorded-unit"`
	} `xml:"recorded-units,omitempty"`
	Free         *struct{} `xml:"free-charge,omitempty"`
	NotAvailable *struct{} `xml:"not-available,omitempty"`
}

// ChargeXML writes an advice of charge as the XML SIP carries.
func ChargeXML(c Charge) []byte {
	part := &aocPart{}
	switch c.Kind {
	case "currency":
		part.Recorded.Currency = &struct {
			ID     string `xml:"currency-id"`
			Amount string `xml:"currency-amount"`
		}{c.Currency, c.Money()}
	case "units":
		part.Recorded.Units = &struct {
			Unit []struct {
				Value int64 `xml:"unit-value"`
			} `xml:"recorded-unit"`
		}{Unit: []struct {
			Value int64 `xml:"unit-value"`
		}{{c.Units}}}
	case "free":
		part.Recorded.Free = &struct{}{}
	default:
		part.Recorded.NotAvailable = &struct{}{}
	}
	x := aocXML{}
	if c.Final {
		x.E = part
	} else {
		part.ChargingInfo = "subtotal"
		if c.Total {
			part.ChargingInfo = "total"
		}
		x.D = part
	}
	b, _ := xml.Marshal(x)
	return append([]byte(xml.Header), b...)
}

// ParseChargeXML reads the XML of an advice of charge from SIP.
func ParseChargeXML(b []byte) (Charge, bool) {
	var x aocXML
	if err := xml.Unmarshal(b, &x); err != nil {
		return Charge{}, false
	}
	part, c := x.D, Charge{}
	if x.E != nil {
		part, c.Final = x.E, true
	}
	if part == nil {
		return c, false
	}
	c.Total = part.ChargingInfo == "total"
	r := part.Recorded
	switch {
	case r.Currency != nil:
		a, m, err := ParseMoney(r.Currency.Amount)
		if err != nil {
			return c, false
		}
		c.Kind, c.Currency, c.Amount, c.Multiplier = "currency", strings.TrimSpace(r.Currency.ID), a, m
	case r.Units != nil:
		c.Kind = "units"
		for _, u := range r.Units.Unit {
			c.Units += u.Value
		}
	case r.Free != nil:
		c.Kind = "free"
	default:
		c.Kind = "unavailable"
	}
	return c, true
}
