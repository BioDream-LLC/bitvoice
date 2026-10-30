package pbx

import (
	"encoding/binary"
	"errors"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/webrtc"
)

// A selective forwarding (SFU) video room: everyone in a browser sends their camera once (WHIP style)
// and receives every other camera, each in its own stream (WHEP style), forwarded as it came, never
// decoded. A browser that sends simulcast (two or three sizes of the same picture) lets each viewer
// get the size it shows: the large tile the full one, the thumbnails the small one; a switch of size
// waits for the next full picture of the new size, asked for at once. The room's sound is its
// conference, as every member hears it; the room's video is this.

// sfuHub holds the SFU rooms by conference number.
type sfuHub struct {
	mu    sync.Mutex
	rooms map[string]*sfuRoom
}

type sfuRoom struct {
	number string
	pubs   map[string]*sfuPub
	subs   map[string]*sfuSub
}

type sfuPub struct {
	id, name string
	kind     string // "camera" or "screen" (a shared screen)
	ep       *media.Endpoint
	sess     *webrtc.Session
	codec    webrtc.VideoCodec
	sc       webrtc.Simulcast
	since    time.Time
	last     atomic.Int64 // unix ms of the last packet
	left     atomic.Bool  // the camera has left the room
	stop     func()

	mu      sync.Mutex
	layers  map[string]uint32 // RID -> SSRC, learnt from the packets' RID extension
	ssrcs   map[uint32]string // and back
	seen    map[uint32]*layerSeen
	plain   uint32 // the SSRC of a publisher without simulcast
	subs    map[string]*sfuSub
	lastPLI time.Time
}

type sfuSub struct {
	id   string
	pub  *sfuPub
	ep   *media.Endpoint
	sess *webrtc.Session
	pt   int

	mu      sync.Mutex
	want    string // the layer asked for: "high", "low" (or a RID)
	cur     uint32 // the SSRC forwarded now; 0 until a keyframe of the wanted layer comes
	next    uint32 // the SSRC to switch to at its next keyframe
	seqOff  uint16
	tsOff   uint32
	lastSeq uint16
	lastTS  uint32
	lastAt  time.Time
	started bool
	// VP8's picture numbers run per layer: rewritten so the viewer sees one run across switches.
	picOff, lastPic uint16
	tl0Off, lastTL0 uint8
}

// layerSeen is what the packets of one simulcast layer showed: when the last came, and the picture
// size its last keyframe gave (VP8; 0 when not known). A browser drops layers when its upload is
// short and sends fewer for a small camera, so viewers get the sizes that actually flow.
type layerSeen struct {
	last time.Time
	w, h int
}

// SFUPublisher is one camera or shared screen in a room, as the API lists it.
type SFUPublisher struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"` // camera, or screen (someone's shared screen)
	Codec  string    `json:"codec"`
	Layers []string  `json:"layers,omitempty"` // simulcast RIDs, best first
	Live   bool      `json:"live"`
	Since  time.Time `json:"since"`
	Views  int       `json:"views"`
}

// SFURoom is a room's cameras.
type SFURoom struct {
	Number     string         `json:"number"`
	Publishers []SFUPublisher `json:"publishers"`
}

// Errors from the SFU.
var (
	ErrNoSFURoom = errors.New("there is no video room of that number (a conference room with Video set to everyone's video)")
	ErrSFUFull   = errors.New("the video room is full")
	ErrSFUGone   = errors.New("that camera has left the room")
	// ErrSFUScreenTaken: a room shows one shared screen at a time.
	ErrSFUScreenTaken = errors.New("someone is already sharing their screen in this room: one screen at a time")
)

func (e *Engine) sfuRoomCfg(number string) (*config.Conference, error) {
	r := e.Config().FindConference(number)
	if r == nil || r.Video != "sfu" {
		return nil, ErrNoSFURoom
	}
	return r, nil
}

func (h *sfuHub) room(number string) *sfuRoom {
	if h.rooms == nil {
		h.rooms = map[string]*sfuRoom{}
	}
	r := h.rooms[number]
	if r == nil {
		r = &sfuRoom{number: number, pubs: map[string]*sfuPub{}, subs: map[string]*sfuSub{}}
		h.rooms[number] = r
	}
	return r
}

