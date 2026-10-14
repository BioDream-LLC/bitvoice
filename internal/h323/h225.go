package h323

import (
	"net/netip"
)

// The H.225.0 and H.245 types a Fast Connect call needs, decoded to what the gateway uses and
// encoded from it. Root components are decoded in full (the encoding requires it); extension
// additions are kept as open types and only the ones needed are read.

// h225 protocol identifier 0.0.8.2250.0.4 (version 4), as BER contents.
var protocolID = []byte{0x00, 0x08, 0x91, 0x4a, 0x00, 0x04}

// Message bodies of H323-UU-PDU.
const (
	bodySetup = iota
	bodyCallProceeding
	bodyConnect
	bodyAlerting
	bodyInformation
	bodyReleaseComplete
	bodyFacility
	bodyCount
)

// uuie is what the gateway reads from (or writes into) a message's H323-UserInformation.
type uuie struct {
	body        int      // bodySetup ...; -1 for an extension body (progress, notify ...)
	ext         bool     // the body is an extension alternative
	callerAlias []string // Setup: sourceAddress
	calledAlias []string // Setup: destinationAddress
	confID      []byte
	callID      []byte
	fastStart   [][]byte // OpenLogicalChannel encodings
	reason      int      // ReleaseComplete: the reason's index, -1 when absent
}

func decodeUU(b []byte) (u *uuie, err error) {
	defer catch(&err)
	r := &reader{b: b}
	u = &uuie{reason: -1}
	// H323-UserInformation ::= SEQUENCE { h323-uu-pdu, user-data OPTIONAL, ... }
	ext := r.bit()
	userData := r.bit()
	// H323-UU-PDU ::= SEQUENCE { h323-message-body CHOICE, nonStandardData OPTIONAL, ..., ... }
	pduExt := r.bit()
	pduNS := r.bit()
	idx, isExt := r.choice(bodyCount, true)
	u.body, u.ext = idx, isExt
	if isExt {
		r.openType()
	} else {
		switch idx {
		case bodySetup:
			decodeSetup(r, u)
		case bodyCallProceeding, bodyAlerting:
			decodeProceeding(r, u, idx)
		case bodyConnect:
			decodeConnect(r, u)
		case bodyReleaseComplete:
			decodeRelease(r, u)
		default:
			return u, nil // information, facility: not needed
		}
	}
	if pduNS {
		nonStandardParameter(r)
	}
	if pduExt {
		r.extensions()
	}
	if userData {
		uext := r.bit()
		r.constrained(0, 255)
		r.octetString(1, 131)
		if uext {
			r.extensions()
		}
	}
	if ext {
		r.extensions()
	}
	return u, nil
}

// h221NonStandard reads H.225's H221NonStandard (extensible).
func h221NonStandard(r *reader) {
	ext := r.bit()
	r.constrained(0, 255)
	r.constrained(0, 255)
	r.constrained(0, 65535)
	if ext {
		r.extensions()
	}
}

func nonStandardParameter(r *reader) {
	// NonStandardIdentifier ::= CHOICE { object, h221NonStandard, ... }
	idx, ext := r.choice(2, true)
	switch {
	case ext:
		r.openType()
	case idx == 0:
		r.oid()
	default:
		h221NonStandard(r)
	}
	r.octetString(0, -1)
}

// capsOnly reads one of the H310Caps .. T120OnlyCaps sequences, whose root is nonStandardData OPTIONAL.
func capsOnly(r *reader) {
	ext := r.bit()
	if r.bit() {
		nonStandardParameter(r)
	}
	if ext {
		r.extensions()
	}
}

func supportedProtocols(r *reader) {
	idx, ext := r.choice(9, true)
	switch {
	case ext:
		r.openType()
	case idx == 0:
		nonStandardParameter(r)
	default:
		capsOnly(r)
	}
}

