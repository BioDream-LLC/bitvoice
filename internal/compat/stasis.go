package compat

// ARI applications (Asterisk's Stasis): a call sent to an application is handed to the program
// connected to /ari/events?app=NAME, which then runs it over ARI: answer, play, record, keys, bridges,
// variables, hang up, or continue (let go of it). BitVoice answers a call as it enters an application.
// When no program is connected the call goes to the application's fallback, and when the program's
// last connection closes its calls go there too, after a few seconds' grace for a reconnect.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// appChannel is what the ARI needs of a channel in an application (*pbx.AppChannel; fakes in tests).
type appChannel interface {
	ID() string
	CallID() string
	Info() pbx.CallInfo
	Gone() <-chan struct{}
	Ended() bool
	OnKey(func(byte))
	Var(string) (string, bool)
	SetVar(string, string)
	Sound(string) ([]int16, error)
	Play([]int16, <-chan struct{}) (bool, bool, error)
	Record(maxSeconds, maxSilence int, beep bool, terminateOn string, stop <-chan struct{}) (pbx.RecordResult, error)
	JoinBridge(*media.Bridge) error
	LeaveBridge()
	Hangup(string)
	Continue(*config.Destination) error
}

// AppPBX is what originating into an application needs from the engine.
type AppPBX interface {
	CallIntoApp(ext, app string, args []string, callerName string, ring time.Duration) (string, error)
}

type appConn struct{ out chan []byte }

type appChan struct {
	ch     appChannel
	app    string
	work   chan func() // playbacks and recordings, one at a time
	bridge string
}

type playback struct {
	ID        string   `json:"id"`
	MediaURI  string   `json:"media_uri"`
	MediaURIs []string `json:"media_uris"`
	TargetURI string   `json:"target_uri"`
	Language  string   `json:"language"`
	State     string   `json:"state"`
	app       string
	stop      chan struct{}
	once      *sync.Once
	control   chan string // pause, unpause, restart, reverse, forward (nil for a bridge's playback)
}

type liveRec struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	TargetURI string `json:"target_uri"`
	State     string `json:"state"`
	Duration  int    `json:"duration,omitempty"`
	Cause     string `json:"cause,omitempty"`
	app       string
	stop      chan struct{}
	cancel    bool
	once      *sync.Once
}

type ariBridge struct {
	id, name string
	br       *media.Bridge
	chans    []string
	created  time.Time
}

type stasis struct {
	mu        sync.Mutex
	conns     map[string]map[*appConn]struct{} // by application
	chans     map[string]*appChan
	bridges   map[string]*ariBridge
	playbacks map[string]*playback
	live      map[string]*liveRec
	grace     map[string]*time.Timer
}

func (a *ARI) st() *stasis {
	a.stOnce.Do(func() {
		a.stasis = &stasis{conns: map[string]map[*appConn]struct{}{}, chans: map[string]*appChan{},
			bridges: map[string]*ariBridge{}, playbacks: map[string]*playback{}, live: map[string]*liveRec{}, grace: map[string]*time.Timer{}}
	})
	return a.stasis
}

func stamp() string { return time.Now().Format("2006-01-02T15:04:05.000-0700") }

// emit sends an event to every connection of an application.
func (a *ARI) emit(app string, msg map[string]any) {
	if t, _ := msg["type"].(string); a.filtered(app, t) {
		return
	}
	msg["application"], msg["timestamp"], msg["asterisk_id"] = app, stamp(), "bitvoice"
	b, _ := json.Marshal(msg)
	s := a.st()
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns[app] {
		select {
		case c.out <- b:
		default: // a program that does not read its events loses them, never the calls
		}
	}
}

func (a *ARI) channelJSON(ch appChannel, app string) ariChannel {
	for _, c := range Channels(ch.Info()) {
		if c.ID == ch.ID() {
			out := toARI(c)
			out.State = "Up"
			out.Dialplan.AppName, out.Dialplan.AppData = "Stasis", app
			return out
		}
	}
	return ariChannel{ID: ch.ID(), State: "Up", Dialplan: ariDialplan{Context: "default", Exten: "s", Priority: 1, AppName: "Stasis", AppData: app}}
}

// Enter hands a call to an application (pbx.StasisHub).
func (a *ARI) Enter(app string, ch *pbx.AppChannel, args []string) bool {
	return a.enter(app, ch, args)
}