// SFUPublish takes a browser's camera offer for a room (video only, simulcast if it offers it).
// kind is "camera" (or empty) or "screen": a shared screen, one per room at a time, beside the
// cameras (it does not count toward their limit).
func (e *Engine) SFUPublish(number, name, kind string, offer []byte, host string, ice webrtc.ICEOptions) (answer []byte, id string, err error) {
	cfg, err := e.sfuRoomCfg(number)
	if err != nil {
		return nil, "", err
	}
	if kind == "" {
		kind = "camera"
	}
	if kind != "camera" && kind != "screen" {
		return nil, "", errors.New("kind is camera or screen")
	}
	limit := cfg.MaxMembers
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 16) // every viewer receives every camera: a room of more is too much for browsers
	e.sfu.mu.Lock()
	err = e.sfu.room(number).roomFor(kind, limit)
	e.sfu.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	off, err := sdp.Parse(offer)
	if err != nil {
		return nil, "", errors.New("the offer is not SDP")
	}
	ep, err := e.browserEndpoint(host)
	if err != nil {
		return nil, "", errors.New("no media port free")
	}
	want := []string{"VP8", "H264"}
	if cfg.VideoCodec == "H264" {
		want = []string{"H264", "VP8"}
	}
	sess, ans, vc, err := webrtc.AnswerVideoWith(ep, off, host, false, want, webrtc.VideoOptions{Simulcast: true, ICE: ice})
	if err != nil {
		ep.Close()
		return nil, "", err
	}
	p := &sfuPub{id: newToken(), name: name, kind: kind, ep: ep, sess: sess, codec: vc, sc: sess.Simulcast(), since: time.Now(),
		layers: map[string]uint32{}, ssrcs: map[uint32]string{}, subs: map[string]*sfuSub{}}
	e.sfu.mu.Lock()
	// Checked again as it goes in: two people pressing "Share my screen" at once get one screen.
	if err := e.sfu.room(number).roomFor(kind, limit); err != nil {
		e.sfu.mu.Unlock()
		sess.Close()
		ep.Close()
		return nil, "", err
	}
	e.sfu.room(number).pubs[p.id] = p
	e.sfu.mu.Unlock()
	p.stop = ep.Listen(func(pkt []byte) { e.sfuPacket(p, pkt) })
	ep.Start()
	go e.sfuWatch(number, p)
	if len(p.sc.RIDs) > 0 {
		go p.sendREMB()
	}
	e.Log.Info("video room: a "+kind+" joined", "room", number, "codec", vc.Name, "layers", len(p.sc.RIDs))
	e.sfuChanged(number)
	return withBandwidth(ans.Bytes(), max(e.VideoKbps(), 1500)), p.id, nil
}

// sfuWatch closes a publisher whose connection never came up, or whose pictures stopped.
func (e *Engine) sfuWatch(number string, p *sfuPub) {
	select {
	case <-p.sess.Connected():
	case <-time.After(20 * time.Second):
		e.SFULeave(number, p.id)
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		e.sfu.mu.Lock()
		_, still := e.sfu.room(number).pubs[p.id]
		e.sfu.mu.Unlock()
		if !still {
			return
		}
		last := p.last.Load()
		// A shared screen that does not change may send almost nothing for a while: it gets longer.
		quiet := 15 * time.Second
		if p.kind == "screen" {
			quiet = time.Minute
		}
		if last == 0 && time.Since(p.since) > 20*time.Second || last != 0 && time.Since(time.UnixMilli(last)) > quiet {
			e.SFULeave(number, p.id)
			return
		}
	}
}

