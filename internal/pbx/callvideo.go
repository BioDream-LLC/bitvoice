package pbx

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/vrec"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
)

// Caller video on 911 calls (DESIGN 4.2.3): the call-taker sends the caller a link; the caller's
// phone browser shares its camera into the call over WHIP (RFC 9725), or sends photos and clips
// through the same page. Video a phone sends in the call itself (SIP video over the ESInet) is the
// same kind of source. Call-takers, CAD and units watch over WHEP. Video never touches the voice:
// it has its own ports, and the number of live sources is capped per server.

// VideoLinkLife is how long a caller's link works after the call ends (for photos sent late).
const VideoLinkLife = time.Hour

// maxViewers is the most viewers one source has at once.
const maxViewers = 8

type callVideo struct {
	token     string // in the caller's link
	viewToken string // for CAD and units: watching only
	call      *Call
	created   time.Time
	src       *videoSource
	viewers   map[string]*videoViewer
}

type videoSource struct {
	ep       *media.Endpoint
	sess     *webrtc.Session // nil for SIP video in the call
	codec    webrtc.VideoCodec
	ssrc     atomic.Uint32
	last     atomic.Int64 // unix ms of the last packet
	since    time.Time
	sip      bool
	stopTap  func()
	resource string

	recMu   sync.Mutex
	rec     *vrec.Recorder
	recDone func(frames int, err error)
	recAsk  time.Time // when a key frame was last asked for the recording
}

type videoViewer struct {
	id    string
	who   string
	ep    *media.Endpoint
	sess  *webrtc.Session
	pt    int
	since time.Time
}

type videoHub struct {
	mu     sync.Mutex
	byTok  map[string]*callVideo // the caller's token and the viewing token both
	byCall map[string]*callVideo
}

