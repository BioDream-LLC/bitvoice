package pbx

import (
	"slices"
	"strconv"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// codecsFor is the codec list, best first, for calls to a trunk (nil: a phone).
func (e *Engine) codecsFor(trunk *config.Trunk) []string {
	if trunk != nil && len(trunk.Codecs) > 0 {
		return trunk.Codecs
	}
	if cs := e.Config().System.Codecs; len(cs) > 0 {
		return cs
	}
	return config.DefaultCodecs
}

func isAux(name string) bool {
	n := strings.ToLower(name)
	return n == "telephone-event" || n == "cn"
}

// shapeOffer makes an offer passed on from one party list only the codecs the next party may use, in
// that party's order. Codecs the first party offered keep their payload numbers (so Opus or G.729 can be
// carried untouched); PCMU, PCMA and G.722 are added when the first party offered any of them, because
// BitVoice converts between those. With nothing usable the offer is left alone for the far end to refuse.
func shapeOffer(m *sdp.Media, allowed []string) {
	if m == nil {
		return
	}
	offered := m.Codecs()
	byName := map[string]sdp.Codec{}
	used := map[int]bool{}
	convertible := false
	for _, c := range offered {
		used[c.PT] = true
		n := strings.ToUpper(c.Name)
		if _, ok := byName[n]; !ok && !isAux(n) {
			byName[n] = c
		}
		convertible = convertible || media.Transcodable(media.CodecPT(n))
	}
	var keep []sdp.Codec
	for _, name := range allowed {
		if c, ok := byName[name]; ok {
			keep = append(keep, c)
		} else if pt := media.CodecPT(name); convertible && pt >= 0 {
			if media.Dynamic(pt) && used[pt] {
				pt = freeDynamic(used)
			}
			if pt >= 0 && !used[pt] {
				used[pt] = true
				keep = append(keep, codecNamed(name, pt))
			}
		}
	}
	if len(keep) == 0 {
		return
	}
	var pts []string
	for _, c := range keep {
		pt := strconv.Itoa(c.PT)
		pts = append(pts, pt)
		if !slices.Contains(m.Formats, pt) {
			m.Formats = append(m.Formats, pt)
			m.Lines = append(c.Attributes(), m.Lines...)
		}
	}
	// Key presses at each clock the kept codecs use (Opus runs at 48000, the rest at 8000): BitVoice
	// re-times them between the two when it converts.
	have := map[int]bool{}
	for _, c := range offered {
		if strings.EqualFold(c.Name, "telephone-event") {
			have[c.ClockRate] = true
		}
	}
	if len(have) > 0 {
		for _, c := range keep {
			clock := eventClock(c.Name)
			if have[clock] {
				continue
			}
			pt := freeDynamic(used)
			if pt < 0 {
				break
			}
			used[pt], have[clock] = true, true
			n := strconv.Itoa(pt)
			m.Formats = append(m.Formats, n)
			m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "rtpmap:" + n + " telephone-event/" + strconv.Itoa(clock)}, sdp.Line{Type: 'a', Value: "fmtp:" + n + " 0-16"})
		}
	}
	filterFormats(m, pts)
	// Key presses only at the clocks the kept codecs run at: a browser's 48 kHz telephone-event means
	// nothing to a trunk that is not offered Opus.
	clocks := map[int]bool{}
	for _, c := range keep {
		clocks[eventClock(c.Name)] = true
	}
	var drop []string
	for _, c := range m.Codecs() {
		if strings.EqualFold(c.Name, "telephone-event") && !clocks[c.ClockRate] {
			drop = append(drop, strconv.Itoa(c.PT))
		}
	}
	removeFormats(m, drop)
	// Our order first, then telephone-event and comfort noise.
	rest := slices.DeleteFunc(slices.Clone(m.Formats), func(f string) bool { return slices.Contains(pts, f) })
	m.Formats = append(pts, rest...)
}