func endpointType(r *reader) {
	ext := r.bit()
	opt := r.bits(6) // nonStandardData, vendor, gatekeeper, gateway, mcu, terminal
	if opt&0x20 != 0 {
		nonStandardParameter(r)
	}
	if opt&0x10 != 0 { // VendorIdentifier
		vext := r.bit()
		vopt := r.bits(2)
		h221NonStandard(r)
		if vopt&2 != 0 {
			r.octetString(1, 256)
		}
		if vopt&1 != 0 {
			r.octetString(1, 256)
		}
		if vext {
			r.extensions()
		}
	}
	if opt&0x08 != 0 { // GatekeeperInfo
		capsOnly(r)
	}
	if opt&0x04 != 0 { // GatewayInfo { protocol SEQUENCE OF SupportedProtocols OPTIONAL, nonStandardData OPTIONAL, ... }
		gext := r.bit()
		gopt := r.bits(2)
		if gopt&2 != 0 {
			for n := r.count(); n > 0; n-- {
				supportedProtocols(r)
			}
		}
		if gopt&1 != 0 {
			nonStandardParameter(r)
		}
		if gext {
			r.extensions()
		}
	}
	if opt&0x02 != 0 { // McuInfo
		capsOnly(r)
	}
	if opt&0x01 != 0 { // TerminalInfo
		capsOnly(r)
	}
	r.bit() // mc
	r.bit() // undefinedNode
	if ext {
		r.extensions()
	}
}

// aliasAddress reads an AliasAddress, saying whether it is digits (dialedDigits) or a name
// (h323-ID); extension alternatives (url, email, party number ...) are skipped.
func aliasAddress(r *reader) (string, bool) {
	idx, ext := r.choice(2, true)
	switch {
	case ext:
		r.openType()
		return "", false
	case idx == 0:
		return r.digits(1, 128), true
	default:
		return r.bmpString(1, 256), false
	}
}

// aliases reads a list of aliases: numbers first, then names.
func aliases(r *reader) []string {
	var nums, names []string
	for n := r.count(); n > 0; n-- {
		a, digits := aliasAddress(r)
		switch {
		case a == "":
		case digits:
			nums = append(nums, a)
		default:
			names = append(names, a)
		}
	}
	return append(nums, names...)
}

// transportAddress reads H.225's TransportAddress, giving the IP address and port when it is one.
func transportAddress(r *reader) netip.AddrPort {
	idx, ext := r.choice(7, true)
	if ext {
		r.openType()
		return netip.AddrPort{}
	}
	switch idx {
	case 0: // ipAddress SEQUENCE { ip OCTET STRING (SIZE(4)), port INTEGER(0..65535) }
		ip := r.octetString(4, 4)
		port := r.constrained(0, 65535)
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(ip)), uint16(port))
	case 1: // ipSourceRoute
		sext := r.bit()
		ip := r.octetString(4, 4)
		port := r.constrained(0, 65535)
		for n := r.count(); n > 0; n-- {
			r.octetString(4, 4)
		}
		if _, e := r.choice(2, true); e {
			r.openType()
		}
		if sext {
			r.extensions()
		}
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(ip)), uint16(port))
	case 2: // ipxAddress
		r.octetString(6, 6)
		r.octetString(4, 4)
		r.octetString(2, 2)
	case 3: // ip6Address
		sext := r.bit()
		ip := r.octetString(16, 16)
		port := r.constrained(0, 65535)
		if sext {
			r.extensions()
		}
		return netip.AddrPortFrom(netip.AddrFrom16([16]byte(ip)), uint16(port))
	case 4:
		r.octetString(16, 16)
	case 5:
		r.octetString(1, 20)
	case 6:
		nonStandardParameter(r)
	}
	return netip.AddrPort{}
}

func decodeSetup(r *reader, u *uuie) {
	ext := r.bit()
	opt := r.bits(7) // h245Address, sourceAddress, destinationAddress, destCallSignalAddress, destExtraCallInfo, destExtraCRV, callServices
	r.oid()
	if opt&0x40 != 0 {
		transportAddress(r)
	}
	if opt&0x20 != 0 {
		u.callerAlias = aliases(r)
	}
	endpointType(r)
	if opt&0x10 != 0 {
		u.calledAlias = aliases(r)
	}
	if opt&0x08 != 0 {
		transportAddress(r)
	}
	if opt&0x04 != 0 {
		aliases(r)
	}
	if opt&0x02 != 0 {
		for n := r.count(); n > 0; n-- {
			r.constrained(0, 65535)
		}
	}
	r.bit() // activeMC
	u.confID = r.octetString(16, 16)
	if _, e := r.choice(3, true); e { // conferenceGoal
		r.openType()
	}
	if opt&0x01 != 0 { // QseriesOptions
		qext := r.bit()
		r.bits(7)
		q4ext := r.bit()
		r.bits(2)
		if q4ext {
			r.extensions()
		}
		if qext {
			r.extensions()
		}
	}
	if _, e := r.choice(4, true); e { // callType
		r.openType()
	}
	if ext {
		adds := r.extensions()
		if b, ok := adds[2]; ok {
			u.callID = callIdentifier(b)
		}
		if b, ok := adds[6]; ok {
			u.fastStart = fastStartList(b)
		}
	}
}

