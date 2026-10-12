package pbx

import (
	"crypto/subtle"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// HotDesk is someone signed in at a shared desk phone: calls to them ring it too, and calls from it
// are theirs (caller ID, permissions, costs). Emergency calls still carry the desk's own location.
type HotDesk struct {
	Desk     string    `json:"desk"` // the desk phone's extension
	DeskName string    `json:"deskName"`
	User     string    `json:"user"` // the extension signed in there
	UserName string    `json:"userName"`
	Since    time.Time `json:"since"`
}

type hotDesks struct {
	mu     sync.Mutex
	byDesk map[string]HotDesk
	fails  map[string][]time.Time // failed sign-ins by desk, for the lockout
	day    string                 // the last day the nightly sign-out ran
	saveMu sync.Mutex             // one save at a time, so the file never goes back in time
}

// Errors from signing in at a hot desk.
var (
	ErrNotHotDesk = errors.New("that extension is not a hot desk")
	ErrNoUser     = errors.New("no such extension to sign in as")
	ErrNoPIN      = errors.New("that extension has no voicemail PIN, so it cannot sign in at a hot desk")
	ErrLocked     = errors.New("too many wrong sign-ins at this desk; wait ten minutes")
)

// HotDesks lists who is signed in where.
func (e *Engine) HotDesks() []HotDesk {
	e.hot.mu.Lock()
	defer e.hot.mu.Unlock()
	out := make([]HotDesk, 0, len(e.hot.byDesk))
	for _, h := range e.hot.byDesk {
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b HotDesk) int { return compareNumbers(a.Desk, b.Desk) })
	return out
}

// LoadHotDesks restores sign-ins saved before a restart, dropping any the configuration no longer allows.
func (e *Engine) LoadHotDesks(hs []HotDesk) {
	cfg := e.Config()
	e.hot.mu.Lock()
	defer e.hot.mu.Unlock()
	e.hot.byDesk = map[string]HotDesk{}
	for _, h := range hs {
		d, u := cfg.FindExtension(h.Desk), cfg.FindExtension(h.User)
		if d != nil && d.HotDesk && u != nil && u.Enabled && !u.HotDesk {
			e.hot.byDesk[h.Desk] = h
		}
	}
}

// SignIn signs user in at desk (moving them from any other desk). The PIN is checked unless it is
// empty and trusted is set (an administrator in the web interface).
func (e *Engine) SignIn(desk, user, pin string, trusted bool) (HotDesk, error) {
	cfg := e.Config()
	d := cfg.FindExtension(desk)
	if d == nil || !d.HotDesk {
		return HotDesk{}, ErrNotHotDesk
	}
	e.hot.mu.Lock()
	recent := 0
	for _, t := range e.hot.fails[desk] {
		if time.Since(t) < 10*time.Minute {
			recent++
		}
	}
	e.hot.mu.Unlock()
	if recent >= 5 && !trusted {
		return HotDesk{}, ErrLocked
	}
	u := cfg.FindExtension(user)
	fail := func(err error) (HotDesk, error) {
		if !trusted {
			e.hot.mu.Lock()
			if e.hot.fails == nil {
				e.hot.fails = map[string][]time.Time{}
			}
			e.hot.fails[desk] = append(e.hot.fails[desk], time.Now())
			e.hot.mu.Unlock()
		}
		return HotDesk{}, err
	}
	if u == nil || !u.Enabled || u.HotDesk || u.Kind != "" {
		return fail(ErrNoUser)
	}
	if !trusted {
		if u.Voicemail.PIN == "" {
			return fail(ErrNoPIN)
		}
		if subtle.ConstantTimeCompare([]byte(pin), []byte(u.Voicemail.PIN)) != 1 {
			return fail(errors.New("the PIN is wrong"))
		}
	}
	h := HotDesk{Desk: d.Number, DeskName: d.Name, User: u.Number, UserName: u.Name, Since: time.Now().UTC()}
	e.hot.mu.Lock()
	if e.hot.byDesk == nil {
		e.hot.byDesk = map[string]HotDesk{}
	}
	for k, x := range e.hot.byDesk {
		if x.User == u.Number {
			delete(e.hot.byDesk, k) // one desk at a time
		}
	}
	e.hot.byDesk[desk] = h
	delete(e.hot.fails, desk)
	e.hot.mu.Unlock()
	e.hotChanged("hotdesk.in", h)
	return h, nil
}

// SignOut signs whoever is at desk out; false when nobody was.
func (e *Engine) SignOut(desk string) bool {
	e.hot.mu.Lock()
	h, ok := e.hot.byDesk[desk]
	delete(e.hot.byDesk, desk)
	e.hot.mu.Unlock()
	if ok {
		e.hotChanged("hotdesk.out", h)
	}
	return ok
}