func (a *ARI) enter(app string, ch appChannel, args []string) bool {
	if !a.PBX.Config().System.Compat.ARI {
		return false
	}
	s := a.st()
	s.mu.Lock()
	if len(s.conns[app]) == 0 {
		s.mu.Unlock()
		return false
	}
	c := &appChan{ch: ch, app: app, work: make(chan func(), 32)}
	s.chans[ch.ID()] = c
	s.mu.Unlock()
	ch.OnKey(func(k byte) {
		a.emit(app, map[string]any{"type": "ChannelDtmfReceived", "digit": string(k), "duration_ms": 100, "channel": a.channelJSON(ch, app)})
	})
	if args == nil {
		args = []string{}
	}
	a.emit(app, map[string]any{"type": "StasisStart", "args": args, "channel": a.channelJSON(ch, app)})
	go func() {
		for {
			select {
			case f := <-c.work:
				f()
			case <-ch.Gone():
				a.leave(c)
				return
			}
		}
	}()
	return true
}

// leave is the end of a channel's time in its application: out of its bridge, its media stopped.
func (a *ARI) leave(c *appChan) {
	s := a.st()
	s.mu.Lock()
	delete(s.chans, c.ch.ID())
	var pbs []*playback
	for _, p := range s.playbacks {
		if p.TargetURI == "channel:"+c.ch.ID() {
			pbs = append(pbs, p)
		}
	}
	var recs []*liveRec
	for _, r := range s.live {
		if r.TargetURI == "channel:"+c.ch.ID() {
			recs = append(recs, r)
		}
	}
	s.mu.Unlock()
	for _, p := range pbs {
		p.once.Do(func() { close(p.stop) })
	}
	for _, r := range recs {
		r.once.Do(func() { close(r.stop) })
	}
	if c.bridge != "" {
		a.removeFromBridge(c.bridge, c)
	}
	ch := a.channelJSON(c.ch, c.app)
	if c.ch.Ended() {
		a.emit(c.app, map[string]any{"type": "ChannelHangupRequest", "cause": 16, "channel": ch})
	}
	a.emit(c.app, map[string]any{"type": "StasisEnd", "channel": ch})
}

// connect registers a program's events connection for its applications; the function returned
// unregisters it, and lets go of an application's calls once it has had no connection for a while.
func (a *ARI) connect(apps []string, c *appConn) func() {
	s := a.st()
	s.mu.Lock()
	for _, app := range apps {
		if s.conns[app] == nil {
			s.conns[app] = map[*appConn]struct{}{}
		}
		s.conns[app][c] = struct{}{}
		if t := s.grace[app]; t != nil {
			t.Stop()
			delete(s.grace, app)
		}
	}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, app := range apps {
			delete(s.conns[app], c)
			if len(s.conns[app]) > 0 {
				continue
			}
			app := app
			s.grace[app] = time.AfterFunc(a.reconnectGrace(), func() {
				s.mu.Lock()
				delete(s.grace, app)
				if len(s.conns[app]) > 0 {
					s.mu.Unlock()
					return
				}
				var orphans []appChannel
				for _, ch := range s.chans {
					if ch.app == app {
						orphans = append(orphans, ch.ch)
					}
				}
				s.mu.Unlock()
				for _, ch := range orphans {
					_ = ch.Continue(nil) // to the application's fallback
				}
			})
		}
	}
}

func (a *ARI) reconnectGrace() time.Duration {
	if a.Grace > 0 {
		return a.Grace
	}
	return 5 * time.Second
}

func (s *stasis) chanOf(id string) *appChan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chans[id]
}

var (
	recNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	idRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

func (a *ARI) recPath(name string) (string, error) {
	if a.Recordings == "" {
		return "", errors.New("recordings are not available here")
	}
	if !recNameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("a recording's name is letters, digits, dot, - and _")
	}
	return filepath.Join(a.Recordings, name+".wav"), nil
}

