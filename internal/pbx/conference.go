package pbx

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A conference room: everyone who dials its number (and its PIN) talks together through one mixing
// bridge. A leader can mute others and lock the room, from the phone or the web interface.
type conferences struct {
	mu    sync.Mutex
	rooms map[string]*room // by room number, while anyone is in
}

type room struct {
	number, name string
	id           string // this session, for its call record and recording
	bridge       *media.Bridge
	started      time.Time
	opener       string // who came in first, for the record

	mu      sync.Mutex
	members map[string]*member // by member id
	waiting map[string]*member // waiting for a leader, hearing the hold tone
	locked  bool
	seq     int
	rec     []int16 // the room's recording so far (8 kHz), when it records
	recOn   bool
	video   *videoSwitch // a video room's picture switching
	vcodec  string
}

type member struct {
	id     string
	call   *Call
	number string
	name   string
	leader bool
	muted  bool
	hand   bool // asked to speak (a muted member pressed 1 in a room where members are muted)
	joined time.Time
	player *media.Player
}

// ConferenceRoom is a room in use, for the live view.
type ConferenceRoom struct {
	Number  string             `json:"number"`
	Name    string             `json:"name"`
	Started time.Time          `json:"started"`
	Locked  bool               `json:"locked"`
	Members []ConferenceMember `json:"members"`
	Waiting int                `json:"waiting"`
	Records bool               `json:"records"`
	HD      bool               `json:"hd,omitempty"` // mixed at 16 kHz
	// Video is the room's video mode (talker or leader), Showing the member everyone sees.
	Video   string `json:"video,omitempty"`
	Showing string `json:"showing,omitempty"`
}

// ConferenceMember is one person in a room.
type ConferenceMember struct {
	ID       string    `json:"id"`
	Number   string    `json:"number"`
	Name     string    `json:"name"`
	Leader   bool      `json:"leader"`
	Muted    bool      `json:"muted"`
	Hand     bool      `json:"hand"`
	Speaking bool      `json:"speaking"`
	Waiting  bool      `json:"waiting"`
	Joined   time.Time `json:"joined"`
	HD       bool      `json:"hd,omitempty"`    // in an HD room on a wideband codec: hears and is heard in HD
	Video    bool      `json:"video,omitempty"` // sends and receives pictures in a video room
}

// Errors from controlling a room.
var (
	ErrNoRoom   = errors.New("nobody is in that conference room")
	ErrNoMember = errors.New("that person is not in the room")
)

var (
	toneJoin  = media.Tone{Freqs: []float64{660, 880}, Level: 0.15, On: 150 * time.Millisecond, Off: 50 * time.Millisecond, Repeat: 1}
	toneLeave = media.Tone{Freqs: []float64{880, 660}, Level: 0.15, On: 150 * time.Millisecond, Off: 50 * time.Millisecond, Repeat: 1}
)

// Conferences lists the rooms in use.
func (e *Engine) Conferences() []ConferenceRoom {
	e.conf.mu.Lock()
	rooms := make([]*room, 0, len(e.conf.rooms))
	for _, r := range e.conf.rooms {
		rooms = append(rooms, r)
	}
	e.conf.mu.Unlock()
	out := []ConferenceRoom{}
	for _, r := range rooms {
		out = append(out, r.view())
	}
	slices.SortFunc(out, func(a, b ConferenceRoom) int { return compareNumbers(a.Number, b.Number) })
	return out
}

