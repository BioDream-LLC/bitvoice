// Package tcap is SS7's Transaction Capabilities Application Part, ITU-T Q.773: the transaction
// messages (Begin, Continue, End, Abort), the dialogue portion (AARQ, AARE, ABRT with an application
// context name) and the components (Invoke, ReturnResult, ReturnError, Reject), in BER. It carries
// operations for an SS7 application over SCCP; the operations themselves (MAP, INAP, CAP ...) are the
// application's.
package tcap

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

// Message kinds, as their [APPLICATION n] tags.
const (
	Unidirectional = 1
	Begin          = 2
	End            = 4
	Continue       = 5
	Abort          = 7
)

// Component kinds, as their context tags.
const (
	Invoke              = 1
	ReturnResultLast    = 2
	ReturnError         = 3
	Reject              = 4
	ReturnResultNotLast = 7
)

// Dialogue PDU kinds.
const (
	AARQ = 0
	AARE = 1
	ABRT = 4
)

// DialogueAS is the dialogue-as-id object identifier, {itu-t recommendation q 773 as(1) dialogue-as(1) version1(1)}.
var DialogueAS = asn1.ObjectIdentifier{0, 0, 17, 773, 1, 1, 1}

// Message is one TCAP message.
type Message struct {
	Kind   int
	OTID   []byte // originating transaction ID (Begin, Continue)
	DTID   []byte // destination transaction ID (Continue, End, Abort)
	Cause  int    // Abort: the P-abort cause, or -1 for a user abort with a dialogue portion
	Dialog *Dialogue
	Comps  []Component
}

// Dialogue is the dialogue portion: an AARQ, AARE or ABRT.
type Dialogue struct {
	PDU         int
	Context     asn1.ObjectIdentifier // the application context name (AARQ, AARE)
	Result      int                   // AARE: 0 accepted, 1 reject-permanent
	Diagnostic  int                   // AARE: the source diagnostic (0 null, 1 no reason, 2 ACN not supported)
	UserInfo    []byte                // user-information, raw
	AbortSource int                   // ABRT: 0 user, 1 service provider
}

// Component is one component. Op is a local operation code (Global is used instead when set).
type Component struct {
	Kind     int
	InvokeID int
	LinkedID int // -1: none
	Op       int
	Global   asn1.ObjectIdentifier
	Error    int    // ReturnError: a local error code
	Param    []byte // the parameter, a whole BER element (tag and length included), or nil
	Problem  [2]int // Reject: the problem's type (0 general, 1 invoke, 2 return result, 3 return error) and value
}

// tlv writes one BER element with a definite length.
func tlv(class, tag int, constructed bool, content []byte) []byte {
	var b []byte
	first := byte(class<<6) | 0
	if constructed {
		first |= 0x20
	}
	if tag < 31 {
		b = append(b, first|byte(tag))
	} else {
		b = append(b, first|0x1F)
		var t []byte
		for v := tag; v > 0; v >>= 7 {
			t = append([]byte{byte(v & 0x7F)}, t...)
		}
		for i := 0; i < len(t)-1; i++ {
			t[i] |= 0x80
		}
		b = append(b, t...)
	}
	n := len(content)
	switch {
	case n < 0x80:
		b = append(b, byte(n))
	case n < 0x100:
		b = append(b, 0x81, byte(n))
	default:
		b = append(b, 0x82, byte(n>>8), byte(n))
	}
	return append(b, content...)
}

const (
	universal   = 0
	application = 1
	context     = 2
)

func integer(v int) []byte {
	b, _ := asn1.Marshal(v)
	return b[2:] // content octets of an INTEGER
}

// oid is the content octets of an OBJECT IDENTIFIER, or nil for one that cannot be written (fewer
// than two arcs, or a first arc above 2).
func oid(o asn1.ObjectIdentifier) []byte {
	b, err := asn1.Marshal(o)
	if err != nil || len(b) < 2 {
		return nil
	}
	return b[2:]
}