// ridOf reads a packet's RID header extension (RFC 8852), one-byte or two-byte form.
func ridOf(pkt []byte, id int) string {
	if id == 0 || len(pkt) < 12 || pkt[0]&0x10 == 0 {
		return ""
	}
	off := 12 + 4*int(pkt[0]&0x0f)
	if off+4 > len(pkt) {
		return ""
	}
	profile := binary.BigEndian.Uint16(pkt[off:])
	n := 4 * int(binary.BigEndian.Uint16(pkt[off+2:]))
	b := pkt[off+4:]
	if n > len(b) {
		return ""
	}
	b = b[:n]
	switch {
	case profile == 0xBEDE:
		for len(b) > 0 {
			if b[0] == 0 {
				b = b[1:]
				continue
			}
			eid, l := int(b[0]>>4), int(b[0]&0x0f)+1
			if eid == 15 || 1+l > len(b) {
				return ""
			}
			if eid == id {
				return string(b[1 : 1+l])
			}
			b = b[1+l:]
		}
	case profile&0xFFF0 == 0x1000:
		for len(b) >= 2 {
			if b[0] == 0 {
				b = b[1:]
				continue
			}
			eid, l := int(b[0]), int(b[1])
			if 2+l > len(b) {
				return ""
			}
			if eid == id {
				return string(b[2 : 2+l])
			}
			b = b[2+l:]
		}
	}
	return ""
}

// vp8Fields finds a VP8 packet's PictureID and TL0PICIDX (RFC 7741 4.2): their offsets in the
// packet (-1 when absent) and whether the PictureID is the 15-bit form.
func vp8Fields(pkt []byte) (pic int, long bool, tl0 int) {
	pic, tl0 = -1, -1
	p := rtpPayload(pkt)
	if len(p) < 2 || p[0]&0x80 == 0 {
		return
	}
	off := cap(pkt) - cap(p) // the payload's offset in pkt (p is a part of pkt)
	ext, i := p[1], 2
	if ext&0x80 != 0 {
		if i >= len(p) {
			return
		}
		pic, long = off+i, p[i]&0x80 != 0
		if long {
			i += 2
		} else {
			i++
		}
	}
	if ext&0x40 != 0 && i < len(p) {
		tl0 = off + i
	}
	return
}

// rtpPayload is a packet's payload, after the header, its CSRCs and its extension.
func rtpPayload(pkt []byte) []byte {
	if len(pkt) < 12 {
		return nil
	}
	off := 12 + 4*int(pkt[0]&0x0f)
	if pkt[0]&0x10 != 0 {
		if off+4 > len(pkt) {
			return nil
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(pkt[off+2:]))
	}
	if off > len(pkt) {
		return nil
	}
	end := len(pkt)
	if pkt[0]&0x20 != 0 && end > off {
		end -= int(pkt[end-1])
	}
	if end < off {
		return nil
	}
	return pkt[off:end]
}

// vp8Start is where the VP8 frame starts in a packet's payload, past the payload descriptor
// (RFC 7741), or -1 when the packet does not start a frame.
func vp8Start(p []byte) int {
	if len(p) == 0 {
		return -1
	}
	x, s, part := p[0]&0x80 != 0, p[0]&0x10 != 0, p[0]&0x0f
	if !s || part != 0 {
		return -1
	}
	i := 1
	if x {
		if len(p) < 2 {
			return -1
		}
		ext := p[1]
		i = 2
		if ext&0x80 != 0 { // PictureID
			if i >= len(p) {
				return -1
			}
			if p[i]&0x80 != 0 {
				i += 2
			} else {
				i++
			}
		}
		if ext&0x40 != 0 { // TL0PICIDX
			i++
		}
		if ext&0x30 != 0 { // TID/KEYIDX
			i++
		}
	}
	if i >= len(p) {
		return -1
	}
	return i
}

// vp8Size is the picture size a VP8 keyframe states (RFC 6386 9.1), or 0, 0.
func vp8Size(pkt []byte) (int, int) {
	p := rtpPayload(pkt)
	i := vp8Start(p)
	if i < 0 || len(p) < i+10 || p[i]&1 != 0 || p[i+3] != 0x9d || p[i+4] != 0x01 || p[i+5] != 0x2a {
		return 0, 0
	}
	return int(binary.LittleEndian.Uint16(p[i+6:]) & 0x3fff), int(binary.LittleEndian.Uint16(p[i+8:]) & 0x3fff)
}

