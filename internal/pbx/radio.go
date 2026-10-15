package pbx

import (
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The radio dispatch console: every radio channel (a RoIP gateway sending the channel's receive
// audio as RTP, and keying the radio when audio comes back) and every dispatcher who has dialled
// the console number are parties of one mixing bridge. A dispatcher hears the channels they
// monitor; a channel hears a dispatcher only while that dispatcher holds push-to-talk on it, and
// hears another channel while the two are patched.

type radio struct {
	mu        sync.Mutex
	bridge    *media.Bridge
	channels  map[string]*radioChannel // by channel ID
	positions map[string]*position     // by call ID
	patches   map[[2]string]bool       // channel ID pairs, sorted
	issi      *sip.Stack               // the ISSI's SIP stack, for talkgroup channels
	issiPort  int
	p25cfg    config.P25Console
	record    bool             // keep every transmission (System.Radio.Record)
	recOnce   sync.Once        // the recorder's goroutine
	fixes     map[int]RadioFix // each radio's last position, by unit ID
}

type radioChannel struct {
	cfg  config.RadioChannel
	ep   *media.Endpoint
	last time.Time // when receive audio last came in
	tx   string    // the position transmitting on it, if any
	txAt time.Time
	err  string
	p25  *p25Channel // a P25 talkgroup or private call (nil: a RoIP gateway)
	// A private (unit-to-unit) call is a channel of its own while it lasts, not in the settings.
	tone      bool // an alert tone is playing
	toneKind  string
	rec       *chRec // transmissions being recorded
	private   bool
	unit      uint32
	incoming  bool
	emergency bool
	notice    string // the last thing to tell the dispatcher (a push-to-talk not granted)
	// An emergency stays raised on the console, after the radio stops talking, until a dispatcher
	// acknowledges it.
	emUnit  string
	emAlias string
	emAt    time.Time
	ackBy   string
	ackAt   time.Time
}

type position struct {
	call    string
	ext     string
	name    string
	monitor map[string]bool // channels heard (all when nil)
	ptt     map[string]bool // channels being transmitted on
}

// ChannelState is a channel as the console shows it.
type ChannelState struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Color   string    `json:"color"`
	Receive bool      `json:"receive"` // audio coming in now
	LastRx  time.Time `json:"lastRx,omitzero"`
	TxBy    string    `json:"txBy,omitempty"` // the dispatcher transmitting (extension)
	Patched []string  `json:"patched"`
	Problem string    `json:"problem,omitempty"`
	Kind    string    `json:"kind"`
	// Private calls: the radio's unit ID, and whether it called in.
	Private   bool `json:"private,omitempty"`
	Unit      int  `json:"unit,omitempty"`
	Incoming  bool `json:"incoming,omitempty"`
	Emergency bool `json:"emergency,omitempty"`
	Tone      bool `json:"tone,omitempty"` // an alert tone is going out
	// P25 talkgroups: whether the group call is up, who is talking (a radio's unit ID), and
	// whether the dispatcher's request for the floor is still waiting.
	Linked  bool   `json:"linked"`
	Talker  string `json:"talker,omitempty"`
	Waiting bool   `json:"waiting,omitempty"`
	Notice  string `json:"notice,omitempty"`
	// Encryption: of the transmission heard now (and whether this console lacks its key), and of
	// what dispatchers send.
	// TalkerName is the talking radio's alias, when it has one.
	TalkerName string `json:"talkerName,omitempty"`
	// EmergencyUnit is a radio's emergency still waiting to be acknowledged (since EmergencyAt);
	// AckBy and AckAt say who acknowledged the last one, and when.
	EmergencyUnit  string    `json:"emergencyUnit,omitempty"`
	EmergencyAlias string    `json:"emergencyAlias,omitempty"`
	EmergencyAt    time.Time `json:"emergencyAt,omitzero"`
	AckBy          string    `json:"ackBy,omitempty"`
	AckAt          time.Time `json:"ackAt,omitzero"`
	Encryption     string    `json:"encryption,omitempty"`
	NoKey          bool      `json:"noKey,omitempty"`
	TxEncrypt      string    `json:"txEncryption,omitempty"`
}

// PositionState is a dispatcher joined to the console.
type PositionState struct {
	Call    string   `json:"call"`
	Ext     string   `json:"ext"`
	Name    string   `json:"name"`
	Monitor []string `json:"monitor"`
	PTT     []string `json:"ptt"`
}

// ConsoleState is the whole console.
type ConsoleState struct {
	Channels  []ChannelState    `json:"channels"`
	Positions []PositionState   `json:"positions"`
	Tones     map[string]string `json:"tones"` // the alert tones there are, by name
}

func pairOf(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (e *Engine) radioSys() *radio {
	e.radioOnce.Do(func() {
		r := &radio{channels: map[string]*radioChannel{}, positions: map[string]*position{}, patches: map[[2]string]bool{}}
		r.bridge = media.NewBridge(r.hears)
		e.radio = r
	})
	return e.radio
}

// hears is the console's mixing rule. Party IDs are "ch:<id>" and "pos:<call>".
func (r *radio) hears(listener, speaker string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	lk, lid, _ := strings.Cut(listener, ":")
	sk, sid, _ := strings.Cut(speaker, ":")
	switch {
	case lk == "pos" && sk == "ch":
		p := r.positions[lid]
		return p != nil && (p.monitor == nil || p.monitor[sid])
	case lk == "ch" && sk == "pos":
		p := r.positions[sid]
		return p != nil && p.ptt[lid]
	case lk == "ch" && sk == "ch":
		return r.patches[pairOf(lid, sid)]
	case lk == "ch" && sk == "tone":
		return lid == sid // an alert tone goes out on its channel
	case lk == "pos" && sk == "tone":
		p := r.positions[lid]
		return p != nil && (p.monitor == nil || p.monitor[sid]) // and dispatchers hear it go
	}
	return false // dispatchers do not hear each other here; they have the phone for that
}

// applyRadio opens and closes channel endpoints to match the configuration.
func (e *Engine) applyRadio(cfg *config.Config) {
	r := e.radioSys()
	want := map[string]config.RadioChannel{}
	for _, ch := range cfg.System.Radio.Channels {
		want[ch.ID] = ch
	}
	r.mu.Lock()
	var closing []*radioChannel
	for id, ch := range r.channels {
		if ch.private {
			continue // ends when the call does
		}
		w, ok := want[id]
		same := ok && w.Port == ch.cfg.Port && w.Gateway == ch.cfg.Gateway && w.Kind == ch.cfg.Kind && w.Group == ch.cfg.Group &&
			w.Home == ch.cfg.Home && w.HomeRFSS == ch.cfg.HomeRFSS && w.TxKey == ch.cfg.TxKey && (w.Kind != "p25" || reflect.DeepEqual(cfg.System.Radio.P25, r.p25cfg))
		if !same {
			closing = append(closing, ch)
			delete(r.channels, id)
		} else {
			ch.cfg = w // a new name or colour
		}
	}
	var opening []config.RadioChannel
	for id, w := range want {
		if _, ok := r.channels[id]; !ok {
			opening = append(opening, w)
		}
	}
	r.mu.Unlock()
	r.mu.Lock()
	r.p25cfg = cfg.System.Radio.P25
	r.record = cfg.System.Radio.Record
	r.mu.Unlock()
	if e.ctx != nil {
		r.recOnce.Do(func() { go e.radioRecorder(r) })
	}
	if pc := cfg.System.Radio.P25; pc.UnitHome != "" && pc.WACN != "" && e.ctx != nil {
		if _, err := e.issiStack(r, pc); err != nil { // private calls arrive even with no talkgroup
			e.Log.Warn("ISSI port unavailable", "port", pc.SIPPort, "err", err)
		}
	}
	for _, ch := range closing {
		r.bridge.Remove("ch:" + ch.cfg.ID)
		if ch.p25 != nil {
			ch.p25.close()
		}
		if ch.ep != nil {
			ch.ep.Close()
		}
	}
	for _, w := range opening {
		ch := &radioChannel{cfg: w}
		if w.Kind == "p25" {
			if err := e.openP25(r, ch, cfg); err != nil {
				ch.err = err.Error()
				e.Log.Warn("radio channel unavailable", "channel", w.Name, "err", err)
			}
			r.mu.Lock()
			r.channels[w.ID] = ch
			r.mu.Unlock()
			continue
		}
		ep, err := media.ListenAt(e.pool.Host, w.Port)
		if err != nil {
			ch.err = "port " + itoaInt(w.Port) + " cannot be opened: " + err.Error()
			e.Log.Warn("radio channel unavailable", "channel", w.Name, "err", err)
		} else if ap, err := netip.ParseAddrPort(w.Gateway); err != nil {
			ch.err = "the gateway address is not an IP address and port"
			ep.Close()
			ep = nil
		} else {
			ep.SetRemote(ap.Addr().String(), int(ap.Port()))
			ep.SetPayloadType(media.PTPCMU)
			id := w.ID
			ep.OnPacket(func() {
				r.mu.Lock()
				if c := r.channels[id]; c != nil {
					c.last = time.Now()
				}
				r.mu.Unlock()
			})
			e.recordChannel(r, w.ID, ep)
			ep.Start()
			r.bridge.AddQuiet("ch:"+w.ID, ep)
		}
		ch.ep = ep
		r.mu.Lock()
		r.channels[w.ID] = ch
		r.mu.Unlock()
	}
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// joinConsole puts a dispatcher's answered call into the console until it ends.
func (c *Call) joinConsole() {
	r := c.e.radioSys()
	a := c.caller
	p := &position{call: c.ID, ext: a.Ext, name: a.Name, ptt: map[string]bool{}}
	r.mu.Lock()
	r.positions[c.ID] = p
	r.mu.Unlock()
	r.bridge.Add("pos:"+c.ID, a.ep)
	c.e.publish("radio.updated", nil)
	for !c.isEnded() {
		time.Sleep(200 * time.Millisecond)
	}
	r.bridge.Remove("pos:" + c.ID)
	r.mu.Lock()
	delete(r.positions, c.ID)
	for _, ch := range r.channels {
		if ch.tx == c.ID {
			ch.tx = ""
		}
	}
	r.mu.Unlock()
	c.e.publish("radio.updated", nil)
}

// ErrNoPosition is a console request for a call that is not on the console.
var ErrNoPosition = errors.New("that call is not on the radio console")

// PTT keys or unkeys a channel for a dispatcher. One dispatcher transmits on a channel at a time:
// a second one is refused until the first lets go.
func (e *Engine) PTT(call, channel string, on bool) error {
	r := e.radioSys()
	r.mu.Lock()
	defer func() { r.mu.Unlock(); e.publish("radio.updated", nil) }()
	p := r.positions[call]
	if p == nil {
		return ErrNoPosition
	}
	ch := r.channels[channel]
	if ch == nil {
		return errors.New("no such channel")
	}
	if on {
		if ch.tx != "" && ch.tx != call {
			return errors.New(ch.cfg.Name + " is in use by another dispatcher")
		}
		ch.tx, ch.txAt, ch.notice = call, time.Now(), ""
		p.ptt[channel] = true
		if ch.p25 != nil {
			if err := ch.p25.group.Key(true); err != nil {
				ch.tx = ""
				delete(p.ptt, channel)
				return err
			}
		}
		return nil
	}
	if ch.tx == call && !ch.tone { // an alert tone keeps the channel until it ends
		ch.tx = ""
		if ch.p25 != nil {
			_ = ch.p25.group.Key(false)
		}
	}
	delete(p.ptt, channel)
	return nil
}

// Monitor chooses which channels a dispatcher hears (nil: all of them).
func (e *Engine) Monitor(call string, channels []string) error {
	r := e.radioSys()
	r.mu.Lock()
	defer func() { r.mu.Unlock(); e.publish("radio.updated", nil) }()
	p := r.positions[call]
	if p == nil {
		return ErrNoPosition
	}
	if channels == nil {
		p.monitor = nil
		return nil
	}
	p.monitor = map[string]bool{}
	for _, c := range channels {
		p.monitor[c] = true
	}
	return nil
}

// Patch joins two channels (each hears the other) or parts them.
func (e *Engine) Patch(a, b string, on bool) error {
	r := e.radioSys()
	r.mu.Lock()
	defer func() { r.mu.Unlock(); e.publish("radio.updated", nil) }()
	if a == b || r.channels[a] == nil || r.channels[b] == nil {
		return errors.New("choose two different channels")
	}
	if on {
		r.patches[pairOf(a, b)] = true
	} else {
		delete(r.patches, pairOf(a, b))
	}
	return nil
}

// Console is the console's state now.
func (e *Engine) Console() ConsoleState {
	r := e.radioSys()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := ConsoleState{Channels: []ChannelState{}, Positions: []PositionState{}, Tones: AlertTones}
	for _, ch := range r.channels {
		s := ChannelState{ID: ch.cfg.ID, Name: ch.cfg.Name, Color: ch.cfg.Color, LastRx: ch.last,
			Receive: !ch.last.IsZero() && time.Since(ch.last) < 400*time.Millisecond, Patched: []string{}, Problem: ch.err}
		if p := r.positions[ch.tx]; p != nil {
			s.TxBy = p.ext
		}
		s.Kind, s.Notice = ch.cfg.Kind, ch.notice
		s.Private, s.Unit, s.Incoming, s.Emergency, s.Tone = ch.private, int(ch.unit), ch.incoming, ch.emergency, ch.tone
		if s.Kind == "" {
			s.Kind = "roip"
		}
		s.EmergencyUnit, s.EmergencyAlias, s.EmergencyAt, s.AckBy, s.AckAt = ch.emUnit, ch.emAlias, ch.emAt, ch.ackBy, ch.ackAt
		if c := ch.p25; c != nil {
			s.Linked, s.Talker = c.up, c.talker
			s.TalkerName = c.talkerName
			s.Encryption, s.NoKey, s.TxEncrypt = c.enc, c.noKey, c.txEnc
			s.Waiting = ch.tx != "" && !c.group.Transmitting()
			if !c.up && c.why != "" && s.Problem == "" {
				s.Problem = c.why
			}
			if c.talker != "" {
				s.Receive = true
			}
		}
		for pr := range r.patches {
			if pr[0] == ch.cfg.ID {
				s.Patched = append(s.Patched, pr[1])
			} else if pr[1] == ch.cfg.ID {
				s.Patched = append(s.Patched, pr[0])
			}
		}
		out.Channels = append(out.Channels, s)
	}
	sort.Slice(out.Channels, func(i, j int) bool { return out.Channels[i].Name < out.Channels[j].Name })
	for _, p := range r.positions {
		ps := PositionState{Call: p.call, Ext: p.ext, Name: p.name, Monitor: []string{}, PTT: []string{}}
		for c := range p.monitor {
			ps.Monitor = append(ps.Monitor, c)
		}
		for c := range p.ptt {
			ps.PTT = append(ps.PTT, c)
		}
		out.Positions = append(out.Positions, ps)
	}
	return out
}

// stopRadio closes the channels' ports.
func (e *Engine) stopRadio() {
	if e.radio == nil {
		return
	}
	r := e.radio
	r.bridge.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.channels {
		if ch.p25 != nil {
			ch.p25.close()
		}
		if ch.ep != nil {
			ch.ep.Close()
		}
	}
	r.channels = map[string]*radioChannel{}
	if r.issi != nil {
		r.issi.Close()
		r.issi = nil
	}
}

// RadioAck is an emergency a dispatcher acknowledged (radio.emergency.ack, for CAD and the log).
type RadioAck struct {
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channelName"`
	Unit        string    `json:"unit"`
	Alias       string    `json:"alias,omitempty"`
	RaisedAt    time.Time `json:"raisedAt"`
	By          string    `json:"by"` // the dispatcher's extension or name
	At          time.Time `json:"at"`
}

// AckEmergency acknowledges the emergency raised on a channel: it stops flashing on every
// console, and who acknowledged it is kept.
func (e *Engine) AckEmergency(channel, by string) (RadioAck, error) {
	r := e.radioSys()
	r.mu.Lock()
	ch := r.channels[channel]
	if ch == nil {
		r.mu.Unlock()
		return RadioAck{}, errors.New("no such channel")
	}
	if ch.emUnit == "" {
		r.mu.Unlock()
		return RadioAck{}, errors.New("there is no emergency on " + ch.cfg.Name + " to acknowledge")
	}
	a := RadioAck{Channel: channel, ChannelName: ch.cfg.Name, Unit: ch.emUnit, Alias: ch.emAlias, RaisedAt: ch.emAt, By: by, At: time.Now().UTC()}
	ch.emUnit, ch.emAlias, ch.emAt = "", "", time.Time{}
	ch.ackBy, ch.ackAt = by, a.At
	r.mu.Unlock()
	e.Log.Info("radio emergency acknowledged", "channel", a.ChannelName, "unit", a.Unit, "by", by)
	e.publish("radio.emergency.ack", a)
	e.publish("radio.updated", nil)
	return a, nil
}

// SimulSelect keys (or unkeys) several channels at once for a dispatcher, so one transmission goes
// out on all of them. It is all or nothing: when any channel is in use by another dispatcher, none
// is keyed.
func (e *Engine) SimulSelect(call string, channels []string, on bool) error {
	if len(channels) == 0 {
		return errors.New("choose the channels to transmit on")
	}
	if !on {
		for _, ch := range channels {
			_ = e.PTT(call, ch, false)
		}
		return nil
	}
	r := e.radioSys()
	r.mu.Lock()
	for _, id := range channels {
		ch := r.channels[id]
		switch {
		case r.positions[call] == nil:
			r.mu.Unlock()
			return ErrNoPosition
		case ch == nil:
			r.mu.Unlock()
			return errors.New("no such channel")
		case ch.tx != "" && ch.tx != call:
			r.mu.Unlock()
			return errors.New(ch.cfg.Name + " is in use by another dispatcher")
		}
	}
	r.mu.Unlock()
	var done []string
	for _, id := range channels {
		if err := e.PTT(call, id, true); err != nil {
			for _, d := range done {
				_ = e.PTT(call, d, false)
			}
			return err
		}
		done = append(done, id)
	}
	return nil
}

// RadioFix is a radio's position, as a radio data or AVL gateway reports it (P25 location service,
// LRRP or a vendor's own, translated by the gateway): radio.location, for the console and CAD.
type RadioFix struct {
	Unit     int       `json:"unit"`
	Alias    string    `json:"alias,omitempty"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	Accuracy float64   `json:"accuracy,omitempty"` // metres
	Speed    float64   `json:"speed,omitempty"`    // km/h
	Heading  float64   `json:"heading,omitempty"`  // degrees from north
	At       time.Time `json:"at"`
	Source   string    `json:"source,omitempty"` // the gateway, as it names itself
}

// maxFixes bounds the radios whose last position is kept.
const maxFixes = 5000

// RadioLocation takes a radio's position report.
func (e *Engine) RadioLocation(f RadioFix) (RadioFix, error) {
	switch {
	case f.Unit < 1 || f.Unit > 0xFFFFFF:
		return f, errors.New("unit: a P25 unit ID, 1 to 16777215")
	case f.Lat < -90 || f.Lat > 90 || f.Lon < -180 || f.Lon > 180 || f.Lat == 0 && f.Lon == 0:
		return f, errors.New("lat and lon: a position in degrees")
	case f.Accuracy < 0 || f.Speed < 0 || f.Heading < 0 || f.Heading >= 360:
		return f, errors.New("accuracy and speed are not negative; heading is 0 to 359")
	}
	now := time.Now().UTC()
	if f.At.IsZero() || f.At.After(now.Add(time.Minute)) {
		f.At = now
	}
	f.Alias = e.Config().System.Radio.AliasFor(f.Unit)
	r := e.radioSys()
	r.mu.Lock()
	if r.fixes == nil {
		r.fixes = map[int]RadioFix{}
	}
	if old, ok := r.fixes[f.Unit]; ok && old.At.After(f.At) {
		r.mu.Unlock()
		return old, nil // an older report arriving late changes nothing
	}
	if _, ok := r.fixes[f.Unit]; !ok && len(r.fixes) >= maxFixes {
		oldest, at := 0, now
		for u, x := range r.fixes {
			if x.At.Before(at) {
				oldest, at = u, x.At
			}
		}
		delete(r.fixes, oldest)
	}
	r.fixes[f.Unit] = f
	r.mu.Unlock()
	e.publish("radio.location", f)
	return f, nil
}

// RadioLocations is every radio's last position, newest first.
func (e *Engine) RadioLocations() []RadioFix {
	r := e.radioSys()
	r.mu.Lock()
	out := make([]RadioFix, 0, len(r.fixes))
	for _, f := range r.fixes {
		out = append(out, f)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