// sound turns one media URI into audio: sound:NAME, digits:123, number:42, characters:ABC (digits
// only), tone:NAME, or recording:NAME (a stored recording).
func (a *ARI) sound(ch appChannel, uri string) ([]int16, error) {
	kind, what, ok := strings.Cut(uri, ":")
	if !ok || what == "" {
		return nil, fmt.Errorf("%q is not a media URI (sound:, digits:, number:, tone:, recording:)", uri)
	}
	switch kind {
	case "sound":
		return ch.Sound(what)
	case "tone":
		name, _, _ := strings.Cut(what, ";")
		return ch.Sound(name)
	case "digits", "number", "characters":
		return ch.Sound("digits:" + what)
	case "recording":
		p, err := a.recPath(what)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("no stored recording %s", what)
		}
		return media.DecodeWAV(b)
	}
	return nil, fmt.Errorf("BitVoice plays sound:, digits:, number:, tone: and recording: media, not %s:", kind)
}

func mediaList(r *http.Request) []string {
	var out []string
	for _, m := range r.URL.Query()["media"] {
		for _, x := range strings.Split(m, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}

// play queues a playback on a channel (POST /channels/{id}/play).
func (a *ARI) play(w http.ResponseWriter, r *http.Request, c *appChan, id string) {
	uris := mediaList(r)
	if len(uris) == 0 {
		ariError(w, http.StatusBadRequest, "media is required (sound:hello-world)")
		return
	}
	if id == "" {
		id = r.URL.Query().Get("playbackId")
	}
	if id == "" {
		id = config.NewID()
	} else if !idRe.MatchString(id) {
		ariError(w, http.StatusBadRequest, "a playback id is letters, digits, dot, - and _")
		return
	}
	s := a.st()
	pb := &playback{ID: id, MediaURI: uris[0], MediaURIs: uris, TargetURI: "channel:" + c.ch.ID(), Language: "en", State: "queued",
		app: c.app, stop: make(chan struct{}), once: &sync.Once{}, control: make(chan string)}
	s.mu.Lock()
	if _, dup := s.playbacks[id]; dup {
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "a playback has this id")
		return
	}
	s.playbacks[id] = pb
	s.mu.Unlock()
	job := func() {
		set := func(state string) {
			s.mu.Lock()
			pb.State = state
			cp := *pb
			s.mu.Unlock()
			typ := "PlaybackFinished"
			if state == "playing" {
				typ = "PlaybackStarted"
			}
			a.emit(pb.app, map[string]any{"type": typ, "playback": cp})
		}
		defer func() {
			s.mu.Lock()
			delete(s.playbacks, pb.ID)
			s.mu.Unlock()
		}()
		select {
		case <-pb.stop:
			set("done")
			return
		default:
		}
		set("playing")
		for i, u := range uris {
			if i > 0 {
				s.mu.Lock()
				pb.MediaURI = u
				cp := *pb
				s.mu.Unlock()
				a.emit(pb.app, map[string]any{"type": "PlaybackContinuing", "playback": cp})
			}
			pcm, err := a.sound(c.ch, u)
			if err != nil {
				set("failed")
				return
			}
			res := a.playControlled(c, pb, pcm, set)
			if res == "failed" {
				set("failed")
				return
			}
			if res != "" {
				break
			}
		}
		set("done")
	}
	select {
	case c.work <- job:
	default:
		s.mu.Lock()
		delete(s.playbacks, id)
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "too many playbacks and recordings are waiting on this channel")
		return
	}
	s.mu.Lock()
	cp := *pb
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cp)
}