// isKeyframe says whether a video packet starts a full picture (VP8 RFC 7741, H.264 RFC 6184).
func isKeyframe(codec string, pkt []byte) bool {
	p := rtpPayload(pkt)
	if len(p) == 0 {
		return false
	}
	if codec == "VP8" {
		i := vp8Start(p)
		return i >= 0 && p[i]&0x01 == 0 // P bit clear: a key frame
	}
	// H.264
	switch nal := p[0] & 0x1f; nal {
	case 5, 7:
		return true
	case 24: // STAP-A
		b := p[1:]
		for len(b) > 2 {
			l := int(binary.BigEndian.Uint16(b))
			if l == 0 || 2+l > len(b) {
				return false
			}
			if t := b[2] & 0x1f; t == 5 || t == 7 {
				return true
			}
			b = b[2+l:]
		}
	case 28: // FU-A: the first fragment of an IDR or SPS
		return len(p) >= 2 && p[1]&0x80 != 0 && (p[1]&0x1f == 5 || p[1]&0x1f == 7)
	}
	return false
}

// layerSSRC is the SSRC of the layer a viewer wants (p.mu held): "high" the largest picture that
// flows, "low" the smallest. Sizes come from keyframes; without them, the browser's order of RIDs
// (best first). Only layers heard in the last two seconds count, as long as one is.
func (p *sfuPub) layerSSRC(want string) uint32 {
	if len(p.sc.RIDs) == 0 {
		return p.plain
	}
	type cand struct {
		ssrc uint32
		rank int // order of the RID, best first
		area int
	}
	var all, live []cand
	for i, r := range p.sc.RIDs {
		s, ok := p.layers[r]
		if !ok {
			continue
		}
		c := cand{ssrc: s, rank: i}
		if l := p.seen[s]; l != nil {
			c.area = l.w * l.h
			if time.Since(l.last) < 2*time.Second {
				live = append(live, c)
			}
		}
		all = append(all, c)
	}
	if len(live) > 0 {
		all = live
	}
	if len(all) == 0 {
		return 0
	}
	sized := true
	for _, c := range all {
		sized = sized && c.area > 0
	}
	better := func(a, b cand) bool { // a shows more than b
		if sized && a.area != b.area {
			return a.area > b.area
		}
		return a.rank < b.rank
	}
	pick := all[0]
	for _, c := range all[1:] {
		if (want == "low") != better(c, pick) {
			pick = c
		}
	}
	return pick.ssrc
}

// sfuPacket forwards a publisher's packet to the viewers who get its layer.
func (e *Engine) sfuPacket(p *sfuPub, pkt []byte) {
	if len(pkt) < 12 || pkt[1]&0x7f != byte(p.codec.PT) {
		return // RTX repairs and other payloads are not forwarded
	}
	p.last.Store(time.Now().UnixMilli())
	ssrc := binary.BigEndian.Uint32(pkt[8:])
	p.mu.Lock()
	if len(p.sc.RIDs) > 0 {
		if _, known := p.ssrcs[ssrc]; !known {
			if rid := ridOf(pkt, p.sc.RIDExt); rid != "" && slices.Contains(p.sc.RIDs, rid) {
				p.ssrcs[ssrc], p.layers[rid] = rid, ssrc
			} else {
				p.mu.Unlock()
				return // a layer not identified yet: its RID comes on its first packets
			}
		}
	} else if p.plain == 0 {
		p.plain = ssrc
	}
	if p.seen == nil {
		p.seen = map[uint32]*layerSeen{}
	}
	l := p.seen[ssrc]
	if l == nil {
		l = &layerSeen{}
		p.seen[ssrc] = l
	}
	l.last = time.Now()
	if p.codec.Name == "VP8" {
		if w, h := vp8Size(pkt); w > 0 {
			l.w, l.h = w, h
		}
	}
	subs := make([]*sfuSub, 0, len(p.subs))
	for _, s := range p.subs {
		subs = append(subs, s)
	}
	p.mu.Unlock()
	key := isKeyframe(p.codec.Name, pkt)
	for _, s := range subs {
		s.forward(p, pkt, ssrc, key)
	}
}

