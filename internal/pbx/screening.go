package pbx

import (
	"crypto/x509"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/screen"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// applyScreening keeps a certificate checker for the STI roots configured (a new one when they change).
func (e *Engine) applyScreening(cfg config.Config) {
	roots := cfg.System.Screening.STIRoots
	if e.checker.Load() != nil && roots == e.checkerRoots {
		return
	}
	k := &screen.Checker{}
	if roots != "" {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM([]byte(roots)) {
			k.Roots = pool
		}
	}
	e.checkerRoots = roots
	e.checker.Store(k)
}

func labelOf(cfg *config.Config) string {
	if l := cfg.System.Screening.Label; l != "" {
		return l
	}
	return "Spam?"
}

func anonymous(n string) bool {
	switch strings.ToLower(n) {
	case "", "anonymous", "restricted", "unknown", "unavailable", "private", "withheld":
		return true
	}
	return false
}

// screenCall judges an incoming call from a trunk: it records the caller ID's verification, labels
// the call, or returns why it is refused. Within an hour of an emergency call nothing is refused
// (the call may be the emergency service calling back), an emergency call (from the ESInet or a CAMA
// trunk) is never refused, and allowed numbers are never touched.
func (e *Engine) screenCall(cfg *config.Config, c *Call, m *sip.Message, trunk *config.Trunk, did string, emergency bool) string {
	sc := cfg.System.Screening
	var v screen.Verdict
	if id := m.Get("Identity"); id != "" && e.checker.Load() != nil {
		v = e.checker.Load().Check(id, c.from, did)
	} else {
		v = screen.FromHeaders(m.Get("From"), m.Get("P-Asserted-Identity"), m.Get("P-Attestation-Indicator"))
	}
	if v.Attest != "" || v.Verified != "" {
		c.screen = &v
	}
	for _, a := range sc.Allowed {
		if a.Matches(c.from) {
			c.allowed = true
			return ""
		}
	}
	callBack := time.Since(time.Unix(e.lastEmergency.Load(), 0)) < time.Hour
	act, why := "", ""
	switch {
	case anonymous(c.from):
		act, why = sc.Anonymous, "the number is withheld"
	case blockedBy(sc.Blocked, c.from) != "":
		act, why = "block", "the number is on the block list"
	case v.Verified == "failed":
		act, why = sc.Failed, v.Why
	case v.Verified == "" && (v.Attest == "" || v.Attest == "C") || v.Attest == "C":
		act, why = sc.Unverified, "the caller ID is not verified"
	}
	if act == "block" && emergency {
		act = "" // a 911 call is never refused, nor marked as spam for the call-taker
	}
	if act == "block" && callBack {
		act = "label" // never refuse what may be the emergency service calling back
	}
	switch act {
	case "block":
		return e.refuseScreened(c, trunk, did, why)
	case "label":
		c.screened = "labelled"
	}
	return ""
}

// refuseScreened records and announces a call screening refused, and returns the SIP reason.
func (e *Engine) refuseScreened(c *Call, trunk *config.Trunk, did, why string) string {
	e.Log.Info("incoming call refused by screening", "from", c.from, "trunk", trunk.Name, "why", why)
	if e.Recorder != nil {
		now := time.Now().UTC()
		r := store.CDR{ID: c.ID, Start: now, End: now, Direction: "inbound", From: c.from, FromName: c.fromName, To: did,
			Trunk: trunk.Name, Disposition: "blocked", Cause: why, Screened: "blocked"}
		if c.screen != nil {
			r.Attest, r.Verified = c.screen.Attest, c.screen.Verified
		}
		if err := e.Recorder.AddCDR(r); err == nil {
			e.publish("call.logged", r)
		}
	}
	e.publish("call.blocked", map[string]string{"from": c.from, "why": why})
	return "Declined"
}

// screenUnknown decides what happens to a caller BitVoice does not know: the spam database's verdict
// (label, the robot test or refused), and Unknown (ring, the robot test, or straight to UnknownTo). It
// runs after screenCall and returns why the call is refused, if it is. Known callers are never looked
// up, emergency calls and calls within an hour of an emergency call are never held up.
func (e *Engine) screenUnknown(cfg *config.Config, c *Call, trunk *config.Trunk, did string, emergency bool) string {
	sc := cfg.System.Screening
	rep := sc.Reputation.URL != ""
	if emergency || c.allowed || sc.Unknown != "test" && sc.Unknown != "send" && !rep {
		return ""
	}
	if time.Since(time.Unix(e.lastEmergency.Load(), 0)) < time.Hour {
		c.note("Screening let the call ring as usual: it came within an hour of an emergency call, and may be the emergency service calling back.")
		return ""
	}
	withheld := anonymous(c.from)
	if !withheld && e.KnownCaller != nil {
		if why := e.KnownCaller(c.from); why != "" {
			c.note("The caller is known (" + why + "), so screening let the call ring.")
			return ""
		}
	}
	act := sc.Unknown
	if rep && !withheld {
		if spam, said := e.reputationCheck(cfg, c.from); spam {
			c.note("The spam database said " + c.from + " is spam (" + said + ").")
			switch sc.Reputation.Action {
			case "block":
				return e.refuseScreened(c, trunk, did, "the spam database said it is spam")
			case "test":
				act = "test"
			default:
				c.screened = "labelled"
			}
		}
	}
	if act == "test" || act == "send" {
		c.unknownAct = act
	}
	return ""
}

// unknownPlan is the plan for a caller screenUnknown chose the robot test or UnknownTo for; dest is
// where the call would have gone. ok is false when the call goes on as planned: the robot test cannot
// be given when its words are not recorded (a caller cannot pass a test they cannot hear).
func (e *Engine) unknownPlan(cfg *config.Config, c *Call, dest config.Destination, sc scriptCall) (plan, bool) {
	s := cfg.System.Screening
	switch c.unknownAct {
	case "send":
		if s.UnknownTo == nil {
			return plan{}, false
		}
		c.screened = "sent"
		c.note("BitVoice does not know the caller, so screening sent the call to " + destinationName(cfg, *s.UnknownTo) + ".")
		return e.resolveFor(cfg, *s.UnknownTo, 1, sc), true
	case "test":
		digit := byte('1' + rand.IntN(9))
		lang := promptLang(cfg, "")
		if e.promptAudio(cfg, lang, "screen-press") == nil || e.promptAudio(cfg, lang, "digit-"+string(digit)) == nil {
			c.note("Screening would have given the robot test, but its words are not recorded (Prompts, screening), so the call rang as usual.")
			return plan{}, false
		}
		c.screenNext, c.screenFail, c.screenDigit = dest, s.UnknownTo, digit
		return plan{local: "screentest", toName: "Call screening"}, true
	}
	return plan{}, false
}

// ScreeningPromptsMissing names what the robot test needs and is not recorded in the callers'
// language: empty when it can be given (or is not used).
func (e *Engine) ScreeningPromptsMissing(cfg *config.Config) []string {
	s := cfg.System.Screening
	if s.Unknown != "test" && (s.Reputation.URL == "" || s.Reputation.Action != "test") {
		return nil
	}
	lang := promptLang(cfg, "")
	var out []string
	for _, k := range append([]string{"screen-press"}, prompts.Digits("123456789")...) {
		if e.promptAudio(cfg, lang, k) == nil {
			out = append(out, k)
		}
	}
	return out
}

// runScreenTest is the robot test: the caller hears "To be connected, please press" and a digit, twice
// at most, and is put through on that digit. Anything else, or nothing, fails: the caller goes where
// failures go, or hears goodbye.
func (c *Call) runScreenTest() {
	cfg := c.e.Config()
	lang := promptLang(cfg, "")
	c.mu.Lock()
	digit, next, fail, from := c.screenDigit, c.screenNext, c.screenFail, c.from
	c.mu.Unlock()
	capt := media.NewCapture(1)
	c.caller.ep.Record(capt.Packet)
	pressed := ""
	for round := 0; round < 2; round++ {
		key, ended := c.sayPrompts(lang, capt.Keys, media.ToneBeep, "screen-press", "digit-"+string(digit))
		if ended {
			c.caller.ep.Record(nil)
			return
		}
		if key == 0 {
			select {
			case key = <-capt.Keys:
			case <-time.After(6 * time.Second):
			case <-c.cancelled:
				c.caller.ep.Record(nil)
				return
			}
		}
		if key == digit {
			c.caller.ep.Record(nil)
			c.mu.Lock()
			c.screened = "passed"
			c.mu.Unlock()
			c.note("The caller pressed " + string(digit) + " at the robot test, so the call went on.")
			if _, ended := c.sayPrompts(lang, nil, media.ToneConfirm, "screen-connecting"); ended {
				return
			}
			c.goTo(c.e.Config(), next)
			return
		}
		if key != 0 {
			pressed += string(key)
		}
	}
	c.caller.ep.Record(nil)
	c.mu.Lock()
	c.screened = "failed"
	c.mu.Unlock()
	what := "pressed nothing"
	if pressed != "" {
		what = "pressed " + pressed
	}
	c.e.publish("call.screened", map[string]string{"from": from, "result": "failed"})
	if fail != nil {
		c.note("The caller " + what + " at the robot test (it asked for " + string(digit) + "), so the call went to " + destinationName(cfg, *fail) + ".")
		c.goTo(c.e.Config(), *fail)
		return
	}
	c.note("The caller " + what + " at the robot test (it asked for " + string(digit) + "), so the call was ended.")
	c.sayPrompts(lang, nil, media.ToneConfirm, "goodbye")
	c.hangup("failed the robot test", "system")
}

// ErrAlwaysAllowed is BlockNumber's answer for a number on the always-allowed list.
var ErrAlwaysAllowed = errors.New("the number is always allowed")

type lastCaller struct {
	number, name string
	at           time.Time
}

// noteLastCaller remembers the outside caller ringing an extension, for the block-last-caller code.
func (e *Engine) noteLastCaller(ext, number, name string) {
	e.lastMu.Lock()
	if e.lastCallers == nil {
		e.lastCallers = map[string]lastCaller{}
	}
	e.lastCallers[ext] = lastCaller{number: number, name: name, at: time.Now()}
	e.lastMu.Unlock()
}

// LastCallerOf is the last outside caller who rang an extension in the last day.
func (e *Engine) LastCallerOf(ext string) (number, name string, ok bool) {
	e.lastMu.Lock()
	defer e.lastMu.Unlock()
	l, ok := e.lastCallers[ext]
	if !ok || time.Since(l.at) > 24*time.Hour {
		return "", "", false
	}
	return l.number, l.name, true
}

// blockLastCode is the block-last-caller code (*32): the last outside caller who rang the extension
// dialling it goes on the block list, and the phone hears the number read back.
func (c *Call) blockLastCode() {
	e := c.e
	cfg := e.Config()
	c.mu.Lock()
	ext := c.callerExt
	c.mu.Unlock()
	lang := e.langOf(cfg, ext)
	number, _, ok := e.LastCallerOf(ext)
	if !ok || e.BlockNumber == nil || ext == "" {
		c.sayPrompts(lang, nil, toneNoMessages, "screen-nothing")
		c.hangup("no outside caller to block", "system")
		return
	}
	err := e.BlockNumber(number, "extension "+ext, "Blocked from extension "+ext+" with "+cfg.System.Codes.BlockLast+" after a call")
	switch {
	case errors.Is(err, ErrAlwaysAllowed):
		c.note("Extension " + ext + " asked to block " + number + ", which is always allowed.")
		c.sayPrompts(lang, nil, toneDeleted, "screen-allowed")
	case err != nil:
		e.Log.Warn("could not block the last caller", "ext", ext, "err", err)
		c.play(media.ToneSamples(toneDeleted), nil)
	default:
		c.note("Extension " + ext + " blocked the last caller, " + number + ".")
		c.sayPrompts(lang, nil, media.ToneConfirm, append([]string{"screen-blocked"}, prompts.Digits(number)...)...)
	}
	c.hangup("block-last-caller code", "system")
}

func blockedBy(list []config.ScreenNumber, n string) string {
	for _, b := range list {
		if b.Matches(n) {
			return b.Number
		}
	}
	return ""
}
