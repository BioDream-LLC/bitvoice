package h323

import "net/netip"

// H.245 OpenLogicalChannel, as Fast Connect carries it in fastStart: G.711 audio over RTP (H.225.0
// logical channel parameters). Anything else in a proposal makes it one BitVoice does not take.

// Audio codecs of AudioCapability BitVoice proposes and takes.
const (
	g711Alaw64k = 1
	g711Ulaw64k = 3
)

// olc is one logical channel: the direction BitVoice cares about, the codec, and the addresses.
type olc struct {
	number  int
	reverse bool // the parameters are the reverse ones: media from the proposer's peer to the proposer
	codec   int  // g711Ulaw64k or g711Alaw64k
	frames  int  // packet size in ms (the G.711 capability value)
	session int
	media   netip.AddrPort // mediaChannel: where RTP goes (absent in a proposal of what the proposer sends)
	control netip.AddrPort // mediaControlChannel: RTCP
}

// h245NonStandard reads H.245's NonStandardParameter (not extensible).
func h245NonStandard(r *reader) {
	if idx, _ := r.choice(2, false); idx == 0 {
		r.oid()
	} else {
		r.constrained(0, 255)
		r.constrained(0, 255)
		r.constrained(0, 65535)
	}
	r.octetString(0, -1)
}

// audioData reads DataType (only nullData and G.711 audio are taken): codec 0 for nullData.
func dataType(r *reader) (codec, frames int) {
	idx, ext := r.choice(6, true)
	if ext {
		fail("data type not taken")
	}
	switch idx {
	case 1:
		return 0, 0
	case 3:
		a, aext := r.choice(14, true)
		if aext || (a != g711Alaw64k && a != g711Ulaw64k) {
			fail("codec not taken")
		}
		return a, int(r.constrained(1, 256))
	}
	fail("data type not taken")
	return 0, 0
}

// h245Address reads H.245's TransportAddress, giving the unicast IPv4 or IPv6 address and port.
func h245Address(r *reader) netip.AddrPort {
	top, ext := r.choice(2, true)
	if ext || top != 0 {
		fail("address not taken")
	}
	u, uext := r.choice(5, true)
	if uext {
		fail("address not taken")
	}
	switch u {
	case 0:
		sext := r.bit()
		ip := r.octetString(4, 4)
		port := r.constrained(0, 65535)
		if sext {
			r.extensions()
		}
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(ip)), uint16(port))
	case 2:
		sext := r.bit()
		ip := r.octetString(16, 16)
		port := r.constrained(0, 65535)
		if sext {
			r.extensions()
		}
		return netip.AddrPortFrom(netip.AddrFrom16([16]byte(ip)).Unmap(), uint16(port))
	}
	fail("address not taken")
	return netip.AddrPort{}
}

func h2250Params(b []byte, o *olc) {
	r := &reader{b: b}
	ext := r.bit()
	opt := r.bits(10)
	if opt&0x200 != 0 {
		for n := r.count(); n > 0; n-- {
			h245NonStandard(r)
		}
	}
	o.session = int(r.constrained(0, 255))
	if opt&0x100 != 0 {
		r.constrained(1, 255)
	}
	if opt&0x80 != 0 {
		o.media = h245Address(r)
	}
	if opt&0x40 != 0 {
		r.bit()
	}
	if opt&0x20 != 0 {
		o.control = h245Address(r)
	}
	// The rest (guaranteed delivery, silence suppression, terminal label, payload type, packetization)
	// is not needed.
	_ = ext
}

// decodeOLC reads a Fast Connect proposal or answer.
func decodeOLC(b []byte) (o *olc, err error) {
	defer catch(&err)
	r := &reader{b: b}
	o = &olc{}
	ext := r.bit()
	hasReverse := r.bit()
	o.number = int(r.constrained(1, 65535))
	// forwardLogicalChannelParameters
	fext := r.bit()
	if r.bit() {
		r.constrained(0, 65535) // portNumber
	}
	fc, ff := dataType(r)
	mi, mext := r.choice(3, true)
	var fwdParams []byte
	switch {
	case mext && mi == 0:
		fwdParams = r.openType()
	case mext && mi == 1: // none
		r.openType()
	default:
		fail("multiplex not taken")
	}
	if fext {
		r.extensions()
	}
	if fc != 0 {
		o.codec, o.frames = fc, ff
		if fwdParams == nil {
			fail("no H.225.0 parameters")
		}
		h2250Params(fwdParams, o)
	}
	if hasReverse {
		rext := r.bit()
		hasMux := r.bit()
		rc, rf := dataType(r)
		var revParams []byte
		if hasMux {
			ri, re := r.choice(2, true)
			if !re || ri != 0 {
				fail("multiplex not taken")
			}
			revParams = r.openType()
		}
		if rext {
			r.extensions()
		}
		if fc == 0 && rc != 0 {
			o.reverse, o.codec, o.frames = true, rc, rf
			if revParams == nil {
				fail("no H.225.0 parameters")
			}
			h2250Params(revParams, o)
		}
	}
	if o.codec == 0 {
		fail("no audio")
	}
	if ext {
		r.extensions()
	}
	return o, nil
}

func writeH245Address(w *writer, a netip.AddrPort) {
	w.choiceIndex(0, 2, true) // unicastAddress
	w.choiceIndex(0, 5, true) // iPAddress
	w.bit(false)
	ip := a.Addr().Unmap().As4()
	w.octetString(ip[:], 4, 4)
	w.constrained(int64(a.Port()), 0, 65535)
}

func writeH2250(o *olc) []byte {
	return sub(func(w *writer) {
		w.bit(false)
		var opt uint64
		if o.media.IsValid() {
			opt |= 0x80
		}
		if o.control.IsValid() {
			opt |= 0x20
		}
		w.bits(opt, 10)
		w.constrained(int64(o.session), 0, 255)
		if o.media.IsValid() {
			writeH245Address(w, o.media)
		}
		if o.control.IsValid() {
			writeH245Address(w, o.control)
		}
	})
}

func writeAudio(w *writer, codec, frames int) {
	w.choiceIndex(3, 6, true) // audioData
	w.choiceIndex(codec, 14, true)
	w.constrained(int64(frames), 1, 256)
}

// encodeOLC writes a channel: forward (what the proposer sends) or reverse (what it receives).
func encodeOLC(o *olc) []byte {
	return sub(func(w *writer) {
		w.bit(false)
		w.bit(o.reverse)
		w.constrained(int64(o.number), 1, 65535)
		w.bit(false) // forward parameters: no extensions
		w.bit(false) // no portNumber
		if o.reverse {
			w.choiceIndex(1, 6, true) // nullData
			w.bit(true)               // multiplexParameters: extension alternative
			w.smallNumber(1)          // none
			w.openType(nil)
		} else {
			writeAudio(w, o.codec, o.frames)
			w.bit(true)
			w.smallNumber(0) // h2250LogicalChannelParameters
			w.openType(writeH2250(o))
		}
		if o.reverse {
			w.bit(false) // reverse parameters: no extensions
			w.bit(true)  // multiplexParameters present
			writeAudio(w, o.codec, o.frames)
			w.bit(true)
			w.smallNumber(0)
			w.openType(writeH2250(o))
		}
	})
}