// forward sends one packet to a viewer if it is of the layer the viewer gets now, switching layers
// at a keyframe of the new one, with one SSRC and continuous sequence numbers and timestamps.
func (s *sfuSub) forward(p *sfuPub, pkt []byte, ssrc uint32, key bool) {
	pkt = withoutExtensions(pkt)
	p.mu.Lock()
	want := p.layerSSRC(s.wantLayer())
	p.mu.Unlock()
	s.mu.Lock()
	if want != s.cur && want != 0 {
		s.next = want
	}
	vp8 := p.codec.Name == "VP8"
	picAt, tl0At := -1, -1 // VP8's picture numbers; H.264 has none (0 would rewrite the RTP header)
	var long bool
	if vp8 {
		picAt, long, tl0At = vp8Fields(pkt)
	}
	if s.next != 0 && ssrc == s.next && key {
		// The new layer's first full picture: switch here.
		seq, ts := binary.BigEndian.Uint16(pkt[2:]), binary.BigEndian.Uint32(pkt[4:])
		if s.started {
			gap := uint32(time.Since(s.lastAt).Seconds()*90000) + 1
			s.seqOff = s.lastSeq + 1 - seq
			s.tsOff = s.lastTS + gap - ts
			if picAt >= 0 {
				s.picOff = s.lastPic + 1 - readPic(pkt, picAt, long)
			}
			if tl0At >= 0 {
				s.tl0Off = s.lastTL0 + 1 - pkt[tl0At]
			}
		} else {
			s.seqOff, s.tsOff, s.picOff, s.tl0Off = 0, 0, 0, 0
		}
		s.cur, s.next = ssrc, 0
	}
	if ssrc != s.cur || s.cur == 0 {
		need := s.next != 0 && ssrc == s.next
		s.mu.Unlock()
		if need && !key {
			go p.askKeyframe(ssrc) // the switch waits for a full picture of the new layer
		}
		return
	}
	out := append([]byte(nil), pkt...)
	seq := binary.BigEndian.Uint16(out[2:]) + s.seqOff
	ts := binary.BigEndian.Uint32(out[4:]) + s.tsOff
	binary.BigEndian.PutUint16(out[2:], seq)
	binary.BigEndian.PutUint32(out[4:], ts)
	binary.BigEndian.PutUint32(out[8:], webrtc.VideoSSRC)
	out[1] = out[1]&0x80 | byte(s.pt)
	if picAt >= 0 {
		pic := readPic(out, picAt, long) + s.picOff
		if long {
			pic &= 0x7fff
			binary.BigEndian.PutUint16(out[picAt:], 0x8000|pic)
		} else {
			pic &= 0x7f
			out[picAt] = byte(pic)
		}
		s.lastPic = pic
	}
	if tl0At >= 0 {
		out[tl0At] += s.tl0Off
		s.lastTL0 = out[tl0At]
	}
	s.lastSeq, s.lastTS, s.lastAt, s.started = seq, ts, time.Now(), true
	s.mu.Unlock()
	s.ep.SendRTP(out)
}

// readPic reads a VP8 PictureID at off, 7 or 15 bits.
func readPic(pkt []byte, off int, long bool) uint16 {
	if long {
		return binary.BigEndian.Uint16(pkt[off:]) & 0x7fff
	}
	return uint16(pkt[off] & 0x7f)
}

func (s *sfuSub) wantLayer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.want
}

// askKeyframe asks the publisher for a full picture of one layer (RTCP PLI), at most every 300 ms.
func (p *sfuPub) askKeyframe(ssrc uint32) {
	if ssrc == 0 {
		return
	}
	p.mu.Lock()
	last := p.lastPLI
	if time.Since(last) < 300*time.Millisecond {
		p.mu.Unlock()
		return
	}
	p.lastPLI = time.Now()
	p.mu.Unlock()
	pli := make([]byte, 12)
	pli[0], pli[1] = 0x81, 206
	binary.BigEndian.PutUint16(pli[2:], 2)
	binary.BigEndian.PutUint32(pli[4:], 1)
	binary.BigEndian.PutUint32(pli[8:], ssrc)
	p.ep.SendMuxRTCP(pli)
}

// rembKbps is the rate BitVoice tells a simulcast camera it may send, all layers together.
const rembKbps = 2500

