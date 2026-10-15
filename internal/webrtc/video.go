package webrtc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/dtls"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// Video sessions: a browser sending its camera (WHIP, RFC 9725: a caller's phone sharing video
// with a 911 call-taker) or watching one (WHEP: the call-taker, CAD, units). One video stream on one
// bundled transport, BitVoice ICE-lite and the DTLS server, as for audio.

// ErrNoVideo is returned when an offer has no video BitVoice can take.
var ErrNoVideo = errors.New("webrtc: the offer has no H.264 or VP8 video over UDP/TLS/RTP/SAVPF")

// VideoCodec is the codec a video session uses: its name (H264 or VP8), the browser's payload
// type for it, and its fmtp line (H.264's profile and packetization mode).
type VideoCodec struct {
	Name string
	PT   int
	Fmtp string
}

// pickVideo chooses the first offered codec of want (in want's order): H.264 only in
// packetization mode 1 (FU-A, as every browser sends it), and only the constrained baseline or
// baseline profile every browser can decode.
func pickVideo(m *sdp.Media, want []string) (VideoCodec, bool) {
	fmtps := map[int]string{}
	for _, l := range m.Lines {
		if l.Type == 'a' && strings.HasPrefix(l.Value, "fmtp:") {
			pt, rest, _ := strings.Cut(strings.TrimPrefix(l.Value, "fmtp:"), " ")
			var n int
			if _, err := fmt.Sscan(pt, &n); err == nil {
				fmtps[n] = rest
			}
		}
	}
	for _, w := range want {
		for _, c := range m.Codecs() {
			if !strings.EqualFold(c.Name, w) || c.ClockRate != 90000 {
				continue
			}
			f := fmtps[c.PT]
			if strings.EqualFold(w, "H264") {
				pl := strings.ToLower(f)
				if !strings.Contains(pl, "packetization-mode=1") || !(strings.Contains(pl, "profile-level-id=42e0") || strings.Contains(pl, "profile-level-id=4200")) {
					continue
				}
			}
			return VideoCodec{Name: strings.ToUpper(w), PT: c.PT, Fmtp: f}, true
		}
	}
	return VideoCodec{}, false
}

// AnswerVideo answers a browser's video offer on ep: it receives (WHIP) when send is false, and
// sends (WHEP) when true, with a codec of want. It returns the session, the answer and the codec.
func AnswerVideo(ep *media.Endpoint, offer *sdp.Session, host string, send bool, want []string) (*Session, *sdp.Session, VideoCodec, error) {
	if err := loadIdentity(); err != nil {
		return nil, nil, VideoCodec{}, err
	}
	var v *sdp.Media
	for _, m := range offer.Media {
		if m.Type == "video" && m.Port != 0 {
			v = m
			break
		}
	}
	if v == nil || !strings.Contains(strings.ToUpper(v.Proto), "SAVPF") {
		return nil, nil, VideoCodec{}, ErrNoVideo
	}
	fp := attr(offer, v, "fingerprint")
	alg, val, _ := strings.Cut(fp, " ")
	peerFP, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(val), ":", ""))
	if !strings.EqualFold(alg, "sha-256") || err != nil || len(peerFP) != 32 || attr(offer, v, "ice-ufrag") == "" {
		return nil, nil, VideoCodec{}, ErrNoKeys
	}
	vc, ok := pickVideo(v, want)
	if !ok {
		return nil, nil, VideoCodec{}, ErrNoVideo
	}
	dir := "recvonly"
	if send {
		dir = "sendonly"
	}
	if d := offer.Direction(v); send && d == "sendonly" || !send && d == "recvonly" {
		return nil, nil, VideoCodec{}, fmt.Errorf("webrtc: the offer's video is %s", d)
	}
	s := &Session{ep: ep, ufrag: randomToken(8), pwd: randomToken(24), PT: vc.PT, EventPT: -1, OpusPT: -1,
		connected: make(chan struct{}), closed: make(chan struct{})}
	s.dtls = dtls.NewServer(identity.der, identity.key, peerFP)
	s.mid = attr(offer, v, "mid")
	if s.mid == "" {
		s.mid = "0"
	}
	ans, err := s.describeVideo(host, dir, vc, offer, v)
	if err != nil {
		return nil, nil, VideoCodec{}, err
	}
	ep.OnOther(s.receive)
	go s.retransmit()
	return s, ans, vc, nil
}

// describeVideo writes the answer: the one video line, the transport's keys, the codec, RTCP
// feedback for keyframe requests (PLI and FIR) and NACK. Lines for any other offered media are
// refused (port 0), in their places.
func (s *Session) describeVideo(host, dir string, vc VideoCodec, offer *sdp.Session, v *sdp.Media) (*sdp.Session, error) {
	fam := "IP4"
	if strings.Contains(host, ":") {
		fam = "IP6"
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\r\n", args...) }
	w("v=0")
	w("o=BitVoice %d 1 IN %s %s", time.Now().UnixNano()/1000, fam, host)
	w("s=-")
	w("t=0 0")
	w("a=group:BUNDLE %s", s.mid)
	w("a=ice-lite")
	for _, m := range offer.Media {
		if m != v {
			mid := attr(offer, m, "mid")
			w("m=%s 0 %s %s", m.Type, m.Proto, strings.Join(m.Formats, " "))
			if mid != "" {
				w("a=mid:%s", mid)
			}
			continue
		}
		w("m=video %d UDP/TLS/RTP/SAVPF %d", s.ep.Port(), vc.PT)
		w("c=IN %s %s", fam, host)
		w("a=mid:%s", s.mid)
		w("a=ice-ufrag:%s", s.ufrag)
		w("a=ice-pwd:%s", s.pwd)
		w("a=fingerprint:sha-256 %s", identity.fingerprint)
		w("a=setup:passive")
		w("a=rtcp-mux")
		w("a=rtcp-rsize")
		w("a=%s", dir)
		w("a=rtpmap:%d %s/90000", vc.PT, vc.Name)
		if vc.Fmtp != "" {
			w("a=fmtp:%d %s", vc.PT, vc.Fmtp)
		}
		w("a=rtcp-fb:%d nack", vc.PT)
		w("a=rtcp-fb:%d nack pli", vc.PT)
		w("a=rtcp-fb:%d ccm fir", vc.PT)
		if dir == "sendonly" {
			w("a=ssrc:%d cname:bitvoice", VideoSSRC)
		}
		w("a=candidate:1 1 udp 2130706431 %s %d typ host", host, s.ep.Port())
		w("a=end-of-candidates")
	}
	return sdp.Parse([]byte(b.String()))
}

// VideoSSRC is the source BitVoice sends a viewer video as (one viewer per session, so one source).
const VideoSSRC = 0x42565631 // "BVV1"