// record starts a live recording on a channel (POST /channels/{id}/record).
func (a *ARI) record(w http.ResponseWriter, r *http.Request, c *appChan) {
	q := r.URL.Query()
	name := q.Get("name")
	path, err := a.recPath(name)
	if err != nil {
		ariError(w, http.StatusBadRequest, err.Error())
		return
	}
	if f := q.Get("format"); f != "" && f != "wav" {
		ariError(w, http.StatusUnprocessableEntity, "BitVoice records wav")
		return
	}
	switch q.Get("ifExists") {
	case "", "fail":
		if _, err := os.Stat(path); err == nil {
			ariError(w, http.StatusConflict, "a recording has this name (ifExists=overwrite replaces it)")
			return
		}
	case "overwrite":
	default:
		ariError(w, http.StatusBadRequest, "BitVoice takes ifExists fail or overwrite")
		return
	}
	maxDur, _ := strconv.Atoi(q.Get("maxDurationSeconds"))
	maxSil, _ := strconv.Atoi(q.Get("maxSilenceSeconds"))
	beep := q.Get("beep") == "true"
	term := q.Get("terminateOn")
	switch term {
	case "", "none":
		term = ""
	case "any", "*", "#":
	default:
		ariError(w, http.StatusBadRequest, "terminateOn is none, any, * or #")
		return
	}
	s := a.st()
	lr := &liveRec{Name: name, Format: "wav", TargetURI: "channel:" + c.ch.ID(), State: "queued", app: c.app, stop: make(chan struct{}), once: &sync.Once{}}
	s.mu.Lock()
	if _, dup := s.live[name]; dup {
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "a recording of this name is going on")
		return
	}
	s.live[name] = lr
	s.mu.Unlock()
	job := func() {
		send := func(typ string) {
			s.mu.Lock()
			cp := *lr
			s.mu.Unlock()
			a.emit(lr.app, map[string]any{"type": typ, "recording": cp})
		}
		defer func() {
			s.mu.Lock()
			delete(s.live, name)
			s.mu.Unlock()
		}()
		s.mu.Lock()
		lr.State = "recording"
		s.mu.Unlock()
		send("RecordingStarted")
		res, err := c.ch.Record(maxDur, maxSil, beep, term, lr.stop)
		s.mu.Lock()
		cancel := lr.cancel
		s.mu.Unlock()
		switch {
		case err != nil:
			s.mu.Lock()
			lr.State, lr.Cause = "failed", err.Error()
			s.mu.Unlock()
			send("RecordingFailed")
			return
		case cancel:
			s.mu.Lock()
			lr.State = "canceled"
			s.mu.Unlock()
			send("RecordingFinished")
			return
		}
		pcm := res.PCM
		if res.Reason == "silence" && maxSil > 0 {
			pcm = media.TrimSilence(pcm)
		}
		if err := os.MkdirAll(a.Recordings, 0o700); err == nil {
			err = os.WriteFile(path, media.EncodeWAV(pcm), 0o600)
			if err != nil {
				s.mu.Lock()
				lr.State, lr.Cause = "failed", "could not save: "+err.Error()
				s.mu.Unlock()
				send("RecordingFailed")
				return
			}
		}
		s.mu.Lock()
		lr.State, lr.Duration = "done", int(math.Round(float64(len(pcm))/media.Rate))
		s.mu.Unlock()
		send("RecordingFinished")
	}
	select {
	case c.work <- job:
	default:
		s.mu.Lock()
		delete(s.live, name)
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "too many playbacks and recordings are waiting on this channel")
		return
	}
	s.mu.Lock()
	cp := *lr
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cp)
}

// dtmfTones are the two frequencies of each key, sent in the audio (inband).
var dtmfTones = map[rune][2]float64{'1': {697, 1209}, '2': {697, 1336}, '3': {697, 1477}, 'A': {697, 1633},
	'4': {770, 1209}, '5': {770, 1336}, '6': {770, 1477}, 'B': {770, 1633}, '7': {852, 1209}, '8': {852, 1336},
	'9': {852, 1477}, 'C': {852, 1633}, '*': {941, 1209}, '0': {941, 1336}, '#': {941, 1477}, 'D': {941, 1633}}

func dtmfAudio(keys string, ms int) ([]int16, error) {
	if ms <= 0 {
		ms = 100
	}
	var out []int16
	for _, k := range strings.ToUpper(keys) {
		if k == ',' {
			out = append(out, make([]int16, media.Rate/2)...)
			continue
		}
		f, ok := dtmfTones[k]
		if !ok {
			return nil, fmt.Errorf("%q is not a key", k)
		}
		out = append(out, media.ToneSamples(media.Tone{Freqs: f[:], Level: 0.25, On: time.Duration(ms) * time.Millisecond, Off: 80 * time.Millisecond, Repeat: 1})...)
	}
	return out, nil
}