// ErrNoContext is a dialogue request or response without a valid application context name.
var ErrNoContext = errors.New("tcap: a dialogue request or response needs an application context name")

// element is one parsed BER element.
type element struct {
	class, tag  int
	constructed bool
	content     []byte
	raw         []byte
}

func parse(b []byte) (element, []byte, error) {
	if len(b) < 2 {
		return element{}, nil, errors.New("tcap: truncated element")
	}
	e := element{class: int(b[0] >> 6), constructed: b[0]&0x20 != 0, tag: int(b[0] & 0x1F)}
	i := 1
	if e.tag == 0x1F {
		e.tag = 0
		for {
			if i >= len(b) || i > 4 {
				return e, nil, errors.New("tcap: bad tag")
			}
			e.tag = e.tag<<7 | int(b[i]&0x7F)
			i++
			if b[i-1]&0x80 == 0 {
				break
			}
		}
	}
	if i >= len(b) {
		return e, nil, errors.New("tcap: truncated length")
	}
	n := int(b[i])
	i++
	if n&0x80 != 0 {
		k := n & 0x7F
		if k == 0 || k > 3 || i+k > len(b) {
			return e, nil, errors.New("tcap: indefinite or bad length")
		}
		n = 0
		for j := 0; j < k; j++ {
			n = n<<8 | int(b[i+j])
		}
		i += k
	}
	if i+n > len(b) {
		return e, nil, errors.New("tcap: element longer than the message")
	}
	e.content, e.raw = b[i:i+n], b[:i+n]
	return e, b[i+n:], nil
}

func children(b []byte) ([]element, error) {
	var out []element
	for len(b) > 0 {
		e, rest, err := parse(b)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		b = rest
	}
	return out, nil
}

func toInt(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	v := int(int8(b[0]))
	for _, x := range b[1:] {
		v = v<<8 | int(x)
	}
	return v
}

// Encode writes the message.
func (m Message) Encode() ([]byte, error) {
	var body []byte
	switch m.Kind {
	case Begin:
		body = append(body, tlv(application, 8, false, m.OTID)...)
	case Continue:
		body = append(body, tlv(application, 8, false, m.OTID)...)
		body = append(body, tlv(application, 9, false, m.DTID)...)
	case End, Abort:
		body = append(body, tlv(application, 9, false, m.DTID)...)
	case Unidirectional:
	default:
		return nil, fmt.Errorf("tcap: message kind %d", m.Kind)
	}
	if m.Kind == Abort && m.Dialog == nil {
		body = append(body, tlv(application, 10, false, integer(m.Cause))...)
	}
	if m.Dialog != nil {
		d, err := m.Dialog.encode()
		if err != nil {
			return nil, err
		}
		body = append(body, tlv(application, 11, true, d)...)
	}
	if len(m.Comps) > 0 && m.Kind != Abort {
		var cs []byte
		for _, c := range m.Comps {
			cs = append(cs, c.encode()...)
		}
		body = append(body, tlv(application, 12, true, cs)...)
	}
	return tlv(application, m.Kind, true, body), nil
}

// encode writes the dialogue portion's EXTERNAL.
func (d Dialogue) encode() ([]byte, error) {
	var pdu []byte
	switch d.PDU {
	case AARQ, AARE:
		acn := oid(d.Context)
		if acn == nil {
			return nil, ErrNoContext
		}
		pdu = append(pdu, tlv(context, 0, false, []byte{0x07, 0x80})...) // protocol-version: version1
		pdu = append(pdu, tlv(context, 1, true, tlv(universal, 6, false, acn))...)
		if d.PDU == AARE {
			pdu = append(pdu, tlv(context, 2, true, tlv(universal, 2, false, integer(d.Result)))...)
			diag := tlv(context, 1, true, tlv(universal, 2, false, integer(d.Diagnostic))) // dialogue-service-user
			pdu = append(pdu, tlv(context, 3, true, diag)...)
		}
		if len(d.UserInfo) > 0 {
			pdu = append(pdu, tlv(context, 30, true, d.UserInfo)...)
		}
	case ABRT:
		pdu = append(pdu, tlv(context, 0, false, integer(d.AbortSource))...)
		if len(d.UserInfo) > 0 {
			pdu = append(pdu, tlv(context, 30, true, d.UserInfo)...)
		}
	default:
		return nil, fmt.Errorf("tcap: dialogue PDU %d", d.PDU)
	}
	inner := tlv(application, d.PDU, true, pdu)
	ext := append(tlv(universal, 6, false, oid(DialogueAS)), tlv(context, 0, true, inner)...) // single-ASN1-type
	return tlv(universal, 8, true, ext), nil
}

