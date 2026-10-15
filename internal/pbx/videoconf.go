package pbx

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A video room switches pictures, as Asterisk's ConfBridge does: every member sends one picture and
// receives one, never decoded. In "talker" rooms everyone sees whoever is speaking, and the speaker
// sees the one before; in "leader" rooms everyone sees the leader, and the leader sees whoever spoke
// last. On each switch the new source is asked for a full picture (RTCP PLI and FIR, and SIP INFO
// picture_fast_update, which most desk phones answer), and each viewer's stream is rewritten (one SSRC,
// continuous sequence numbers and timestamps, its own payload type) so phones see one steady stream.

type videoSwitch struct {
	mode string // talker, leader or grid
	room *room

	// grid rooms: ffmpeg makes one picture of everyone (videogrid.go).
	ffmpeg, codec string
	log           func(msg string, args ...any)
	grid          *media.VideoMixer
	gridGen       int
	gridIDs       []string
	gridAt        time.Time
	gridWant      []string
	gridWantSince time.Time
	joins         int

	mu       sync.Mutex
	members  map[string]*vmember
	speaker  string         // the picture everyone sees
	prev     string         // the picture the speaker sees
	heard    map[string]int // ticks each member has been speaking in a row
	silent   int            // ticks the speaker has been quiet
	lastPLI  map[string]time.Time
	stop     chan struct{}
	switches int
}

type vmember struct {
	id      string
	leg     *Leg
	ep      *media.Endpoint
	pt      int    // this member's payload type for the room's codec
	ssrc    uint32 // this member's own SSRC, as seen
	leader  bool
	joined  int       // order of joining, for the grid's places
	lastVid time.Time // when its last picture packet came
	tile    int       // its place in the grid, -1 when not shown

	// What this member is sent: one SSRC, sequence and timestamp carried across switches.
	outSSRC        uint32
	src            string // whose picture it is getting now
	seqOff, tsOff  uint32
	lastSeq, lastT uint32
	started        bool
	switched       bool // the next packet starts a new source
	sentAt         time.Time
}

const (
	videoTick       = 200 * time.Millisecond
	videoTakeFloor  = 5 // ticks a member must speak to take the picture (1 s)
	videoQuietTicks = 5 // ticks the speaker must be quiet first
)

func newVideoSwitch(r *room, mode, codec, ffmpeg string, log func(string, ...any)) *videoSwitch {
	if mode == "grid" && !media.FFmpegHas(ffmpeg) {
		mode = "talker" // without ffmpeg a grid cannot be made: follow the speaker instead
		if log != nil {
			log("video room shows the speaker: a grid needs ffmpeg, which is not installed", "room", r.number)
		}
	}
	v := &videoSwitch{mode: mode, room: r, members: map[string]*vmember{}, heard: map[string]int{},
		lastPLI: map[string]time.Time{}, stop: make(chan struct{}), ffmpeg: ffmpeg, codec: codec, log: log}
	go v.run()
	return v
}

func randSSRC() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:]) | 1
}

// add joins a member's video (when its call carries video in the room's codec).
func (v *videoSwitch) add(m *member, codec string) {
	leg := m.call.caller
	ep := leg.videoEP()
	if ep == nil {
		return
	}
	pt := -1
	leg.mu.Lock()
	s := leg.remoteSDP
	leg.mu.Unlock()
	if vm := videoMedia(s); vm != nil {
		for _, c := range vm.Codecs() {
			if strings.EqualFold(c.Name, codec) {
				pt = c.PT
				break
			}
		}
	}
	if pt < 0 {
		return
	}
	x := &vmember{id: m.id, leg: leg, ep: ep, pt: pt, leader: m.leader, outSSRC: randSSRC(), tile: -1}
	media.Connect(ep, nil)
	ep.SetTap(func(pkt []byte) { v.packet(x, pkt) })
	ep.OnRTCP(func(pkt []byte) { v.rtcp(x, pkt) })
	leg.call.mu.Lock()
	leg.call.keyframe = func() { v.wantPicture(x) }
	leg.call.mu.Unlock()
	v.mu.Lock()
	v.joins++
	x.joined = v.joins
	v.members[m.id] = x
	if v.mode == "grid" {
		v.retarget()
		v.mu.Unlock()
		v.askPicture(m.id)
		return
	}
	if v.speaker == "" || (v.mode == "leader" && x.leader && !v.isLeader(v.speaker)) {
		v.prev, v.speaker = v.speaker, m.id
	} else if v.prev == "" {
		v.prev = m.id
	}
	v.retarget()
	v.mu.Unlock()
	v.askPicture(v.speaker)
	v.askPicture(v.prev)
}