func callIdentifier(b []byte) []byte {
	var out []byte
	func() {
		defer func() { _ = recover() }()
		r := &reader{b: b}
		ext := r.bit()
		out = append([]byte(nil), r.octetString(16, 16)...)
		_ = ext
	}()
	return out
}

func fastStartList(b []byte) [][]byte {
	var out [][]byte
	func() {
		defer func() { _ = recover() }()
		r := &reader{b: b}
		for n := r.count(); n > 0; n-- {
			out = append(out, append([]byte(nil), r.octetString(0, -1)...))
		}
	}()
	return out
}

// decodeProceeding reads CallProceeding-UUIE and Alerting-UUIE: protocolIdentifier, destinationInfo,
// h245Address OPTIONAL; fastStart is addition 4 of both.
func decodeProceeding(r *reader, u *uuie, body int) {
	ext := r.bit()
	h245 := r.bit()
	r.oid()
	endpointType(r)
	if h245 {
		transportAddress(r)
	}
	if ext {
		if b, ok := r.extensions()[4]; ok {
			u.fastStart = fastStartList(b)
		}
	}
}

// decodeConnect reads Connect-UUIE: protocolIdentifier, h245Address OPTIONAL, destinationInfo,
// conferenceID; fastStart is addition 4.
func decodeConnect(r *reader, u *uuie) {
	ext := r.bit()
	h245 := r.bit()
	r.oid()
	if h245 {
		transportAddress(r)
	}
	endpointType(r)
	u.confID = r.octetString(16, 16)
	if ext {
		if b, ok := r.extensions()[4]; ok {
			u.fastStart = fastStartList(b)
		}
	}
}

func decodeRelease(r *reader, u *uuie) {
	ext := r.bit()
	hasReason := r.bit()
	r.oid()
	if hasReason {
		idx, e := r.choice(12, true)
		if e {
			r.openType()
			idx = 100 + idx
		}
		u.reason = idx
	}
	if ext {
		r.extensions()
	}
}

// Release reasons (ReleaseCompleteReason root alternatives).
const (
	reasonNoBandwidth          = 0
	reasonUnreachable          = 2
	reasonDestinationRejection = 3
	reasonNoPermission         = 5
	reasonGatewayResources     = 7
	reasonBadFormatAddress     = 8
	reasonAdaptiveBusy         = 9
	reasonInConf               = 10
	reasonUndefined            = 11
)

// Encoding.

// vendor is BitVoice's endpoint description: a gateway, from an unregistered vendor code.
func writeEndpoint(w *writer) {
	w.bit(false)               // no extensions
	w.bits(0b010100, 6)        // vendor, gateway
	w.bit(false)               // VendorIdentifier: no extensions
	w.bits(0b11, 2)            // productId, versionId
	w.bit(false)               // H221NonStandard: no extensions
	w.constrained(181, 0, 255) // United States
	w.constrained(0, 0, 255)
	w.constrained(0, 0, 65535)
	w.octetString([]byte("BitVoice"), 1, 256)
	w.octetString([]byte("1"), 1, 256)
	w.bit(false) // GatewayInfo: no extensions
	w.bits(0b10, 2)
	w.length(1)
	w.choiceIndex(5, 9, true) // h323 caps
	w.bit(false)
	w.bit(false)
	w.bit(false) // mc
	w.bit(false) // undefinedNode
}

func (w *writer) choiceIndex(i, n int, extensible bool) {
	if extensible {
		w.bit(false)
	}
	w.constrained(int64(i), 0, int64(n-1))
}

func writeAliasDigits(w *writer, s string) {
	w.choiceIndex(0, 2, true)
	w.digits(s, 1, 128)
}

func writeAliasID(w *writer, s string) {
	w.choiceIndex(1, 2, true)
	w.bmpString(s, 1, 256)
}