func newToken() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CallVideo is a 911 call's video, as the call-taker's screen and the API show it.
type CallVideo struct {
	Token     string    `json:"token,omitempty"`     // the caller's link (shown to call-takers only)
	ViewToken string    `json:"viewToken,omitempty"` // for CAD and units
	Live      bool      `json:"live"`
	Source    string    `json:"source,omitempty"` // link (WHIP) or call (SIP video)
	Codec     string    `json:"codec,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Viewers   []string  `json:"viewers"`
}

func (v *callVideo) info() *CallVideo {
	out := &CallVideo{Token: v.token, ViewToken: v.viewToken, Viewers: []string{}}
	if s := v.src; s != nil {
		out.Live = s.last.Load() != 0 // video is coming; when it stops, the source is ended
		out.Codec, out.Since = s.codec.Name, s.since
		out.Source = "link"
		if s.sip {
			out.Source = "call"
		}
	}
	for _, w := range v.viewers {
		out.Viewers = append(out.Viewers, w.who)
	}
	return out
}

// VideoFor is a 911 call's video, or nil.
func (e *Engine) VideoFor(callID string) *CallVideo {
	e.video.mu.Lock()
	defer e.video.mu.Unlock()
	if v := e.video.byCall[callID]; v != nil {
		return v.info()
	}
	return nil
}

// changed refreshes what the call's screen shows of its video and tells the screens. Called without
// e.video.mu held.
func (e *Engine) videoChanged(v *callVideo) {
	e.video.mu.Lock()
	in := v.info()
	e.video.mu.Unlock()
	old := v.call.video.Swap(in)
	info := v.call.info()
	e.publish("call.updated", info)
	if (old != nil && old.Live) != in.Live {
		e.publish("call.video", info) // live video started or stopped: CAD hears of it
	}
}

// ErrNotVideoCall is returned for video on a call that is not a 911 call.
var ErrNotVideoCall = errors.New("video is for 911 calls")

// VideoLink makes (once) the link a 911 caller opens to share video and send photos.
func (e *Engine) VideoLink(callID string) (*CallVideo, error) {
	e.mu.Lock()
	c := e.calls[callID]
	e.mu.Unlock()
	if c == nil {
		return nil, errors.New("no such call")
	}
	c.mu.Lock()
	ok := c.i3 != nil || c.esinet
	c.mu.Unlock()
	if !ok {
		return nil, ErrNotVideoCall
	}
	h := &e.video
	h.mu.Lock()
	if h.byTok == nil {
		h.byTok, h.byCall = map[string]*callVideo{}, map[string]*callVideo{}
	}
	v := h.byCall[callID]
	if v == nil {
		v = &callVideo{token: newToken(), viewToken: newToken(), call: c, created: time.Now(), viewers: map[string]*videoViewer{}}
		h.byTok[v.token], h.byTok[v.viewToken], h.byCall[callID] = v, v, v
		go func() {
			<-c.cancelled
			e.endCallVideo(v, true)
			time.AfterFunc(VideoLinkLife, func() { e.dropVideo(v) })
		}()
	}
	h.mu.Unlock()
	e.videoChanged(v)
	return e.VideoFor(callID), nil
}

// VideoToken resolves a link's token to its call: whether it is the caller's (may send) and whether
// the call is still up.
func (e *Engine) VideoToken(token string) (callID string, caller, up bool, ok bool) {
	e.video.mu.Lock()
	v := e.video.byTok[token]
	e.video.mu.Unlock() // the call's lock is never taken under the video's
	if v == nil || token == "" {
		return "", false, false, false
	}
	v.call.mu.Lock()
	up = !v.call.ended
	v.call.mu.Unlock()
	return v.call.ID, token == v.token, up, true
}

func (e *Engine) dropVideo(v *callVideo) {
	e.video.mu.Lock()
	delete(e.video.byTok, v.token)
	delete(e.video.byTok, v.viewToken)
	if e.video.byCall[v.call.ID] == v {
		delete(e.video.byCall, v.call.ID)
	}
	e.video.mu.Unlock()
}

// endCallVideo stops the live video (and with all, every viewer).
func (e *Engine) endCallVideo(v *callVideo, all bool) {
	e.video.mu.Lock()
	src := v.src
	v.src = nil
	var vs []*videoViewer
	if all {
		for id, w := range v.viewers {
			vs = append(vs, w)
			delete(v.viewers, id)
		}
	}
	e.video.mu.Unlock()
	if src != nil {
		src.close()
	}
	for _, w := range vs {
		w.close()
	}
	e.videoChanged(v)
}

func (s *videoSource) close() {
	if s.stopTap != nil {
		s.stopTap()
	}
	s.recMu.Lock()
	r, done := s.rec, s.recDone
	s.rec = nil
	s.recMu.Unlock()
	if r != nil {
		err := r.Close()
		done(r.Frames(), err)
	}
	if s.sess != nil {
		s.sess.Close()
	}
	if !s.sip && s.ep != nil {
		s.ep.Close()
	}
}

func (w *videoViewer) close() {
	if w.sess != nil {
		w.sess.Close()
	}
	if w.ep != nil {
		w.ep.Close()
	}
}

// liveSources counts the live video sources on this server.
func (e *Engine) liveSources() int {
	n := 0
	for _, v := range e.video.byCall {
		if v.src != nil {
			n++
		}
	}
	return n
}

func (e *Engine) videoCap() int {
	if n := e.Config().System.PSAP.VideoStreams; n > 0 {
		return n
	}
	return 4
}

// VideoKbps is the bit rate a caller's video is asked to stay under.
func (e *Engine) VideoKbps() int {
	if n := e.Config().System.PSAP.VideoKbps; n > 0 {
		return n
	}
	return 1500
}

// WHIP takes a caller's video offer from their link (the browser's camera) and answers it.
func (e *Engine) WHIP(token string, offer []byte, host string) (answer []byte, resource string, err error) {
	h := &e.video
	h.mu.Lock()
	v := h.byTok[token]
	h.mu.Unlock()
	if v == nil || token != v.token {
		return nil, "", errors.New("this link is not valid")
	}
	v.call.mu.Lock()
	ended := v.call.ended
	v.call.mu.Unlock()
	if ended {
		return nil, "", errors.New("the call has ended: send photos and clips instead")
	}
	h.mu.Lock()
	if v.src == nil && e.liveSources() >= e.videoCap() {
		h.mu.Unlock()
		return nil, "", errors.New("this server takes no more live video now (its limit protects the calls); send photos and clips instead")
	}
	old := v.src
	v.src = nil
	h.mu.Unlock()
	if old != nil {
		old.close() // the caller started again (another camera, a reload)
	}
	off, err := sdp.Parse(offer)
	if err != nil {
		return nil, "", errors.New("the offer is not SDP")
	}
	ep, err := e.browserEndpoint(host)
	if err != nil {
		return nil, "", errors.New("no media port free")
	}
	sess, ans, vc, err := webrtc.AnswerVideo(ep, off, host, false, []string{"H264", "VP8"})
	if err != nil {
		ep.Close()
		return nil, "", err
	}
	src := &videoSource{ep: ep, sess: sess, codec: vc, since: time.Now(), resource: newToken()}
	e.tapSource(v, src)
	ep.Start()
	h.mu.Lock()
	if v.src != nil {
		h.mu.Unlock()
		src.close()
		return nil, "", errors.New("another camera is already sharing on this link")
	}
	v.src = src
	h.mu.Unlock()
	go e.watchSource(v, src)
	e.Log.Info("911 caller video started", "call", v.call.ID, "codec", vc.Name)
	e.videoChanged(v)
	return withBandwidth(ans.Bytes(), e.VideoKbps()), src.resource, nil
}

// withBandwidth caps a description's video bit rate (b=AS, and b=TIAS in bits per second).
func withBandwidth(b []byte, kbps int) []byte {
	s := string(b)
	i := strings.Index(s, "m=video")
	if i < 0 {
		return b
	}
	j := strings.Index(s[i:], "\r\n")
	if j < 0 {
		return b
	}
	at := i + j + 2
	if k := strings.Index(s[at:], "c="); k == 0 { // after the connection line
		at += strings.Index(s[at:], "\r\n") + 2
	}
	return []byte(s[:at] + "b=AS:" + itoaInt(kbps) + "\r\nb=TIAS:" + itoaInt(kbps*1000) + "\r\n" + s[at:])
}

// EndWHIP ends the caller's live video (a WHIP DELETE on its resource).
func (e *Engine) EndWHIP(token, resource string) bool {
	e.video.mu.Lock()
	v := e.video.byTok[token]
	ok := v != nil && token == v.token && v.src != nil && v.src.resource == resource
	e.video.mu.Unlock()
	if ok {
		e.endCallVideo(v, false)
	}
	return ok
}

// tapSource sends the source's packets to every viewer.
func (e *Engine) tapSource(v *callVideo, src *videoSource) {
	e.startRecording(v, src)
	src.stopTap = src.ep.Listen(func(pkt []byte) {
		if len(pkt) < 12 {
			return
		}
		src.record(e, pkt)
		src.ssrc.Store(binary.BigEndian.Uint32(pkt[8:]))
		if src.last.Swap(time.Now().UnixMilli()) == 0 {
			go e.videoChanged(v) // the first picture: the call-taker's screen shows it live
		}
		e.video.mu.Lock()
		vs := make([]*videoViewer, 0, len(v.viewers))
		for _, w := range v.viewers {
			vs = append(vs, w)
		}
		e.video.mu.Unlock()
		for _, w := range vs {
			out := append([]byte(nil), pkt...)
			out[1] = out[1]&0x80 | byte(w.pt)
			binary.BigEndian.PutUint32(out[8:], webrtc.VideoSSRC)
			w.ep.SendRTP(out)
		}
	})
}

// keyframe asks the source for a full picture (RTCP PLI, RFC 4585), for a viewer that just joined
// or lost one.
func (e *Engine) keyframe(src *videoSource) {
	if src == nil {
		return
	}
	pli := make([]byte, 12)
	pli[0], pli[1] = 0x81, 206 // version 2, FMT 1 (PLI), payload-specific feedback
	binary.BigEndian.PutUint16(pli[2:], 2)
	binary.BigEndian.PutUint32(pli[4:], 1)
	binary.BigEndian.PutUint32(pli[8:], src.ssrc.Load())
	if src.sip {
		src.ep.SendRTCP(pli)
	} else {
		src.ep.SendMuxRTCP(pli)
	}
}

// WHEP takes a viewer's offer: a call-taker (callID, signed in) or CAD and units (the viewing token).
// who names the viewer for the call's record of who saw it.
func (e *Engine) WHEP(callOrToken string, staff bool, offer []byte, host, who string) (answer []byte, id string, err error) {
	h := &e.video
	h.mu.Lock()
	v := h.byTok[callOrToken]
	if staff {
		v = h.byCall[callOrToken]
	} else if v != nil && callOrToken != v.viewToken {
		v = nil // the caller's own token sends; it does not watch
	}
	if v == nil {
		h.mu.Unlock()
		return nil, "", errors.New("no video for this call")
	}
	needSIP := v.src == nil
	h.mu.Unlock()
	if needSIP {
		e.sipVideoSource(v) // reads the call: never under the video's lock
	}
	h.mu.Lock()
	src := v.src
	if src == nil {
		h.mu.Unlock()
		return nil, "", errors.New("the caller is not sharing video now")
	}
	if len(v.viewers) >= maxViewers {
		h.mu.Unlock()
		return nil, "", errors.New("this video has as many viewers as it takes")
	}
	h.mu.Unlock()
	off, err := sdp.Parse(offer)
	if err != nil {
		return nil, "", errors.New("the offer is not SDP")
	}
	ep, err := e.browserEndpoint(host)
	if err != nil {
		return nil, "", errors.New("no media port free")
	}
	sess, ans, vc, err := webrtc.AnswerVideo(ep, off, host, true, []string{src.codec.Name})
	if err != nil {
		ep.Close()
		return nil, "", err
	}
	w := &videoViewer{id: newToken(), who: who, ep: ep, sess: sess, pt: vc.PT, since: time.Now()}
	ep.OnMuxRTCP(func(pkt []byte) {
		if rtcpAsksKeyframe(pkt) {
			e.video.mu.Lock()
			s := v.src
			e.video.mu.Unlock()
			e.keyframe(s)
		}
	})
	ep.Start()
	h.mu.Lock()
	v.viewers[w.id] = w
	h.mu.Unlock()
	go func() {
		select {
		case <-sess.Connected():
			e.video.mu.Lock()
			s := v.src
			e.video.mu.Unlock()
			e.keyframe(s)
		case <-time.After(15 * time.Second):
		}
	}()
	e.videoChanged(v)
	return ans.Bytes(), w.id, nil
}

// EndWHEP drops a viewer.
func (e *Engine) EndWHEP(id string) bool {
	e.video.mu.Lock()
	var w *videoViewer
	var v *callVideo
	for _, cv := range e.video.byCall {
		if x := cv.viewers[id]; x != nil {
			w, v = x, cv
			delete(cv.viewers, id)
		}
	}
	e.video.mu.Unlock()
	if w == nil {
		return false
	}
	w.close()
	e.videoChanged(v)
	return true
}

// rtcpAsksKeyframe finds a PLI or FIR in a compound RTCP packet.
func rtcpAsksKeyframe(b []byte) bool {
	for len(b) >= 4 {
		n := (int(binary.BigEndian.Uint16(b[2:])) + 1) * 4
		if n > len(b) {
			return false
		}
		if b[1] == 206 && (b[0]&0x1f == 1 || b[0]&0x1f == 4) {
			return true
		}
		b = b[n:]
	}
	return false
}

// sipVideoSource makes the caller's own SIP video (video in the 911 call) the source, when the
// call carries it.
func (e *Engine) sipVideoSource(v *callVideo) {
	c := v.call
	c.mu.Lock()
	l := c.caller
	c.mu.Unlock()
	if l == nil {
		return
	}
	ep := l.videoEP()
	if ep == nil {
		return
	}
	l.mu.Lock()
	remote := l.remoteSDP
	l.mu.Unlock()
	vm := videoMedia(remote)
	if vm == nil {
		return
	}
	var vc webrtc.VideoCodec
	for _, k := range vm.Codecs() {
		if n := strings.ToUpper(k.Name); n == "H264" || n == "VP8" {
			vc = webrtc.VideoCodec{Name: n, PT: k.PT}
			break
		}
	}
	if vc.Name == "" {
		return
	}
	src := &videoSource{ep: ep, codec: vc, since: time.Now(), sip: true}
	e.video.mu.Lock()
	if v.src != nil {
		e.video.mu.Unlock()
		return
	}
	v.src = src
	e.video.mu.Unlock()
	e.tapSource(v, src)
}

// browserEndpoint opens media ports for a browser on the address the browser is told to reach
// (host), so ICE replies leave from the very address the browser checked: a socket on every
// address would answer from whichever one the route goes out of, and the browser would ignore it.
// When the address is not this computer's own (a public address in front of a router), every
// address is used, as for phones.
func (e *Engine) browserEndpoint(host string) (*media.Endpoint, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ep, err := e.pool.AllocateOn(host); err == nil {
			return ep, nil
		}
	}
	return e.pool.Allocate()
}

// videoSilence ends a caller's live video once no picture has come for this long (a phone that lost
// its signal or was put away), or when none came at all after the connection was made.
var videoSilence = 10 * time.Second

func (e *Engine) watchSource(v *callVideo, src *videoSource) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	start := time.Now()
	for range t.C {
		e.video.mu.Lock()
		still := v.src == src
		e.video.mu.Unlock()
		if !still {
			return
		}
		last := src.last.Load()
		if last == 0 && time.Since(start) > 3*videoSilence || last != 0 && time.Since(time.UnixMilli(last)) > videoSilence {
			e.Log.Info("911 caller video stopped: no picture", "call", v.call.ID)
			e.video.mu.Lock()
			if v.src == src {
				v.src = nil
			}
			e.video.mu.Unlock()
			src.close()
			e.videoChanged(v)
			return
		}
	}
}

// startRecording begins keeping the caller's live video, when the PSAP records it.
func (e *Engine) startRecording(v *callVideo, src *videoSource) {
	if !e.Config().System.PSAP.RecordVideo || e.OnVideoRecord == nil {
		return
	}
	w, done := e.OnVideoRecord(v.call.ID)
	if w == nil {
		return
	}
	r, err := vrec.New(w, src.codec.Name)
	if err != nil {
		done(0, err)
		return
	}
	src.recMu.Lock()
	src.rec, src.recDone = r, done
	src.recMu.Unlock()
}

// record writes one packet to the recording, and asks the phone for a full picture while the
// recording waits for one (at the start, and after a loss).
func (s *videoSource) record(e *Engine, pkt []byte) {
	s.recMu.Lock()
	r := s.rec
	ask := false
	if r != nil {
		r.Write(pkt)
		if r.NeedsKeyFrame() && time.Since(s.recAsk) > time.Second {
			s.recAsk, ask = time.Now(), true
		}
	}
	s.recMu.Unlock()
	if ask {
		e.keyframe(s)
	}
}
