package pbx

import (
	"bytes"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/srtp"
)

// Video passes between two parties that both offer it, as it is: BitVoice anchors it on a port of its
// own per leg (so phones never learn each other's addresses), relays RTP and RTCP (so a phone's
// request for a fresh picture reaches the sender), maps payload type numbers when the two chose
// different ones for the same codec, and encrypts each leg as that leg negotiated. It never decodes
// or converts video; a party without video simply gets none.

// videoMedia is the first video stream of a description that is not refused.
func videoMedia(s *sdp.Session) *sdp.Media {
	if s == nil {
		return nil
	}
	for _, m := range s.Media {
		if m.Type == "video" && m.Port != 0 {
			return m
		}
	}
	return nil
}

func (e *Engine) videoAllowed() bool { return !e.Config().System.VideoOff }

// videoEndpoint is this leg's video port, opened on first use.
func (l *Leg) videoEndpoint() *media.Endpoint {
	l.mu.Lock()
	v := l.video
	ended := l.ended
	l.mu.Unlock()
	if v != nil || ended || l.e.pool == nil {
		return v
	}
	host := ""
	if l.peer.Local.IsValid() {
		host = l.peer.Local.String()
	}
	ep, err := l.e.pool.AllocateOn(host)
	if err != nil {
		l.e.Log.Warn("no port for video", "err", err)
		return nil
	}
	l.mu.Lock()
	if l.video != nil || l.ended {
		l.mu.Unlock()
		ep.Close()
		return l.video
	}
	l.video = ep
	remote := l.remoteSDP
	l.mu.Unlock()
	if m := videoMedia(remote); m != nil {
		ep.SetRemote(remote.Connection(m), m.Port)
	}
	ep.Start()
	return ep
}

// describeVideo fills in the video stream of a description going to this party: this leg's port and
// keys. answering is the party's own offer when this description answers it. It reports whether the
// stream is carried; when not, it is refused (port 0).
func (l *Leg) describeVideo(m *sdp.Media, answering *sdp.Media, audioSecure bool) bool {
	if !l.e.videoAllowed() || m.Port == 0 || (answering != nil && answering.Port == 0) {
		m.Port = 0
		return false
	}
	ep := l.videoEndpoint()
	if ep == nil {
		m.Port = 0
		return false
	}
	m.Port = ep.Port()
	l.mu.Lock()
	defer l.mu.Unlock()
	if answering != nil {
		if !answering.Secure() {
			m.Proto = strings.Replace(answering.Proto, "SAVP", "AVP", 1)
			m.SetCrypto(nil)
			l.setVideoKeys(nil, "", nil)
			return true
		}
		c, ok := supportedCrypto(answering)
		if !ok {
			m.Port = 0
			return false
		}
		if l.vLocalKey == nil || l.vSuite != c.Suite {
			l.vLocalKey = newMasterKey()
		}
		m.Proto = answering.Proto
		m.SetCrypto([]sdp.Crypto{{Tag: c.Tag, Suite: c.Suite, Key: l.vLocalKey}})
		l.setVideoKeys(c.Key, c.Suite, l.vLocalKey)
		return true
	}
	// Offering: encrypted when this leg's sound is.
	if !audioSecure {
		m.Proto = strings.Replace(m.Proto, "SAVP", "AVP", 1)
		m.SetCrypto(nil)
		return true
	}
	if l.vLocalKey == nil {
		l.vLocalKey, l.vSuite = newMasterKey(), srtp.AES128SHA80
	}
	if !strings.Contains(m.Proto, "SAVP") {
		m.Proto = strings.Replace(m.Proto, "AVP", "SAVP", 1)
	}
	m.SetCrypto([]sdp.Crypto{{Tag: 1, Suite: srtp.AES128SHA80, Key: l.vLocalKey}})
	return true
}

// setVideoKeys installs the video stream's SRTP contexts when the keys change. Called with l.mu held.
func (l *Leg) setVideoKeys(remote []byte, suite string, local []byte) {
	if l.video == nil {
		return
	}
	if remote == nil {
		if l.vRemoteKey != nil {
			l.video.SetSRTP(nil, nil)
		}
		l.vRemoteKey = nil
		return
	}
	if bytes.Equal(remote, l.vRemoteKey) && suite == l.vSuite && l.video.Secure() {
		return
	}
	in, err1 := srtp.New(suite, remote[:16], remote[16:])
	out, err2 := srtp.New(suite, local[:16], local[16:])
	if err1 != nil || err2 != nil {
		return
	}
	l.vRemoteKey, l.vSuite = remote, suite
	l.video.SetSRTP(in, out)
}