func (r *room) view() ConferenceRoom {
	r.mu.Lock()
	v := ConferenceRoom{Number: r.number, Name: r.name, Started: r.started, Locked: r.locked, Waiting: len(r.waiting), Records: r.recOn, HD: r.bridge.Wide()}
	for _, m := range r.members {
		hd := false
		if v.HD && m.call != nil && m.call.caller != nil && m.call.caller.ep != nil {
			hd = m.call.caller.ep.Wideband()
		}
		v.Members = append(v.Members, ConferenceMember{ID: m.id, Number: m.number, Name: m.name, Leader: m.leader,
			Muted: m.muted, Hand: m.hand, Joined: m.joined, HD: hd})
	}
	for _, m := range r.waiting {
		v.Members = append(v.Members, ConferenceMember{ID: m.id, Number: m.number, Name: m.name, Waiting: true, Joined: m.joined})
	}
	r.mu.Unlock()
	// The bridge's lock is taken only after the room's is released: the bridge's mixing holds its own
	// lock while it asks the room who is muted.
	for i := range v.Members {
		if !v.Members[i].Waiting {
			v.Members[i].Speaking = r.bridge.Speaking(v.Members[i].ID)
		}
	}
	if r.video != nil {
		sp, sees := r.video.watching()
		v.Video, v.Showing = r.video.mode, sp
		for i := range v.Members {
			_, v.Members[i].Video = sees[v.Members[i].ID]
		}
	}
	slices.SortFunc(v.Members, func(a, b ConferenceMember) int { return a.Joined.Compare(b.Joined) })
	return v
}

func (e *Engine) roomFor(number string) *room {
	e.conf.mu.Lock()
	defer e.conf.mu.Unlock()
	return e.conf.rooms[number]
}

// MuteConferenceMember mutes or unmutes someone in a room (a leader's or an administrator's choice).
func (e *Engine) MuteConferenceMember(number, id string, muted bool) error {
	r := e.roomFor(number)
	if r == nil {
		return ErrNoRoom
	}
	r.mu.Lock()
	m := r.members[id]
	if m != nil {
		m.muted = muted
		if !muted {
			m.hand = false
		}
	}
	r.mu.Unlock()
	if m == nil {
		return ErrNoMember
	}
	e.publish("conference.updated", r.view())
	return nil
}

// RemoveConferenceMember hangs up on someone in a room.
func (e *Engine) RemoveConferenceMember(number, id string) error {
	r := e.roomFor(number)
	if r == nil {
		return ErrNoRoom
	}
	r.mu.Lock()
	m := r.members[id]
	if m == nil {
		m = r.waiting[id]
	}
	r.mu.Unlock()
	if m == nil {
		return ErrNoMember
	}
	m.call.hangup("removed from the conference", "system")
	return nil
}

// LockConference stops (or again allows) anyone new joining a room.
func (e *Engine) LockConference(number string, locked bool) error {
	r := e.roomFor(number)
	if r == nil {
		return ErrNoRoom
	}
	r.mu.Lock()
	r.locked = locked
	r.mu.Unlock()
	e.publish("conference.updated", r.view())
	return nil
}