func (c Component) encode() []byte {
	b := tlv(universal, 2, false, integer(c.InvokeID))
	opCode := func() []byte {
		if o := oid(c.Global); o != nil {
			return tlv(universal, 6, false, o)
		}
		return tlv(universal, 2, false, integer(c.Op))
	}
	switch c.Kind {
	case Invoke:
		if c.LinkedID >= 0 {
			b = append(b, tlv(context, 0, false, integer(c.LinkedID))...)
		}
		b = append(b, opCode()...)
		b = append(b, c.Param...)
	case ReturnResultLast, ReturnResultNotLast:
		if c.Param != nil {
			b = append(b, tlv(universal, 16, true, append(opCode(), c.Param...))...)
		}
	case ReturnError:
		b = append(b, tlv(universal, 2, false, integer(c.Error))...)
		b = append(b, c.Param...)
	case Reject:
		if c.InvokeID < 0 {
			b = tlv(universal, 5, false, nil)
		}
		b = append(b, tlv(context, c.Problem[0], false, integer(c.Problem[1]))...)
	}
	return tlv(context, c.Kind, true, b)
}

// Decode reads a message.
func Decode(b []byte) (Message, error) {
	top, rest, err := parse(b)
	if err != nil {
		return Message{}, err
	}
	if len(rest) != 0 || top.class != application || !top.constructed {
		return Message{}, errors.New("tcap: not a TCAP message")
	}
	m := Message{Kind: top.tag, Cause: -1}
	parts, err := children(top.content)
	if err != nil {
		return m, err
	}
	for _, p := range parts {
		if p.class != application {
			return m, fmt.Errorf("tcap: unexpected element %d/%d", p.class, p.tag)
		}
		switch p.tag {
		case 8:
			m.OTID = append([]byte(nil), p.content...)
		case 9:
			m.DTID = append([]byte(nil), p.content...)
		case 10:
			m.Cause = toInt(p.content)
		case 11:
			d, err := decodeDialogue(p.content)
			if err != nil {
				return m, err
			}
			m.Dialog = &d
		case 12:
			cs, err := children(p.content)
			if err != nil {
				return m, err
			}
			for _, ce := range cs {
				c, err := decodeComponent(ce)
				if err != nil {
					return m, err
				}
				m.Comps = append(m.Comps, c)
			}
		}
	}
	switch m.Kind {
	case Begin:
		if len(m.OTID) == 0 {
			return m, errors.New("tcap: a Begin without an originating transaction ID")
		}
	case Continue:
		if len(m.OTID) == 0 || len(m.DTID) == 0 {
			return m, errors.New("tcap: a Continue without both transaction IDs")
		}
	case End, Abort:
		if len(m.DTID) == 0 {
			return m, errors.New("tcap: no destination transaction ID")
		}
	case Unidirectional:
	default:
		return m, fmt.Errorf("tcap: message kind %d", m.Kind)
	}
	if len(m.OTID) > 4 || len(m.DTID) > 4 {
		return m, errors.New("tcap: a transaction ID longer than 4 octets")
	}
	return m, nil
}