func (e *Engine) hotChanged(event string, h HotDesk) {
	if e.SaveHotDesks != nil {
		e.hot.saveMu.Lock()
		err := e.SaveHotDesks(e.HotDesks())
		e.hot.saveMu.Unlock()
		if err != nil {
			e.Log.Error("could not save hot desk sign-ins", "err", err)
		}
	}
	e.publish(event, h)
	e.publish("presence.changed", map[string]string{"extension": h.User})
}

// userAt is who is signed in at a desk, or "".
func (e *Engine) userAt(desk string) string {
	e.hot.mu.Lock()
	defer e.hot.mu.Unlock()
	return e.hot.byDesk[desk].User
}

// desksOf lists the desks a user is signed in at (at most one).
func (e *Engine) desksOf(user string) []string {
	e.hot.mu.Lock()
	defer e.hot.mu.Unlock()
	var out []string
	for d, h := range e.hot.byDesk {
		if h.User == user {
			out = append(out, d)
		}
	}
	return out
}

// signOutNightly signs everyone out once a day at the configured time, in the system time zone.
func (e *Engine) signOutNightly(now time.Time) {
	cfg := e.Config()
	at := cfg.System.HotDesk.SignOutAt
	if at == "" {
		return
	}
	local := now.In(cfg.System.Location())
	day := local.Format("2006-01-02")
	if local.Format("15:04") < at {
		return
	}
	e.hot.mu.Lock()
	if e.hot.day == day {
		e.hot.mu.Unlock()
		return
	}
	e.hot.day = day
	desks := make([]string, 0, len(e.hot.byDesk))
	for d := range e.hot.byDesk {
		desks = append(desks, d)
	}
	e.hot.mu.Unlock()
	for _, d := range desks {
		e.SignOut(d)
	}
}

// hotDeskCode runs the sign-in and sign-out codes dialled from a desk phone. Sign in: a tone, the
// extension and #, a tone, the voicemail PIN and #. Confirmed with the rising tone; refused with the
// falling one.
func (c *Call) hotDeskCode(code string) {
	cfg := c.e.Config()
	desk := c.deskExt
	defer func() {
		time.Sleep(300 * time.Millisecond)
		c.hangup("hot desk code", "system")
	}()
	if code == cfg.System.HotDesk.Logout {
		c.e.SignOut(desk)
		c.play(media.ToneSamples(media.ToneOff), nil)
		return
	}
	keys := media.NewCapture(1)
	c.caller.ep.Record(keys.Packet)
	defer c.caller.ep.Record(nil)
	c.play(make([]int16, media.Rate/4), nil)
	if _, ended := c.play(media.ToneSamples(toneNext), nil); ended {
		return
	}
	user := c.collectEntry(keys.Keys)
	if user == "" {
		c.play(media.ToneSamples(media.ToneOff), nil)
		return
	}
	if _, ended := c.play(media.ToneSamples(toneNext), nil); ended {
		return
	}
	pin := c.collectEntry(keys.Keys)
	if _, err := c.e.SignIn(desk, user, pin, false); err != nil {
		c.e.Log.Info("hot desk sign-in refused", "desk", desk, "as", user, "why", err.Error())
		c.play(media.ToneSamples(media.ToneOff), nil)
		return
	}
	c.play(media.ToneSamples(media.ToneConfirm), nil)
}

// collectEntry gathers digits until # or five quiet seconds.
func (c *Call) collectEntry(keys <-chan byte) string {
	var b []byte
	for len(b) < 12 {
		select {
		case k := <-keys:
			if k == '#' {
				return string(b)
			}
			if k >= '0' && k <= '9' {
				b = append(b, k)
			}
		case <-time.After(5 * time.Second):
			return string(b)
		case <-c.cancelled:
			return ""
		}
	}
	return string(b)
}

// compareNumbers orders extension numbers as numbers when they are the same length, else shorter first.
func compareNumbers(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

// pruneHotDesks signs out anyone whose desk is no longer a hot desk, or whose extension went away.
func (e *Engine) pruneHotDesks() {
	cfg := e.Config()
	var gone []string
	e.hot.mu.Lock()
	for d, h := range e.hot.byDesk {
		x, u := cfg.FindExtension(d), cfg.FindExtension(h.User)
		if x == nil || !x.HotDesk || u == nil || !u.Enabled || u.HotDesk {
			gone = append(gone, d)
		}
	}
	e.hot.mu.Unlock()
	for _, d := range gone {
		e.SignOut(d)
	}
}