// joinConference asks for the room's PIN when it has one, then puts the caller in the room.
func (c *Call) joinConference(number string) {
	e := c.e
	cfg := e.Config()
	rc := cfg.FindConference(number)
	if rc == nil {
		c.hangup("no such conference room", "system")
		return
	}
	leader := false
	if rc.PIN != "" || rc.LeaderPIN != "" {
		keys := media.NewCapture(1)
		c.caller.ep.Record(keys.Packet)
		ok := false
		c.play(make([]int16, media.Rate/4), nil)
		for try := 0; try < 3 && !ok; try++ {
			if _, ended := c.play(media.ToneSamples(toneNext), nil); ended {
				c.caller.ep.Record(nil)
				return
			}
			pin := c.collectEntry(keys.Keys)
			switch {
			case rc.LeaderPIN != "" && pin == rc.LeaderPIN:
				ok, leader = true, true
			case pin == rc.PIN:
				ok = true // with no member PIN, members press # alone
			default:
				c.play(media.ToneSamples(media.ToneOff), nil)
			}
		}
		c.caller.ep.Record(nil)
		if !ok {
			c.hangup("wrong conference PIN", "system")
			return
		}
	}
	c.mu.Lock()
	name, num := c.fromName, c.from
	c.mu.Unlock()
	if name == "" {
		name = num
	}
	e.conf.mu.Lock()
	if e.conf.rooms == nil {
		e.conf.rooms = map[string]*room{}
	}
	r := e.conf.rooms[number]
	if r == nil {
		r = &room{number: number, name: rc.Name, id: "conf-" + config.NewID(), bridge: newRoomBridge(rc.HD), started: time.Now(),
			opener: num, members: map[string]*member{}, waiting: map[string]*member{}}
		r.bridge.SetHearing(r.hears)
		if rc.Video != "" {
			r.video, r.vcodec = newVideoSwitch(r, rc.Video), rc.VideoCodec
		}
		if rc.Record && e.Recordings != nil && shouldRecordRoom(cfg) {
			r.recOn = true
			r.bridge.OnMix(func(f []int16) {
				r.mu.Lock()
				if len(r.rec) < maxRecordSeconds*media.Rate {
					r.rec = append(r.rec, f...)
				}
				r.mu.Unlock()
			})
		}
		e.conf.rooms[number] = r
	}
	e.conf.mu.Unlock()

	r.mu.Lock()
	if r.locked && !leader {
		r.mu.Unlock()
		c.play(media.ToneSamples(media.ToneOff), nil)
		c.hangup("the conference room is locked", "system")
		e.closeRoomIfEmpty(r)
		return
	}
	if len(r.members)+len(r.waiting) >= rc.MaxMembers {
		r.mu.Unlock()
		c.play(media.ToneSamples(media.ToneOff), nil)
		c.hangup("the conference room is full", "system")
		e.closeRoomIfEmpty(r)
		return
	}
	r.seq++
	m := &member{id: strconv.Itoa(r.seq), call: c, number: num, name: name, leader: leader,
		muted: rc.MuteMembers && !leader, joined: time.Now()}
	waitForLeader := rc.WaitForLeader && !leader && !r.hasLeader()
	r.mu.Unlock()

	c.mu.Lock()
	c.answeredBy = "Conference " + rc.Name
	c.mu.Unlock()
	c.onEnded(func() { e.leaveConference(r, m) })
	if waitForLeader {
		r.mu.Lock()
		r.waiting[m.id] = m
		r.mu.Unlock()
		cfg := e.Config()
		m.player = e.startHold(cfg, c.caller, holdClass(cfg), media.ToneHold)
	} else {
		r.enter(m, !rc.Quiet)
		if leader && rc.WaitForLeader {
			r.admitWaiting(!rc.Quiet)
		}
	}
	c.caller.ep.OnDTMF(func(k byte) { e.conferenceKey(r, m, k) })
	e.publish("conference.updated", r.view())
}

// hears: nobody hears a muted member. Called with the bridge's lock held, so it reads r.members
// under r.mu only (never the bridge).
func (r *room) hears(listener, speaker string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.members[speaker]
	return m == nil || !m.muted
}

// hasLeader says whether a leader is in the room. Called with r.mu held.
func (r *room) hasLeader() bool {
	for _, m := range r.members {
		if m.leader {
			return true
		}
	}
	return false
}

// enter puts a member in the bridge, with the join tone for everyone else.
func (r *room) enter(m *member, tone bool) {
	if tone {
		r.bridge.Inject(media.ToneSamples(toneJoin))
	}
	r.mu.Lock()
	r.members[m.id] = m
	delete(r.waiting, m.id)
	r.mu.Unlock()
	if m.player != nil {
		m.player.Stop()
		m.player = nil
	}
	connect(m.call.caller, nil)
	r.bridge.Add(m.id, m.call.caller.ep)
	if r.video != nil {
		r.video.add(m, r.vcodec)
	}
}

// admitWaiting lets everyone waiting for a leader in.
func (r *room) admitWaiting(tone bool) {
	r.mu.Lock()
	ws := make([]*member, 0, len(r.waiting))
	for _, m := range r.waiting {
		ws = append(ws, m)
	}
	r.mu.Unlock()
	for _, m := range ws {
		r.enter(m, false)
	}
	if tone && len(ws) > 0 {
		r.bridge.Inject(media.ToneSamples(toneJoin))
	}
}