// fitAnswer makes the answer BitVoice sends a caller use a codec the caller offered. When the other party
// chose one the caller did not offer, the caller gets its own first convertible codec and BitVoice
// converts in between. It returns false when there is no such codec.
func fitAnswer(ans, offer *sdp.Media) bool {
	if ans == nil || offer == nil {
		return true
	}
	cs := ans.Codecs()
	var first *sdp.Codec
	for i := range cs {
		if !isAux(cs[i].Name) {
			first = &cs[i]
			break
		}
	}
	for _, c := range offer.Codecs() {
		if first != nil && strings.EqualFold(c.Name, first.Name) {
			return true
		}
	}
	for _, c := range offer.Codecs() {
		if !media.Transcodable(media.CodecPT(strings.ToUpper(c.Name))) {
			continue
		}
		pt := strconv.Itoa(c.PT)
		if !slices.Contains(ans.Formats, pt) {
			ans.Formats = append([]string{pt}, ans.Formats...)
			named := codecNamed(strings.ToUpper(c.Name), c.PT)
			named.Fmtp = c.Fmtp // AMR's answer keeps the framing and modes offered
			ans.Lines = append(named.Attributes(), ans.Lines...)
		}
		filterFormats(ans, []string{pt})
		return true
	}
	return false
}

// sendCodec sets the payload type this leg is sent from the description it agreed (the first codec).
func (l *Leg) sendCodec(s *sdp.Session) {
	if s == nil || s.Audio() == nil || l.ep == nil {
		return
	}
	applyCodec(l.ep, s.Audio().Codecs())
}

// applyCodec tells a party's endpoint the codec it speaks (the first audio codec of what it agreed),
// its number for Opus and its number for key presses.
func applyCodec(ep *media.Endpoint, cs []sdp.Codec) {
	for _, c := range cs {
		if !isAux(c.Name) {
			pt := media.CodecPTFmtp(c.Name, c.Fmtp)
			ep.SetFmtp(c.Fmtp)
			ep.SetPayloadType(pt)
			ep.SetEventPT(eventFor(cs, c.ClockRate))
			if media.Dynamic(pt) {
				// Opus and G.726 have dynamic numbers: this party's comes in as BitVoice's own and goes
				// back out as its own.
				ep.MapIncoming(c.PT, pt)
				ep.MapPayloadType(pt, c.PT)
			}
			return
		}
	}
}

// eventFor is the telephone-event number among cs at a clock rate (8000 when there is none at it), or -1.
func eventFor(cs []sdp.Codec, clock int) int {
	if clock != 48000 {
		clock = 8000
	}
	best := -1
	for _, c := range cs {
		if strings.EqualFold(c.Name, "telephone-event") {
			if c.ClockRate == clock {
				return c.PT
			}
			if c.ClockRate == 8000 {
				best = c.PT
			}
		}
	}
	return best
}

// codecNamed is the description of a codec BitVoice speaks under a payload number.
func codecNamed(name string, pt int) sdp.Codec {
	if name == "OPUS" {
		return sdp.Codec{PT: pt, Name: "opus", ClockRate: 48000, Channels: 2}
	}
	return sdp.Codec{PT: pt, Name: name, ClockRate: 8000}
}

// freeDynamic is a dynamic payload number (96-127) not yet used, or -1.
func freeDynamic(used map[int]bool) int {
	for pt := 96; pt <= 127; pt++ {
		if !used[pt] && pt != 101 {
			return pt
		}
	}
	return -1
}

// codecsForKind narrows a codec list for what is connected: analog adapters get G.711 only, which faxes,
// modems and alarm panels need to get through.
func codecsForKind(list []string, kind string) []string {
	if kind != "analog" {
		return list
	}
	var out []string
	for _, c := range list {
		if c == "PCMU" || c == "PCMA" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return []string{"PCMU", "PCMA"}
	}
	return out
}

// eventClock is the clock key presses travel at beside a codec (RFC 4733): Opus's 48000, else 8000
// (G.722 included, whose RTP clock is 8000 by RFC 3551).
func eventClock(codec string) int {
	if strings.EqualFold(codec, "opus") {
		return 48000
	}
	return 8000
}

// removeFormats takes payload types out of a media description, with their rtpmap and fmtp lines.
func removeFormats(m *sdp.Media, pts []string) {
	m.Formats = slices.DeleteFunc(m.Formats, func(f string) bool { return slices.Contains(pts, f) })
	m.Lines = slices.DeleteFunc(m.Lines, func(l sdp.Line) bool {
		if l.Type != 'a' || !(strings.HasPrefix(l.Value, "rtpmap:") || strings.HasPrefix(l.Value, "fmtp:")) {
			return false
		}
		pt, _, _ := strings.Cut(l.Value[strings.IndexByte(l.Value, ':')+1:], " ")
		return slices.Contains(pts, pt)
	})
}