// videoRemote takes the party's video address from its description, and its keys when the
// description answers BitVoice's offer.
func (l *Leg) videoRemote(s *sdp.Session, isAnswer bool) {
	m := videoMedia(s)
	l.mu.Lock()
	v := l.video
	l.mu.Unlock()
	if v == nil {
		return
	}
	if m == nil {
		media.Connect(v, nil) // the party stopped its video
		return
	}
	v.SetRemote(s.Connection(m), m.Port)
	if !isAnswer {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !m.Secure() || l.vLocalKey == nil {
		l.setVideoKeys(nil, "", nil)
		return
	}
	if c, ok := supportedCrypto(m); ok {
		l.setVideoKeys(c.Key, c.Suite, l.vLocalKey)
	}
}

// connect joins two legs' sound, and their video when both carry it; b nil leaves a alone (hold,
// music, a prompt).
func connect(a, b *Leg) {
	stopVideoConversion(a)
	if b == nil {
		media.Connect(a.ep, nil)
		if v := a.videoEP(); v != nil {
			media.Connect(v, nil)
		}
		return
	}
	stopVideoConversion(b)
	media.Connect(a.ep, b.ep)
	av, bv := a.videoEP(), b.videoEP()
	switch {
	case av != nil && bv != nil:
		if a.call != nil && a.call.convertVideo(a, b) {
			break
		}
		media.Connect(av, bv)
		mapVideoTypes(a, b)
	case av != nil:
		media.Connect(av, nil)
	case bv != nil:
		media.Connect(bv, nil)
	}
}

// stopVideoConversion stops the converters of a leg's call and takes their taps off its video.
func stopVideoConversion(l *Leg) {
	if l == nil || l.call == nil {
		return
	}
	c := l.call
	c.vmu.Lock()
	v := c.vconv
	c.vconv = nil
	c.vmu.Unlock()
	if v == nil {
		return
	}
	v.stop()
	for _, x := range []*Leg{v.a, v.b} {
		if ep := x.videoEP(); ep != nil {
			ep.SetTap(nil)
		}
	}
}

func (l *Leg) videoEP() *media.Endpoint {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.video
}

// mapVideoTypes renumbers video payload types where the two parties chose different numbers for
// the same codec (H.264 as 96 on one phone and 99 on the other).
func mapVideoTypes(a, b *Leg) {
	pts := func(l *Leg) map[string]int {
		l.mu.Lock()
		s := l.remoteSDP
		l.mu.Unlock()
		out := map[string]int{}
		if m := videoMedia(s); m != nil {
			for _, c := range m.Codecs() {
				k := strings.ToUpper(c.Name)
				if _, seen := out[k]; !seen {
					out[k] = c.PT
				}
			}
		}
		return out
	}
	ap, bp := pts(a), pts(b)
	av, bv := a.videoEP(), b.videoEP()
	for name, x := range ap {
		if y, ok := bp[name]; ok && x != y {
			bv.MapPayloadType(x, y) // what a sends, b receives as its own number
			av.MapPayloadType(y, x)
		}
	}
}

func (l *Leg) closeVideo() {
	l.mu.Lock()
	v := l.video
	l.video = nil
	l.mu.Unlock()
	if v != nil {
		v.Close()
	}
}

// videoUpgrade handles a party switching its camera on during a call: the other party is offered
// the video first, and only if it takes it does this party's re-INVITE get video in its answer
// (answerFor carries video once this leg has a video port). Called before the answer is built.
func (l *Leg) videoUpgrade(offer *sdp.Session) {
	want := videoMedia(offer)
	if want == nil || !l.e.videoAllowed() || l.videoEP() != nil || l.call == nil {
		return
	}
	other := l.call.other(l)
	if other == nil || other.videoEP() != nil {
		return
	}
	other.mu.Lock()
	base, ok := other.localSDP, other.answered && !other.ended && !other.onHold
	other.mu.Unlock()
	l.mu.Lock()
	ok = ok && !l.onHold
	l.mu.Unlock()
	if !ok || base == nil {
		return
	}
	o := base.Clone()
	vm := &sdp.Media{Type: want.Type, Port: want.Port, Proto: want.Proto, Formats: append([]string(nil), want.Formats...),
		Lines: append([]sdp.Line(nil), want.Lines...)}
	placed := false
	for i, m := range o.Media {
		if m.Type == "video" { // a refused video line is reused in place (RFC 3264 8.4)
			o.Media[i], placed = vm, true
			break
		}
	}
	if !placed {
		o.Media = append(o.Media, vm)
	}
	if !other.describeVideo(vm, nil, o.Audio() != nil && o.Audio().Secure()) {
		return
	}
	o.Lines = replaceOrigin(o.Lines, base)
	ans, err := other.reinvite(o)
	if err != nil || videoMedia(ans) == nil {
		other.closeVideo() // the other party has no video, or refused it: this party stays on sound
		vm.Port = 0
		if err == nil {
			other.setRemoteSDP(ans)
			other.mu.Lock()
			other.localSDP = o
			other.mu.Unlock()
		}
		return
	}
	other.setRemoteSDP(ans)
	other.answeredCrypto(ans)
	other.mu.Lock()
	other.localSDP = o
	other.mu.Unlock()
	if l.videoEndpoint() != nil {
		connect(l, other)
	}
}