func decodeDialogue(b []byte) (Dialogue, error) {
	ext, _, err := parse(b)
	if err != nil {
		return Dialogue{}, err
	}
	if ext.class != universal || ext.tag != 8 {
		return Dialogue{}, errors.New("tcap: the dialogue portion is not an EXTERNAL")
	}
	es, err := children(ext.content)
	if err != nil || len(es) < 2 {
		return Dialogue{}, errors.New("tcap: bad dialogue portion")
	}
	var id asn1.ObjectIdentifier
	if _, err := asn1.Unmarshal(es[0].raw, &id); err != nil || !id.Equal(DialogueAS) {
		return Dialogue{}, errors.New("tcap: the dialogue portion is not a structured dialogue (Q.773)")
	}
	inner, _, err := parse(es[len(es)-1].content)
	if err != nil {
		return Dialogue{}, err
	}
	d := Dialogue{PDU: inner.tag}
	if inner.tag != AARQ && inner.tag != AARE && inner.tag != ABRT {
		return d, fmt.Errorf("tcap: dialogue PDU %d", inner.tag)
	}
	fields, err := children(inner.content)
	if err != nil {
		return d, err
	}
	for _, f := range fields {
		switch {
		case f.class == context && f.tag == 1 && inner.tag != ABRT:
			var o asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(f.content, &o); err == nil {
				d.Context = o
			}
		case f.class == context && f.tag == 2:
			if v, _, err := parse(f.content); err == nil {
				d.Result = toInt(v.content)
			}
		case f.class == context && f.tag == 3:
			if c, _, err := parse(f.content); err == nil {
				if v, _, err := parse(c.content); err == nil {
					d.Diagnostic = toInt(v.content)
				}
			}
		case f.class == context && f.tag == 0 && inner.tag == ABRT:
			d.AbortSource = toInt(f.content)
		case f.class == context && f.tag == 30:
			d.UserInfo = append([]byte(nil), f.content...)
		}
	}
	if (d.PDU == AARQ || d.PDU == AARE) && oid(d.Context) == nil {
		return d, ErrNoContext
	}
	return d, nil
}

func decodeComponent(e element) (Component, error) {
	c := Component{Kind: e.tag, LinkedID: -1}
	if e.class != context {
		return c, errors.New("tcap: a component is not context tagged")
	}
	fs, err := children(e.content)
	if err != nil || len(fs) == 0 {
		return c, errors.New("tcap: an empty component")
	}
	if fs[0].class == universal && fs[0].tag == 5 { // Reject's NULL invoke ID
		c.InvokeID = -1
	} else {
		c.InvokeID = toInt(fs[0].content)
	}
	fs = fs[1:]
	readOp := func(f element) {
		if f.tag == 6 {
			var o asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(f.raw, &o); err == nil {
				c.Global = o
			}
		} else {
			c.Op = toInt(f.content)
		}
	}
	switch c.Kind {
	case Invoke:
		if len(fs) > 0 && fs[0].class == context && fs[0].tag == 0 {
			c.LinkedID = toInt(fs[0].content)
			fs = fs[1:]
		}
		if len(fs) == 0 {
			return c, errors.New("tcap: an Invoke without an operation code")
		}
		readOp(fs[0])
		if len(fs) > 1 {
			c.Param = append([]byte(nil), fs[1].raw...)
		}
	case ReturnResultLast, ReturnResultNotLast:
		if len(fs) > 0 {
			seq, err := children(fs[0].content)
			if err != nil || len(seq) == 0 {
				return c, errors.New("tcap: a bad result")
			}
			readOp(seq[0])
			if len(seq) > 1 {
				c.Param = append([]byte(nil), seq[1].raw...)
			}
		}
	case ReturnError:
		if len(fs) == 0 {
			return c, errors.New("tcap: a ReturnError without an error code")
		}
		c.Error = toInt(fs[0].content)
		if len(fs) > 1 {
			c.Param = append([]byte(nil), fs[1].raw...)
		}
	case Reject:
		if len(fs) == 0 {
			return c, errors.New("tcap: a Reject without a problem")
		}
		c.Problem = [2]int{fs[0].tag, toInt(fs[0].content)}
	default:
		return c, fmt.Errorf("tcap: component kind %d", c.Kind)
	}
	return c, nil
}