func writeTransport(w *writer, a netip.AddrPort) {
	w.choiceIndex(0, 7, true)
	ip := a.Addr().Unmap().As4()
	w.octetString(ip[:], 4, 4)
	w.constrained(int64(a.Port()), 0, 65535)
}

func onlyDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] == '*' || s[i] == '#' || s[i] == ',') {
			return false
		}
	}
	return s != ""
}

func writeAliases(w *writer, as []string) {
	w.length(len(as))
	for _, a := range as {
		if onlyDigits(a) && len(a) <= 128 {
			writeAliasDigits(w, a)
		} else {
			writeAliasID(w, a)
		}
	}
}

func writeFastStart(olcs [][]byte) []byte {
	return sub(func(w *writer) {
		w.length(len(olcs))
		for _, o := range olcs {
			w.octetString(o, 0, -1)
		}
	})
}

func writeCallID(id []byte) []byte {
	return sub(func(w *writer) {
		w.bit(false)
		w.octetString(id, 16, 16)
	})
}

// encodeUU builds H323-UserInformation for a message body. Setup carries the aliases, conference and
// call identifiers and fastStart; the answers carry fastStart; ReleaseComplete its reason.
func encodeUU(u *uuie) []byte {
	w := &writer{}
	w.bit(false) // H323-UserInformation: no extensions
	w.bit(false) // no user-data
	w.bit(true)  // H323-UU-PDU: extensions follow (h245Tunnelling false)
	w.bit(false) // no nonStandardData
	w.choiceIndex(u.body, bodyCount, true)
	switch u.body {
	case bodySetup:
		w.bit(true) // extensions
		var opt uint64
		if len(u.callerAlias) > 0 {
			opt |= 0x20
		}
		if len(u.calledAlias) > 0 {
			opt |= 0x10
		}
		w.bits(opt, 7)
		w.oid(protocolID)
		if opt&0x20 != 0 {
			writeAliases(w, u.callerAlias)
		}
		writeEndpoint(w)
		if opt&0x10 != 0 {
			writeAliases(w, u.calledAlias)
		}
		w.bit(false) // activeMC
		w.octetString(u.confID, 16, 16)
		w.choiceIndex(0, 3, true) // create
		w.choiceIndex(0, 4, true) // pointToPoint
		adds := map[int][]byte{2: writeCallID(u.callID), 7: sub(func(w *writer) { w.bit(false) }), 8: sub(func(w *writer) { w.bit(false) }),
			10: sub(func(w *writer) { w.bit(false) }), 11: sub(func(w *writer) { w.bit(false) })}
		if len(u.fastStart) > 0 {
			adds[6] = writeFastStart(u.fastStart)
		}
		w.extensions(12, adds) // up to maintainConnection
	case bodyCallProceeding, bodyAlerting:
		w.bit(true)
		w.bit(false) // no h245Address
		w.oid(protocolID)
		writeEndpoint(w)
		adds := map[int][]byte{0: writeCallID(u.callID), 5: sub(func(w *writer) { w.bit(false) }), 6: sub(func(w *writer) { w.bit(false) })}
		if len(u.fastStart) > 0 {
			adds[4] = writeFastStart(u.fastStart)
		}
		w.extensions(7, adds)
	case bodyConnect:
		w.bit(true)
		w.bit(false)
		w.oid(protocolID)
		writeEndpoint(w)
		w.octetString(u.confID, 16, 16)
		adds := map[int][]byte{0: writeCallID(u.callID), 5: sub(func(w *writer) { w.bit(false) }), 6: sub(func(w *writer) { w.bit(false) })}
		if len(u.fastStart) > 0 {
			adds[4] = writeFastStart(u.fastStart)
		}
		w.extensions(7, adds)
	case bodyReleaseComplete:
		w.bit(true)
		w.bit(u.reason >= 0)
		w.oid(protocolID)
		if u.reason >= 0 {
			w.choiceIndex(u.reason, 12, true)
		}
		w.extensions(1, map[int][]byte{0: writeCallID(u.callID)})
	}
	// H323-UU-PDU additions: h245Tunnelling (addition 1) false.
	w.extensions(2, map[int][]byte{1: sub(func(w *writer) { w.bit(false) })})
	return w.b
}