func (v *videoSwitch) isLeader(id string) bool {
	m := v.members[id]
	return m != nil && m.leader
}

// remove takes a member's video out.
func (v *videoSwitch) remove(id string) {
	v.mu.Lock()
	x := v.members[id]
	delete(v.members, id)
	delete(v.heard, id)
	if x != nil {
		x.ep.SetTap(nil)
		x.ep.OnRTCP(nil)
	}
	if v.mode == "grid" {
		v.mu.Unlock()
		return // the next tick makes the grid without it
	}
	if v.speaker == id || v.prev == id {
		if v.speaker == id {
			v.speaker = v.prev
		}
		v.prev = ""
		for k := range v.members {
			if k != v.speaker {
				v.prev = k
				break
			}
		}
		if v.mode == "leader" {
			for k, m := range v.members {
				if m.leader {
					if v.speaker != k {
						v.prev, v.speaker = v.speaker, k
					}
					break
				}
			}
		}
	}
	v.retarget()
	sp, pr := v.speaker, v.prev
	v.mu.Unlock()
	v.askPicture(sp)
	v.askPicture(pr)
}

func (v *videoSwitch) close() {
	select {
	case <-v.stop:
	default:
		close(v.stop)
	}
	v.mu.Lock()
	for _, x := range v.members {
		x.ep.SetTap(nil)
		x.ep.OnRTCP(nil)
	}
	g := v.grid
	v.grid = nil
	v.mu.Unlock()
	if g != nil {
		g.Close()
	}
}

// retarget points each member at the picture it should get. Called with v.mu held.
func (v *videoSwitch) retarget() {
	if v.mode == "grid" {
		for _, x := range v.members {
			if x.src != "grid" {
				x.src, x.switched = "grid", true
			}
		}
		return
	}
	for id, x := range v.members {
		want := v.speaker
		if id == v.speaker {
			want = v.prev
		}
		if want == id {
			want = ""
		}
		if x.src != want {
			x.src, x.switched = want, true
		}
	}
}

// run follows who speaks, from the room's own mixer.
func (v *videoSwitch) run() {
	t := time.NewTicker(videoTick)
	defer t.Stop()
	for {
		select {
		case <-v.stop:
			return
		case <-t.C:
		}
		if v.mode == "grid" {
			v.gridTick()
			continue
		}
		v.mu.Lock()
		ids := make([]string, 0, len(v.members))
		for id := range v.members {
			ids = append(ids, id)
		}
		v.mu.Unlock()
		speaking := map[string]bool{}
		for _, id := range ids {
			speaking[id] = v.room.bridge.Speaking(id) // outside v.mu: the bridge has its own lock
		}
		v.mu.Lock()
		next := ""
		best := 0
		for _, id := range ids {
			if speaking[id] {
				v.heard[id]++
			} else {
				v.heard[id] = 0
			}
			if id != v.speaker && v.heard[id] >= videoTakeFloor && v.heard[id] > best {
				next, best = id, v.heard[id]
			}
		}
		if speaking[v.speaker] {
			v.silent = 0
		} else {
			v.silent++
		}
		var ask []string
		switch {
		case next == "":
		case v.mode == "leader" && v.isLeader(v.speaker):
			// The leader keeps the picture; the leader watches whoever speaks.
			if v.prev != next && !v.isLeader(next) {
				v.prev = next
				v.retarget()
				ask = append(ask, next)
			}
		case v.silent >= videoQuietTicks || v.members[v.speaker] == nil:
			v.prev, v.speaker = v.speaker, next
			v.silent = 0
			v.switches++
			v.retarget()
			ask = append(ask, next, v.prev)
		}
		v.mu.Unlock()
		for _, id := range ask {
			v.askPicture(id)
		}
	}
}

// packet passes a member's picture on to those watching it.
func (v *videoSwitch) packet(from *vmember, pkt []byte) {
	if len(pkt) < 12 {
		return
	}
	if from.ssrc == 0 {
		v.mu.Lock()
		from.ssrc = binary.BigEndian.Uint32(pkt[8:])
		v.mu.Unlock()
	}
	if v.mode == "grid" {
		v.mu.Lock()
		from.lastVid = time.Now()
		g, tile := v.grid, from.tile
		v.mu.Unlock()
		if g != nil && tile >= 0 {
			g.Write(tile, pkt)
		}
		return
	}
	v.forward(from.id, from, pkt)
}