// conferenceKey handles a key pressed in a room: 1 mutes and unmutes yourself (or asks to speak when
// members are muted), and a leader's 2 locks and unlocks the room.
func (e *Engine) conferenceKey(r *room, m *member, k byte) {
	rc := e.Config().FindConference(r.number)
	r.mu.Lock()
	if r.members[m.id] == nil {
		r.mu.Unlock()
		return
	}
	switch k {
	case '1':
		if rc != nil && rc.MuteMembers && !m.leader && m.muted {
			m.hand = !m.hand
		} else {
			m.muted = !m.muted
		}
	case '2':
		if m.leader {
			r.locked = !r.locked
		}
	}
	r.mu.Unlock()
	e.publish("conference.updated", r.view())
}

// leaveConference takes a member out when their call ends; the last one out closes the room, and so
// does the last leader leaving a room that waits for leaders.
func (e *Engine) leaveConference(r *room, m *member) {
	r.mu.Lock()
	_, in := r.members[m.id]
	delete(r.members, m.id)
	delete(r.waiting, m.id)
	endAll := false
	if in && m.leader && !r.hasLeader() {
		if rc := e.Config().FindConference(r.number); rc != nil && rc.WaitForLeader {
			endAll = true
		}
	}
	rest := make([]*member, 0, len(r.members)+len(r.waiting))
	for _, x := range r.members {
		rest = append(rest, x)
	}
	for _, x := range r.waiting {
		rest = append(rest, x)
	}
	r.mu.Unlock()
	if m.player != nil {
		m.player.Stop()
	}
	if r.video != nil {
		r.video.remove(m.id)
	}
	if in {
		r.bridge.Remove(m.id)
		if rc := e.Config().FindConference(r.number); rc == nil || !rc.Quiet {
			r.bridge.Inject(media.ToneSamples(toneLeave))
		}
	}
	if endAll {
		for _, x := range rest {
			x.call.hangup("the conference leader left", "system")
		}
	}
	e.closeRoomIfEmpty(r)
	e.publish("conference.updated", r.view())
}

// closeRoomIfEmpty ends a room nobody is in: its bridge stops and its recording is kept.
func (e *Engine) closeRoomIfEmpty(r *room) {
	r.mu.Lock()
	empty := len(r.members) == 0 && len(r.waiting) == 0
	r.mu.Unlock()
	if !empty {
		return
	}
	e.conf.mu.Lock()
	if e.conf.rooms[r.number] != r {
		e.conf.mu.Unlock()
		return
	}
	delete(e.conf.rooms, r.number)
	e.conf.mu.Unlock()
	r.bridge.OnMix(nil)
	r.bridge.Close()
	if r.video != nil {
		r.video.close()
	}
	r.mu.Lock()
	pcm := r.rec
	r.rec = nil
	r.mu.Unlock()
	name := ""
	if len(pcm) > media.Rate && e.Recordings != nil { // at least a second
		if n, err := e.Recordings.AddRecording(r.id, r.started, media.EncodeWAV(pcm)); err == nil {
			name = n
		} else {
			e.Log.Error("could not keep the conference recording", "room", r.number, "err", err)
		}
	}
	if e.Recorder != nil {
		end := time.Now()
		_ = e.Recorder.AddCDR(store.CDR{ID: r.id, Start: r.started.UTC(), Answer: r.started.UTC(), End: end.UTC(),
			Direction: "conference", From: r.opener, To: r.number, ToName: "Conference " + r.name,
			AnsweredBy: "Conference " + r.name, Disposition: "answered", Recording: name,
			Duration: int(end.Sub(r.started).Seconds()), Billsec: int(end.Sub(r.started).Seconds())})
	}
	e.publish("conference.ended", map[string]string{"number": r.number})
}

// shouldRecordRoom: a room set to record is recorded unless recording is off for the whole system.
func shouldRecordRoom(cfg *config.Config) bool { return cfg.System.Recording.Mode != "off" }

// onEnded runs f when the call ends.
func (c *Call) onEnded(f func()) {
	c.mu.Lock()
	c.onEnd = append(c.onEnd, f)
	c.mu.Unlock()
}

// newRoomBridge mixes a conference room, at 16 kHz for an HD room.
func newRoomBridge(hd bool) *media.Bridge {
	if hd {
		return media.NewWideBridge(nil)
	}
	return media.NewBridge(nil)
}
