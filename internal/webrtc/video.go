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
	return AnswerVideoWith(ep, offer, host, send, want, VideoOptions{})
}

// VideoOptions add to a video answer: simulcast (a sending browser's layers, RFC 8853, with their RIDs
// RFC 8851) and BitVoice's part in ICE.
type VideoOptions struct {
	Simulcast bool
	ICE       ICEOptions
}

// Simulcast is what an answer agreed for a browser's simulcast: its layers' RIDs, best first as the
// browser listed them, and the header extension IDs that carry each packet's RID (and repaired RID).
type Simulcast struct {
	RIDs        []string
	RIDExt      int
	RepairedExt int
}

// Simulcast returns what the session agreed for simulcast (nil RIDs when none).
func (s *Session) Simulcast() Simulcast { return s.simulcast }

// AnswerVideoWith is AnswerVideo with simulcast and ICE choices.
func AnswerVideoWith(ep *media.Endpoint, offer *sdp.Session, host string, send bool, want []string, o VideoOptions) (*Session, *sdp.Session, VideoCodec, error) {
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
	if o.Simulcast && !send {
		s.simulcast = simulcastOf(v)
	}
	if o.ICE.Full {
		_, lite := offer.SessionAttr("ice-lite")
		s.startICE(o.ICE, host, lite)
	}
	ans, err := s.describeVideo(host, dir, vc, offer, v)
	if err != nil {
		s.Close()
		return nil, nil, VideoCodec{}, err
	}
	ep.OnOther(s.receive)
	s.remoteICE(offer, v)
	go s.retransmit()
	return s, ans, vc, nil
}

// simulcastOf reads a sending browser's simulcast offer: a=rid:X send lines, a=simulcast:send X;Y,
// and the RID header extensions.
func simulcastOf(v *sdp.Media) Simulcast {
	var sc Simulcast
	send := map[string]bool{}
	for _, r := range v.Attrs("rid") {
		f := strings.Fields(r)
		if len(f) >= 2 && f[1] == "send" {
			send[f[0]] = true
		}
	}
	if s, ok := v.Attr("simulcast"); ok {
		f := strings.Fields(s)
		if len(f) >= 2 && f[0] == "send" {
			for _, alt := range strings.Split(f[1], ";") {
				r := strings.TrimPrefix(strings.Split(alt, ",")[0], "~")
				if send[r] {
					sc.RIDs = append(sc.RIDs, r)
				}
			}
		}
	}
	for _, e := range v.Attrs("extmap") {
		f := strings.Fields(e)
		if len(f) < 2 {
			continue
		}
		id := 0
		fmt.Sscan(strings.Split(f[0], "/")[0], &id)
		switch f[1] {
		case "urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id":
			sc.RIDExt = id
		case "urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id":
			sc.RepairedExt = id
		}
	}
	if len(sc.RIDs) < 2 || sc.RIDExt == 0 {
		return Simulcast{} // not simulcast after all: one stream as usual
	}
	return sc
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
	if s.agent == nil {
		w("a=ice-lite")
	}
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
		if sc := s.simulcast; len(sc.RIDs) > 0 {
			// The receiver tells the browser how much it may send (REMB): without it Chrome keeps
			// its start rate and sends only its small layers.
			w("a=rtcp-fb:%d goog-remb", vc.PT)
			w("a=extmap:%d urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id", sc.RIDExt)
			if sc.RepairedExt != 0 {
				w("a=extmap:%d urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id", sc.RepairedExt)
			}
			for _, r := range sc.RIDs {
				w("a=rid:%s recv", r)
			}
			w("a=simulcast:recv %s", strings.Join(sc.RIDs, ";"))
		}
		if dir == "sendonly" {
			w("a=ssrc:%d cname:bitvoice", VideoSSRC)
		}
		if s.agent == nil {
			for _, c := range liteCandidates(host, s.ep.Port()) {
				w("%s", c)
			}
		} else {
			for _, c := range s.cands {
				w("a=candidate:%s", c.SDP())
			}
		}
		w("a=end-of-candidates")
	}
	return sdp.Parse([]byte(b.String()))
}

// VideoSSRC is the source BitVoice sends a viewer video as (one viewer per session, so one source).
const VideoSSRC = 0x42565631 // "BVV1"