// appChannelRoute serves the ARI calls on a channel in an application; false when the path is not one.
func (a *ARI) appChannelRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	s := a.st()
	c := s.chanOf(parts[1])
	if c == nil {
		return false
	}
	q := r.URL.Query()
	op := ""
	if len(parts) >= 3 {
		op = parts[2]
	}
	switch {
	case op == "" && r.Method == http.MethodGet:
		ariJSON(w, a.channelJSON(c.ch, c.app))
	case op == "" && r.Method == http.MethodDelete:
		a.audit(u, "ari.hangup", c.ch.ID())
		c.ch.Hangup("hung up by ARI " + u.Username)
		w.WriteHeader(http.StatusNoContent)
	case (op == "answer" || op == "ring" || op == "ring_stop") && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusNoContent) // answered as it entered the application
	case op == "play" && r.Method == http.MethodPost:
		id := ""
		if len(parts) == 4 {
			id = parts[3]
		}
		a.play(w, r, c, id)
	case op == "record" && r.Method == http.MethodPost:
		a.record(w, r, c)
	case op == "dtmf" && r.Method == http.MethodPost:
		ms, _ := strconv.Atoi(q.Get("duration"))
		pcm, err := dtmfAudio(q.Get("dtmf"), ms)
		if err != nil || len(pcm) == 0 {
			ariError(w, http.StatusBadRequest, "dtmf is the keys to send (0-9, *, #, A-D; a comma pauses)")
			return true
		}
		select {
		case c.work <- func() { _, _, _ = c.ch.Play(pcm, nil) }:
			w.WriteHeader(http.StatusNoContent)
		default:
			ariError(w, http.StatusConflict, "too much is waiting on this channel")
		}
	case op == "variable" && r.Method == http.MethodGet:
		v, ok := c.ch.Var(q.Get("variable"))
		if !ok {
			ariError(w, http.StatusNotFound, "no such variable")
			return true
		}
		ariJSON(w, map[string]string{"value": v})
	case op == "variable" && r.Method == http.MethodPost:
		name := q.Get("variable")
		if name == "" {
			ariError(w, http.StatusBadRequest, "variable is required")
			return true
		}
		c.ch.SetVar(name, q.Get("value"))
		w.WriteHeader(http.StatusNoContent)
	case op == "redirect" && r.Method == http.MethodPost:
		ext := Extension(q.Get("endpoint"))
		if ext == "" || a.PBX.Config().FindExtension(ext) == nil {
			ariError(w, http.StatusBadRequest, "endpoint is an extension here (PJSIP/101)")
			return true
		}
		if err := c.ch.Continue(&config.Destination{Type: "extension", Target: ext}); err != nil {
			ariError(w, http.StatusConflict, err.Error())
			return true
		}
		a.audit(u, "ari.redirect", c.ch.ID()+" to "+ext)
		w.WriteHeader(http.StatusNoContent)
	case op == "continue" && r.Method == http.MethodPost:
		// The dialplan's place is a destination here: the extension parameter as a number BitVoice
		// knows (an extension, ring group or queue), else the application's fallback.
		var d *config.Destination
		if n := q.Get("extension"); n != "" {
			cfg := a.PBX.Config()
			switch {
			case cfg.FindExtension(n) != nil:
				d = &config.Destination{Type: "extension", Target: n}
			case cfg.FindRingGroup(n) != nil:
				d = &config.Destination{Type: "ringgroup", Target: n}
			case cfg.FindQueue(n) != nil:
				d = &config.Destination{Type: "queue", Target: n}
			}
		}
		if err := c.ch.Continue(d); err != nil {
			ariError(w, http.StatusConflict, err.Error())
			return true
		}
		a.audit(u, "ari.continue", c.ch.ID())
		w.WriteHeader(http.StatusNoContent)
	default:
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice for a channel in an application: "+r.Method+" "+op)
	}
	return true
}

func (b *ariBridge) json() map[string]any {
	chans := append([]string{}, b.chans...)
	return map[string]any{"id": b.id, "name": b.name, "technology": "softmix", "bridge_type": "mixing", "bridge_class": "stasis",
		"creator": "Stasis", "channels": chans, "creationtime": b.created.Format("2006-01-02T15:04:05.000-0700")}
}