// sendREMB tells a simulcast camera, every second while it is in the room, that it may send
// rembKbps (RTCP REMB, draft-alvestrand-rmcat-remb), so its browser sends all its sizes.
func (p *sfuPub) sendREMB() {
	select {
	case <-p.sess.Connected():
	case <-time.After(20 * time.Second):
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if p.left.Load() {
			return
		}
		p.mu.Lock()
		ssrcs := make([]uint32, 0, len(p.layers))
		for _, s := range p.layers {
			ssrcs = append(ssrcs, s)
		}
		p.mu.Unlock()
		if len(ssrcs) == 0 {
			continue
		}
		p.ep.SendMuxRTCP(remb(rembKbps*1000, ssrcs))
	}
}

// remb builds an RTCP REMB packet for a rate in bits per second.
func remb(bps uint64, ssrcs []uint32) []byte {
	exp := 0
	for bps >= 1<<18 {
		bps >>= 1
		exp++
	}
	b := make([]byte, 20+4*len(ssrcs))
	b[0], b[1] = 0x8f, 206 // version 2, FMT 15 (application layer feedback), PSFB
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)/4-1))
	binary.BigEndian.PutUint32(b[4:], 1) // our SSRC; media SSRC is 0
	copy(b[12:], "REMB")
	b[16] = byte(len(ssrcs))
	b[17] = byte(exp<<2) | byte(bps>>16)
	binary.BigEndian.PutUint16(b[18:], uint16(bps))
	for i, s := range ssrcs {
		binary.BigEndian.PutUint32(b[20+4*i:], s)
	}
	return b
}

// SFUSubscribe takes a viewer's offer for one camera of a room (video only). layer is "high" or
// "low" (simulcast cameras; others send what they send).
func (e *Engine) SFUSubscribe(number, pubID, layer string, offer []byte, host string, ice webrtc.ICEOptions) (answer []byte, id string, err error) {
	if _, err := e.sfuRoomCfg(number); err != nil {
		return nil, "", err
	}
	e.sfu.mu.Lock()
	r := e.sfu.room(number)
	p := r.pubs[pubID]
	tooMany := len(r.subs) >= 16*16
	e.sfu.mu.Unlock()
	if p == nil {
		return nil, "", ErrSFUGone
	}
	if tooMany {
		return nil, "", ErrSFUFull
	}
	off, err := sdp.Parse(offer)
	if err != nil {
		return nil, "", errors.New("the offer is not SDP")
	}
	ep, err := e.browserEndpoint(host)
	if err != nil {
		return nil, "", errors.New("no media port free")
	}
	sess, ans, vc, err := webrtc.AnswerVideoWith(ep, off, host, true, []string{p.codec.Name}, webrtc.VideoOptions{ICE: ice})
	if err != nil {
		ep.Close()
		return nil, "", err
	}
	s := &sfuSub{id: newToken(), pub: p, ep: ep, sess: sess, pt: vc.PT, want: layer}
	ep.OnMuxRTCP(func(pkt []byte) {
		if rtcpAsksKeyframe(pkt) {
			s.mu.Lock()
			cur := s.cur
			s.mu.Unlock()
			p.askKeyframe(cur)
		}
	})
	ep.Start()
	e.sfu.mu.Lock()
	r.subs[s.id] = s
	e.sfu.mu.Unlock()
	p.mu.Lock()
	p.subs[s.id] = s
	p.mu.Unlock()
	go func() {
		select {
		case <-sess.Connected():
			p.mu.Lock()
			want := p.layerSSRC(layer)
			p.mu.Unlock()
			p.askKeyframe(want)
		case <-time.After(20 * time.Second):
			e.SFULeave(number, s.id)
		}
	}()
	e.sfuChanged(number)
	return ans.Bytes(), s.id, nil
}

// SFULayer changes the size a viewer gets of a simulcast camera.
func (e *Engine) SFULayer(number, subID, layer string) error {
	e.sfu.mu.Lock()
	s := e.sfu.room(number).subs[subID]
	e.sfu.mu.Unlock()
	if s == nil {
		return ErrSFUGone
	}
	s.mu.Lock()
	s.want = layer
	s.mu.Unlock()
	p := s.pub
	p.mu.Lock()
	want := p.layerSSRC(layer)
	p.mu.Unlock()
	p.askKeyframe(want)
	return nil
}