// forward sends a picture's packet to every member watching src (from, when a member, never gets
// its own), rewritten so each sees one steady stream.
func (v *videoSwitch) forward(src string, from *vmember, pkt []byte) {
	seq := uint32(binary.BigEndian.Uint16(pkt[2:]))
	ts := binary.BigEndian.Uint32(pkt[4:])
	now := time.Now()
	v.mu.Lock()
	var outs []*media.Endpoint
	var bufs [][]byte
	for _, x := range v.members {
		if x.src != src || (from != nil && x == from) {
			continue
		}
		if x.switched || !x.started {
			// A new source: carry on the viewer's sequence, and its clock by the time that has passed.
			nextSeq := uint32(0)
			nextT := uint32(0)
			if x.started {
				nextSeq = x.lastSeq + 1
				gap := uint32(now.Sub(x.sentAt) / (time.Second / 90000))
				nextT = x.lastT + max(gap, 1)
			}
			x.seqOff = nextSeq - seq
			x.tsOff = nextT - ts
			x.switched, x.started = false, true
		}
		out := append([]byte(nil), pkt...)
		oseq := (seq + x.seqOff) & 0xFFFF
		ots := ts + x.tsOff
		binary.BigEndian.PutUint16(out[2:], uint16(oseq))
		binary.BigEndian.PutUint32(out[4:], ots)
		binary.BigEndian.PutUint32(out[8:], x.outSSRC)
		out[1] = out[1]&0x80 | byte(x.pt)
		x.lastSeq, x.lastT, x.sentAt = oseq, ots, now
		outs = append(outs, x.ep)
		bufs = append(bufs, out)
	}
	v.mu.Unlock()
	for i, ep := range outs {
		ep.SendRTP(bufs[i])
	}
}

// rtcp: a viewer asking for a full picture (PLI or FIR) has it asked of the source it watches.
func (v *videoSwitch) rtcp(from *vmember, pkt []byte) {
	for p := 0; p+4 <= len(pkt); {
		pt, fmtv := pkt[p+1], pkt[p]&0x1F
		n := (int(binary.BigEndian.Uint16(pkt[p+2:])) + 1) * 4
		if pt == 206 && (fmtv == 1 || fmtv == 4) {
			v.wantPicture(from)
			return
		}
		p += n
	}
}

// wantPicture asks for a full picture from the source a member watches.
func (v *videoSwitch) wantPicture(x *vmember) {
	v.mu.Lock()
	src := x.src
	v.mu.Unlock()
	v.askPicture(src)
}

// askPicture asks a member for a full picture, at most once a second: RTCP PLI and FIR, and SIP INFO.
func (v *videoSwitch) askPicture(id string) {
	if id == "" {
		return
	}
	v.mu.Lock()
	x := v.members[id]
	if x == nil || time.Since(v.lastPLI[id]) < time.Second {
		v.mu.Unlock()
		return
	}
	v.lastPLI[id] = time.Now()
	ssrc, sender := x.ssrc, x.outSSRC
	v.mu.Unlock()
	pli := make([]byte, 12)
	pli[0], pli[1] = 0x81, 206 // PSFB, FMT 1 (RFC 4585 6.3.1)
	binary.BigEndian.PutUint16(pli[2:], 2)
	binary.BigEndian.PutUint32(pli[4:], sender)
	binary.BigEndian.PutUint32(pli[8:], ssrc)
	fir := make([]byte, 20)
	fir[0], fir[1] = 0x84, 206 // PSFB, FMT 4 (RFC 5104 4.3.1)
	binary.BigEndian.PutUint16(fir[2:], 4)
	binary.BigEndian.PutUint32(fir[4:], sender)
	binary.BigEndian.PutUint32(fir[12:], ssrc)
	v.mu.Lock()
	v.switches++
	fir[16] = byte(v.switches)
	v.mu.Unlock()
	x.ep.SendRTCP(append(pli, fir...))
	go func() {
		m := x.leg.newRequest(sip.INFO)
		m.Set("Content-Type", "application/media_control+xml")
		m.Body = []byte(`<?xml version="1.0" encoding="utf-8" ?><media_control><vc_primitive><to_encoder><picture_fast_update/></to_encoder></vc_primitive></media_control>`)
		x.leg.request(m)
	}()
}

// watching says whose picture each member gets, for the live view and tests.
func (v *videoSwitch) watching() (speaker string, sees map[string]string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	sees = map[string]string{}
	for id, x := range v.members {
		sees[id] = x.src
	}
	if v.mode == "grid" {
		return "", sees
	}
	return v.speaker, sees
}
