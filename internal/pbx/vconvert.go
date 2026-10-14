package pbx

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// Video conversion: when two parties have no video codec in common (a desk phone with H.264, a
// browser with only VP8), BitVoice converts between H.264 and VP8 with ffmpeg, a frame at a time,
// when Settings allow it and ffmpeg is installed. Offers carry the other codec as well, an answer
// the caller cannot use is answered in the caller's own codec, and the call's video runs through a
// converter each way.

func (e *Engine) videoTranscodeOn() bool {
	c := e.Config()
	return !c.System.VideoOff && c.System.VideoTranscode && media.FFmpegHas(c.System.FFmpeg)
}

// offerVideoAlternatives adds to a video offer the convertible codecs it lacks, under free numbers.
func offerVideoAlternatives(m *sdp.Media) {
	if m == nil || m.Port == 0 {
		return
	}
	have := map[string]bool{}
	used := map[int]bool{}
	convertible := false
	for _, c := range m.Codecs() {
		have[strings.ToUpper(c.Name)] = true
		used[c.PT] = true
		convertible = convertible || media.CanTranscodeVideo(c.Name)
	}
	if !convertible {
		return
	}
	for _, f := range m.Formats {
		if n, err := strconv.Atoi(f); err == nil {
			used[n] = true
		}
	}
	for _, name := range media.VideoTranscodable {
		if have[name] {
			continue
		}
		pt := freeDynamic(used)
		if pt < 0 {
			return
		}
		used[pt] = true
		p := strconv.Itoa(pt)
		m.Formats = append(m.Formats, p)
		m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "rtpmap:" + p + " " + name + "/90000"})
		if name == "H264" {
			m.Lines = append(m.Lines, sdp.Line{Type: 'a', Value: "fmtp:" + p + " profile-level-id=42e01f;packetization-mode=1"})
		}
	}
}

// chosenVideo is the video codec a leg sends and receives: the first in the answer it agreed.
func (l *Leg) chosenVideo() (sdp.Codec, bool) {
	l.mu.Lock()
	s := l.remoteSDP
	if l.inbound {
		s = l.localSDP // the party offered; BitVoice's answer says which codec
	}
	l.mu.Unlock()
	m := videoMedia(s)
	if m == nil {
		return sdp.Codec{}, false
	}
	for _, c := range m.Codecs() {
		if !isAux(c.Name) && !strings.EqualFold(c.Name, "rtx") && !strings.EqualFold(c.Name, "red") && !strings.EqualFold(c.Name, "ulpfec") {
			return c, true
		}
	}
	return sdp.Codec{}, false
}

// fitVideoAnswer makes the video of an answer to a party use a codec the party offered: when the
// answer's codec is not among the offer's, the offer's first convertible codec is answered instead
// (BitVoice converts). It reports whether it changed the answer.
func fitVideoAnswer(ans, offer *sdp.Media) bool {
	if ans == nil || offer == nil || ans.Port == 0 {
		return false
	}
	offered := map[string]sdp.Codec{}
	for _, c := range offer.Codecs() {
		if _, ok := offered[strings.ToUpper(c.Name)]; !ok {
			offered[strings.ToUpper(c.Name)] = c
		}
	}
	for _, c := range ans.Codecs() {
		if _, ok := offered[strings.ToUpper(c.Name)]; ok {
			return false // a codec in common: passed as it is
		}
	}
	for _, c := range offer.Codecs() {
		if media.CanTranscodeVideo(c.Name) {
			p := strconv.Itoa(c.PT)
			ans.Formats = []string{p}
			lines := slices.DeleteFunc(slices.Clone(ans.Lines), func(l sdp.Line) bool {
				return strings.HasPrefix(l.Value, "rtpmap:") || strings.HasPrefix(l.Value, "fmtp:") || strings.HasPrefix(l.Value, "rtcp-fb:")
			})
			lines = append(lines, sdp.Line{Type: 'a', Value: "rtpmap:" + p + " " + c.Name + "/90000"})
			if c.Fmtp != "" {
				lines = append(lines, sdp.Line{Type: 'a', Value: "fmtp:" + p + " " + c.Fmtp})
			}
			ans.Lines = lines
			return true
		}
	}
	return false
}

// videoConverters are the two converters of a call whose parties use different video codecs.
type videoConverters struct {
	cancel context.CancelFunc
	ab, ba *media.VideoTranscoder
	a, b   *Leg
}

func (v *videoConverters) stop() {
	if v == nil {
		return
	}
	v.cancel()
	if v.ab != nil {
		v.ab.Close()
	}
	if v.ba != nil {
		v.ba.Close()
	}
}

// convertVideo joins two legs' video through converters when their codecs differ and both can be
// converted; it reports whether it did (false: connect them as they are).
func (c *Call) convertVideo(a, b *Leg) bool {
	if c == nil || !c.e.videoTranscodeOn() {
		return false
	}
	ca, okA := a.chosenVideo()
	cb, okB := b.chosenVideo()
	if !okA || !okB || strings.EqualFold(ca.Name, cb.Name) || !media.CanTranscodeVideo(ca.Name) || !media.CanTranscodeVideo(cb.Name) {
		return false
	}
	av, bv := a.videoEP(), b.videoEP()
	ctx, cancel := context.WithCancel(c.e.ctx)
	ff := c.e.Config().System.FFmpeg
	from := func(x sdp.Codec) media.VideoCodec { return media.VideoCodec{Name: x.Name, PT: x.PT, Fmtp: x.Fmtp} }
	ab, err1 := media.StartVideoTranscoder(ctx, ff, from(ca), from(cb), bv.SendRTP)
	ba, err2 := media.StartVideoTranscoder(ctx, ff, from(cb), from(ca), av.SendRTP)
	if err1 != nil || err2 != nil {
		cancel()
		if ab != nil {
			ab.Close()
		}
		if ba != nil {
			ba.Close()
		}
		c.e.Log.Warn("video not converted", "err", firstErr(err1, err2))
		return false
	}
	media.Connect(av, nil)
	media.Connect(bv, nil)
	av.SetTap(ab.Write)
	bv.SetTap(ba.Write)
	c.vmu.Lock()
	c.vconv.stop()
	c.vconv = &videoConverters{cancel: cancel, ab: ab, ba: ba, a: a, b: b}
	c.vmu.Unlock()
	c.e.Log.Info("converting video", "call", c.ID, "from", ca.Name, "to", cb.Name)
	return true
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