// SFULeave ends a camera (and everyone watching it) or one viewer.
func (e *Engine) SFULeave(number, id string) bool {
	e.sfu.mu.Lock()
	r := e.sfu.room(number)
	var closeSubs []*sfuSub
	p := r.pubs[id]
	if p != nil {
		delete(r.pubs, id)
		p.mu.Lock()
		for sid, s := range p.subs {
			closeSubs = append(closeSubs, s)
			delete(r.subs, sid)
		}
		p.subs = map[string]*sfuSub{}
		p.mu.Unlock()
	}
	s := r.subs[id]
	if s != nil {
		delete(r.subs, id)
		closeSubs = append(closeSubs, s)
		s.pub.mu.Lock()
		delete(s.pub.subs, id)
		s.pub.mu.Unlock()
	}
	e.sfu.mu.Unlock()
	if p == nil && s == nil {
		return false
	}
	for _, x := range closeSubs {
		x.sess.Close()
		x.ep.Close()
	}
	if p != nil {
		p.left.Store(true)
		p.stop()
		p.sess.Close()
		p.ep.Close()
		e.Log.Info("video room: a "+p.kind+" left", "room", number)
	}
	e.sfuChanged(number)
	return true
}

// SFURoomOf lists a room's cameras.
func (e *Engine) SFURoomOf(number string) (SFURoom, error) {
	if _, err := e.sfuRoomCfg(number); err != nil {
		return SFURoom{}, err
	}
	e.sfu.mu.Lock()
	defer e.sfu.mu.Unlock()
	return e.sfuRoomLocked(number), nil
}

func (e *Engine) sfuRoomLocked(number string) SFURoom {
	r := e.sfu.room(number)
	out := SFURoom{Number: number, Publishers: []SFUPublisher{}}
	for _, p := range r.pubs {
		p.mu.Lock()
		views := len(p.subs)
		p.mu.Unlock()
		out.Publishers = append(out.Publishers, SFUPublisher{ID: p.id, Name: p.name, Kind: p.kind, Codec: p.codec.Name, Layers: p.sc.RIDs,
			Live: p.last.Load() != 0 && time.Since(time.UnixMilli(p.last.Load())) < 3*time.Second, Since: p.since.UTC(), Views: views})
	}
	sort.Slice(out.Publishers, func(i, j int) bool { return out.Publishers[i].Since.Before(out.Publishers[j].Since) })
	return out
}

// sfuChanged tells screens a room's cameras changed (sfu.changed).
func (e *Engine) sfuChanged(number string) {
	e.sfu.mu.Lock()
	r := e.sfuRoomLocked(number)
	e.sfu.mu.Unlock()
	e.publish("sfu.changed", r)
}

// roomFor says whether a room takes one more camera (limit) or a shared screen (one at a time).
// Called with the hub locked.
func (r *sfuRoom) roomFor(kind string, limit int) error {
	cams := 0
	for _, p := range r.pubs {
		if p.kind == "screen" {
			if kind == "screen" {
				return ErrSFUScreenTaken
			}
			continue
		}
		cams++
	}
	if kind == "camera" && cams >= limit {
		return ErrSFUFull
	}
	return nil
}

// withoutExtensions is an RTP packet without its header extensions: a viewer's answer agrees on
// none, and the publisher's numbers belong to the publisher's session. Returns pkt itself when it
// has none.
func withoutExtensions(pkt []byte) []byte {
	if len(pkt) < 12 || pkt[0]&0x10 == 0 {
		return pkt
	}
	at := 12 + 4*int(pkt[0]&0x0f)
	if len(pkt) < at+4 {
		return pkt
	}
	end := at + 4 + 4*int(binary.BigEndian.Uint16(pkt[at+2:]))
	if end > len(pkt) {
		return pkt
	}
	out := make([]byte, 0, len(pkt)-(end-at))
	out = append(out, pkt[:at]...)
	out[0] &^= 0x10
	return append(out, pkt[end:]...)
}