// bridgeRoute serves the ARI's own bridges (made with POST /bridges); false when the path is not one.
func (a *ARI) bridgeRoute(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	s := a.st()
	q := r.URL.Query()
	if len(parts) == 1 && r.Method == http.MethodPost {
		if t := q.Get("type"); t != "" && !strings.Contains(t, "mixing") {
			ariError(w, http.StatusBadRequest, "BitVoice makes mixing bridges")
			return true
		}
		id := q.Get("bridgeId")
		if id == "" {
			id = config.NewID()
		} else if !idRe.MatchString(id) {
			ariError(w, http.StatusBadRequest, "a bridge id is letters, digits, dot, - and _")
			return true
		}
		a.newBridge(w, u, id, q.Get("name"))
		return true
	}
	if len(parts) < 2 {
		return false
	}
	s.mu.Lock()
	b := s.bridges[parts[1]]
	s.mu.Unlock()
	if b == nil {
		if len(parts) == 2 && r.Method == http.MethodPost { // POST /bridges/{id} creates it
			a.newBridge(w, u, parts[1], q.Get("name"))
			return true
		}
		return false
	}
	op := ""
	if len(parts) >= 3 {
		op = parts[2]
	}
	ids := func() []string {
		var out []string
		for _, x := range strings.Split(q.Get("channel"), ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	switch {
	case op == "" && r.Method == http.MethodGet:
		s.mu.Lock()
		out := b.json()
		s.mu.Unlock()
		ariJSON(w, out)
	case op == "" && r.Method == http.MethodPost: // updates an existing bridge: its name
		s.mu.Lock()
		if n := q.Get("name"); n != "" {
			b.name = n
		}
		out := b.json()
		s.mu.Unlock()
		ariJSON(w, out)
	case op == "" && r.Method == http.MethodDelete:
		s.mu.Lock()
		chans := append([]string{}, b.chans...)
		s.mu.Unlock()
		for _, id := range chans {
			if c := s.chanOf(id); c != nil {
				a.removeFromBridge(b.id, c)
			}
		}
		s.mu.Lock()
		delete(s.bridges, b.id)
		out := b.json()
		s.mu.Unlock()
		a.stopBridgeMusic(b.id)
		b.br.Close()
		a.emitAll(map[string]any{"type": "BridgeDestroyed", "bridge": out})
		a.audit(u, "ari.bridge.destroy", b.id)
		w.WriteHeader(http.StatusNoContent)
	case op == "addChannel" && r.Method == http.MethodPost:
		list := ids()
		if len(list) == 0 {
			ariError(w, http.StatusBadRequest, "channel is required")
			return true
		}
		for _, id := range list {
			c := s.chanOf(id)
			if c == nil {
				ariError(w, http.StatusUnprocessableEntity, "channel "+id+" is not in an application")
				return true
			}
			if c.bridge != "" && c.bridge != b.id {
				ariError(w, http.StatusConflict, "channel "+id+" is in another bridge")
				return true
			}
		}
		for _, id := range list {
			c := s.chanOf(id)
			if c.bridge == b.id {
				continue
			}
			if err := c.ch.JoinBridge(b.br); err != nil {
				ariError(w, http.StatusConflict, err.Error())
				return true
			}
			s.mu.Lock()
			c.bridge = b.id
			b.chans = append(b.chans, id)
			out := b.json()
			s.mu.Unlock()
			a.emit(c.app, map[string]any{"type": "ChannelEnteredBridge", "bridge": out, "channel": a.channelJSON(c.ch, c.app)})
		}
		a.audit(u, "ari.bridge.add", b.id+" "+strings.Join(list, ","))
		w.WriteHeader(http.StatusNoContent)
	case op == "removeChannel" && r.Method == http.MethodPost:
		for _, id := range ids() {
			c := s.chanOf(id)
			if c == nil || c.bridge != b.id {
				ariError(w, http.StatusUnprocessableEntity, "channel "+id+" is not in this bridge")
				return true
			}
			a.removeFromBridge(b.id, c)
		}
		w.WriteHeader(http.StatusNoContent)
	case op == "play" && r.Method == http.MethodPost:
		uris := mediaList(r)
		var pcm []int16
		var any appChannel
		s.mu.Lock()
		for _, id := range b.chans {
			if c := s.chans[id]; c != nil {
				any = c.ch
			}
		}
		s.mu.Unlock()
		for _, u := range uris {
			if any == nil {
				ariError(w, http.StatusConflict, "the bridge has nobody in it")
				return true
			}
			p, err := a.sound(any, u)
			if err != nil {
				ariError(w, http.StatusBadRequest, err.Error())
				return true
			}
			pcm = append(pcm, p...)
		}
		if len(pcm) == 0 {
			ariError(w, http.StatusBadRequest, "media is required (sound:hello-world)")
			return true
		}
		b.br.Inject(pcm)
		id := q.Get("playbackId")
		if id == "" {
			id = config.NewID()
		}
		pb := playback{ID: id, MediaURI: uris[0], MediaURIs: uris, TargetURI: "bridge:" + b.id, Language: "en", State: "playing"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(pb)
	default:
		ariError(w, http.StatusNotImplemented, "Not offered by BitVoice for a bridge: "+r.Method+" "+op)
	}
	return true
}

func (a *ARI) newBridge(w http.ResponseWriter, u *config.CompatUser, id, name string) {
	s := a.st()
	s.mu.Lock()
	if _, dup := s.bridges[id]; dup {
		s.mu.Unlock()
		ariError(w, http.StatusConflict, "a bridge has this id")
		return
	}
	b := &ariBridge{id: id, name: name, br: media.NewBridge(nil), created: time.Now()}
	s.bridges[id] = b
	out := b.json()
	s.mu.Unlock()
	a.emitAll(map[string]any{"type": "BridgeCreated", "bridge": out})
	a.audit(u, "ari.bridge.create", id)
	ariJSON(w, out)
}

func (a *ARI) removeFromBridge(id string, c *appChan) {
	s := a.st()
	s.mu.Lock()
	b := s.bridges[id]
	if b == nil || c.bridge != id {
		s.mu.Unlock()
		return
	}
	c.bridge = ""
	for i, x := range b.chans {
		if x == c.ch.ID() {
			b.chans = append(b.chans[:i], b.chans[i+1:]...)
			break
		}
	}
	out := b.json()
	s.mu.Unlock()
	c.ch.LeaveBridge()
	a.emit(c.app, map[string]any{"type": "ChannelLeftBridge", "bridge": out, "channel": a.channelJSON(c.ch, c.app)})
}

// emitAll sends an event to every connected application.
func (a *ARI) emitAll(msg map[string]any) {
	s := a.st()
	s.mu.Lock()
	var apps []string
	for app, cs := range s.conns {
		if len(cs) > 0 {
			apps = append(apps, app)
		}
	}
	s.mu.Unlock()
	for _, app := range apps {
		m := map[string]any{}
		for k, v := range msg {
			m[k] = v
		}
		a.emit(app, m)
	}
}

// ariBridges lists the ARI's own bridges.
func (a *ARI) ariBridges() []map[string]any {
	s := a.st()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, b := range s.bridges {
		out = append(out, b.json())
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

// otherRoutes serves playbacks, recordings and applications.
func (a *ARI) otherRoutes(w http.ResponseWriter, r *http.Request, u *config.CompatUser, parts []string) bool {
	s := a.st()
	switch {
	case parts[0] == "playbacks" && len(parts) == 2:
		s.mu.Lock()
		pb := s.playbacks[parts[1]]
		var cp playback
		if pb != nil {
			cp = *pb
		}
		s.mu.Unlock()
		if pb == nil {
			ariError(w, http.StatusNotFound, "Playback not found")
			return true
		}
		switch r.Method {
		case http.MethodGet:
			ariJSON(w, cp)
		case http.MethodDelete:
			pb.once.Do(func() { close(pb.stop) })
			w.WriteHeader(http.StatusNoContent)
		default:
			ariError(w, http.StatusNotImplemented, "a playback takes GET and DELETE")
		}
		return true
	case parts[0] == "recordings" && len(parts) >= 3 && parts[1] == "live":
		s.mu.Lock()
		lr := s.live[parts[2]]
		var cp liveRec
		if lr != nil {
			cp = *lr
		}
		s.mu.Unlock()
		if lr == nil {
			ariError(w, http.StatusNotFound, "Recording not found")
			return true
		}
		switch {
		case len(parts) == 3 && r.Method == http.MethodGet:
			ariJSON(w, cp)
		case len(parts) == 4 && parts[3] == "stop" && r.Method == http.MethodPost:
			lr.once.Do(func() { close(lr.stop) })
			w.WriteHeader(http.StatusNoContent)
		case len(parts) == 3 && r.Method == http.MethodDelete:
			s.mu.Lock()
			lr.cancel = true
			s.mu.Unlock()
			lr.once.Do(func() { close(lr.stop) })
			w.WriteHeader(http.StatusNoContent)
		default:
			ariError(w, http.StatusNotImplemented, "a live recording takes GET, DELETE and POST .../stop")
		}
		return true
	case parts[0] == "recordings" && len(parts) >= 2 && parts[1] == "stored":
		if len(parts) == 2 && r.Method == http.MethodGet {
			out := []map[string]string{}
			ents, _ := os.ReadDir(a.Recordings)
			for _, e := range ents {
				if n, ok := strings.CutSuffix(e.Name(), ".wav"); ok && !e.IsDir() {
					out = append(out, map[string]string{"name": n, "format": "wav"})
				}
			}
			ariJSON(w, out)
			return true
		}
		if len(parts) < 3 {
			return false
		}
		p, err := a.recPath(parts[2])
		if err != nil {
			ariError(w, http.StatusBadRequest, err.Error())
			return true
		}
		if _, err := os.Stat(p); err != nil {
			ariError(w, http.StatusNotFound, "Recording not found")
			return true
		}
		switch {
		case len(parts) == 3 && r.Method == http.MethodGet:
			ariJSON(w, map[string]string{"name": parts[2], "format": "wav"})
		case len(parts) == 4 && parts[3] == "file" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "audio/wav")
			http.ServeFile(w, r, p)
		case len(parts) == 3 && r.Method == http.MethodDelete:
			if !u.Write {
				ariError(w, http.StatusForbidden, "This login is read only")
				return true
			}
			_ = os.Remove(p)
			a.audit(u, "ari.recording.delete", parts[2])
			w.WriteHeader(http.StatusNoContent)
		default:
			ariError(w, http.StatusNotImplemented, "a stored recording takes GET, GET .../file and DELETE")
		}
		return true
	case parts[0] == "applications":
		cfg := a.PBX.Config()
		app := func(name string) map[string]any {
			s.mu.Lock()
			defer s.mu.Unlock()
			chans, bridges := []string{}, []string{}
			for id, c := range s.chans {
				if c.app == name {
					chans = append(chans, id)
					if c.bridge != "" && !contains(bridges, c.bridge) {
						bridges = append(bridges, c.bridge)
					}
				}
			}
			sort.Strings(chans)
			return map[string]any{"name": name, "channel_ids": chans, "bridge_ids": bridges, "endpoint_ids": []string{}, "device_names": []string{},
				"connected": len(s.conns[name]) > 0}
		}
		_ = app
		if len(parts) == 1 && r.Method == http.MethodGet {
			out := []map[string]any{}
			for _, x := range cfg.System.Compat.Apps {
				out = append(out, a.appJSON(x.Name))
			}
			ariJSON(w, out)
			return true
		}
		if len(parts) == 2 && r.Method == http.MethodGet {
			if cfg.FindARIApp(parts[1]) == nil {
				ariError(w, http.StatusNotFound, "Application not found")
				return true
			}
			ariJSON(w, a.appJSON(parts[1]))
			return true
		}
	}
	return false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// playControlled plays pcm to a channel, obeying the playback's controls: pause and unpause, restart,
// and reverse or forward by three seconds. It returns "" when the sound played to its end, "stopped"
// when the playback was stopped or the call ended, or "failed".
func (a *ARI) playControlled(c *appChan, pb *playback, pcm []int16, set func(string)) string {
	const skip = 3 * media.Rate
	pos := 0
	for pos < len(pcm) {
		stop := make(chan struct{})
		done := make(chan struct{})
		var stopped, ended bool
		var err error
		started := time.Now()
		go func(from int) {
			stopped, ended, err = c.ch.Play(pcm[from:], stop)
			close(done)
		}(pos)
		select {
		case <-done:
			if err != nil {
				return "failed"
			}
			if stopped || ended {
				return "stopped"
			}
			return ""
		case <-pb.stop:
			close(stop)
			<-done
			return "stopped"
		case op := <-pb.control:
			close(stop)
			<-done
			if ended {
				return "stopped"
			}
			pos = min(pos+int(time.Since(started).Seconds()*media.Rate), len(pcm))
			switch op {
			case "restart":
				pos = 0
			case "reverse":
				pos = max(pos-skip, 0)
			case "forward":
				pos = min(pos+skip, len(pcm))
			case "pause":
				set("paused")
			wait:
				for {
					select {
					case op2 := <-pb.control:
						switch op2 {
						case "unpause":
							break wait
						case "restart":
							pos = 0
							break wait
						}
					case <-pb.stop:
						return "stopped"
					case <-c.ch.Gone():
						return "stopped"
					}
				}
				set("playing")
			}
		}
	}
	return ""
}
