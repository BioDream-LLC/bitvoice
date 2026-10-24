package agi

import (
	"strconv"
	"strings"
	"time"
)

// app is an Asterisk dialplan application a script may run with EXEC. Each returns what Asterisk's
// would: 0 to go on, -1 when the call has ended, a key's code for Background.
type app func(s *session, args []string) int

// apps are the applications BitVoice carries out for EXEC. Others answer -2, as Asterisk does for an
// application it does not have; System and its kin are left out on purpose (a script never runs
// commands on this server through EXEC).
var apps = map[string]app{
	"answer":        func(s *session, _ []string) int { return okOr(s.ch.Answer() == nil) },
	"hangup":        func(s *session, _ []string) int { s.ch.Hangup(); return -1 },
	"noop":          func(s *session, args []string) int { return 0 },
	"verbose":       func(s *session, args []string) int { s.ch.Log(strings.Join(args, ",")); return 0 },
	"log":           func(s *session, args []string) int { s.ch.Log(strings.Join(args, ",")); return 0 },
	"wait":          (*session).appWait,
	"playback":      (*session).appPlayback,
	"background":    (*session).appBackground,
	"read":          (*session).appRead,
	"set":           (*session).appSet,
	"mset":          (*session).appMSet,
	"saydigits":     func(s *session, a []string) int { return s.appSay(digitSounds(arg(a, 0))) },
	"saynumber":     func(s *session, a []string) int { return s.appSpeak(arg(a, 0), arg(a, 0)) },
	"sayalpha":      func(s *session, a []string) int { return s.appSpeak(spell(arg(a, 0)), arg(a, 0)) },
	"sayphonetic":   func(s *session, a []string) int { return s.appSpeak(spell(arg(a, 0)), arg(a, 0)) },
	"dial":          (*session).appDial,
	"goto":          (*session).appGoto,
	"voicemail":     (*session).appVoicemail,
	"queue":         func(s *session, a []string) int { return s.handOver("queue", arg(a, 0)) },
	"musiconhold":   (*session).appMusicOnHold,
	"record":        (*session).appRecord,
	"senddtmf":      func(s *session, a []string) int { return 0 },
	"progress":      func(s *session, a []string) int { return 0 },
	"ringing":       func(s *session, a []string) int { return 0 },
	"playtones":     func(s *session, a []string) int { return 0 },
	"stopplaytones": func(s *session, a []string) int { return 0 },
}

func okOr(ok bool) int {
	if ok {
		return 0
	}
	return -1
}

func arg(a []string, i int) string {
	if i < len(a) {
		return strings.TrimSpace(a[i])
	}
	return ""
}

func spell(v string) string { return strings.Join(strings.Split(v, ""), " ") }

// appArgs splits an application's options at commas (and at |, as old dialplans wrote them), the
// way the dialplan does, keeping commas inside quotes and parentheses.
func appArgs(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	depth, quoted := 0, false
	for _, r := range v {
		switch {
		case r == '"':
			quoted = !quoted
			continue
		case quoted:
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case (r == ',' || r == '|') && depth == 0:
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	return append(out, cur.String())
}

func (s *session) cmdExec(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	opts := ""
	if len(argv) > 2 {
		opts = strings.Join(argv[2:], " ")
	}
	a, ok := apps[strings.ToLower(argv[1])]
	if !ok {
		s.ch.Log("EXEC " + argv[1] + ": BitVoice does not have this application")
		s.result(-2)
		return true
	}
	if s.dead {
		s.result(-1)
		return true
	}
	s.result(a(s, appArgs(s.expand(opts))))
	return true
}

func (s *session) appWait(a []string) int {
	secs, _ := strconv.ParseFloat(arg(a, 0), 64)
	select {
	case <-time.After(time.Duration(min(secs, 3600) * float64(time.Second))):
		return 0
	case <-s.ch.Hungup():
		return -1
	}
}

// playList plays sounds joined with &; a missing one is skipped, as Playback does (with a warning).
func (s *session) playList(names, escape string) (byte, bool, bool) {
	all := true
	for _, n := range strings.Split(names, "&") {
		pcm, ok := s.sound(strings.TrimSpace(n))
		if !ok {
			s.ch.Log("Playback: no sound " + n)
			all = false
			continue
		}
		k, _, hungup := s.ch.Play(pcm, escape)
		if hungup || k != 0 {
			return k, hungup, all
		}
	}
	return 0, false, all
}

func (s *session) appPlayback(a []string) int {
	_, hungup, all := s.playList(arg(a, 0), "")
	s.vars["PLAYBACKSTATUS"] = map[bool]string{true: "SUCCESS", false: "FAILED"}[all && !hungup]
	if hungup {
		return -1
	}
	return 0
}

func (s *session) appBackground(a []string) int {
	k, hungup, all := s.playList(arg(a, 0), "0123456789*#")
	s.vars["BACKGROUNDSTATUS"] = map[bool]string{true: "SUCCESS", false: "FAILED"}[all && !hungup]
	return keyResult(k, hungup)
}

func (s *session) appSay(names []string) int {
	var pcm []int16
	for _, n := range names {
		if p, ok := s.ch.Sound(n); ok {
			pcm = append(append(pcm, p...), make([]int16, 1000)...)
		}
	}
	if _, _, hungup := s.ch.Play(pcm, ""); hungup {
		return -1
	}
	return 0
}

func (s *session) appSpeak(text, digits string) int {
	if pcm, ok := s.ch.Speak(text); ok {
		if _, _, hungup := s.ch.Play(pcm, ""); hungup {
			return -1
		}
		return 0
	}
	return s.appSay(digitSounds(digits))
}

// appRead is Read(variable,filename[&filename2],maxdigits,options,attempts,timeout).
func (s *session) appRead(a []string) int {
	name := arg(a, 0)
	if name == "" {
		return 0
	}
	max, _ := strconv.Atoi(arg(a, 2))
	if max <= 0 || max > 255 {
		max = 255
	}
	attempts, _ := strconv.Atoi(arg(a, 4))
	attempts = min(max1(attempts), 20)
	wait := 5 * time.Second
	if t, err := strconv.ParseFloat(arg(a, 5), 64); err == nil && t > 0 {
		wait = time.Duration(t * float64(time.Second))
	}
	status := "TIMEOUT"
	digits := ""
	for range attempts {
		got, timedOut, hungup := s.readDigits(arg(a, 1), max, wait, wait)
		if hungup {
			s.vars["READSTATUS"] = "HANGUP"
			s.vars[name] = got
			return -1
		}
		digits = got
		if got != "" || !timedOut {
			status = "OK"
			break
		}
	}
	s.vars[name], s.vars["READSTATUS"] = digits, status
	return 0
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func (s *session) appSet(a []string) int {
	name, value, ok := strings.Cut(strings.Join(a, ","), "=")
	if ok {
		s.setVar(strings.TrimSpace(name), value)
	}
	return 0
}

func (s *session) appMSet(a []string) int {
	for _, kv := range a {
		if name, value, ok := strings.Cut(kv, "="); ok {
			s.setVar(strings.TrimSpace(name), value)
		}
	}
	return 0
}

// dialNumber reads the number out of a dial string: PJSIP/201, SIP/201, IAX2/201,
// Local/201@from-internal/n, PJSIP/5551234@provider, DAHDI/g0/5551234.
func dialNumber(target string) string {
	t := strings.TrimSpace(target)
	if i := strings.IndexByte(t, '/'); i >= 0 {
		t = t[i+1:]
	}
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		if strings.ToLower(t[i+1:]) == "n" || strings.Contains(t[:i], "@") {
			t = t[:i] // Local/201@ctx/n
		} else {
			t = t[i+1:] // DAHDI/g0/5551234
		}
	}
	if i := strings.IndexByte(t, '@'); i >= 0 {
		t = t[:i]
	}
	return t
}

// appDial is Dial(Tech/number[&Tech/number2],timeout,options): the first number that answers takes
// the call. BitVoice rings them one after the other, each for the whole timeout.
func (s *session) appDial(a []string) int {
	ring := 60 * time.Second
	if t, err := strconv.Atoi(arg(a, 1)); err == nil && t > 0 {
		ring = time.Duration(min(t, 600)) * time.Second
	}
	status := "CHANUNAVAIL"
	for _, target := range strings.Split(arg(a, 0), "&") {
		n := dialNumber(target)
		if n == "" {
			continue
		}
		st, answered := s.ch.Dial(n, ring)
		status = st
		if answered {
			s.vars["DIALSTATUS"] = "ANSWER"
			return -1 // the call was theirs; it has ended
		}
		select {
		case <-s.ch.Hungup():
			s.vars["DIALSTATUS"] = "CANCEL"
			return -1
		default:
		}
		if st == "BUSY" {
			break
		}
	}
	s.vars["DIALSTATUS"] = status
	return 0
}

// appGoto is Goto([[context,]extension,]priority): the call goes to that extension when the script ends.
func (s *session) appGoto(a []string) int {
	switch len(a) {
	case 2:
		s.goTo(a[0])
	case 3:
		s.vars["CONTEXT"] = a[0]
		s.goTo(a[1])
	}
	return 0
}

// appVoicemail is VoiceMail(mailbox[@context][&mailbox2],options): the caller leaves a message in the
// first mailbox.
func (s *session) appVoicemail(a []string) int {
	box := arg(a, 0)
	if i := strings.IndexByte(box, '&'); i >= 0 {
		box = box[:i]
	}
	if i := strings.IndexByte(box, '@'); i >= 0 {
		box = box[:i]
	}
	return s.handOver("voicemail", box)
}

func (s *session) handOver(kind, target string) int {
	if i := strings.IndexByte(target, '@'); i >= 0 {
		target = target[:i]
	}
	if err := s.ch.HandOver(kind, target); err != nil {
		s.ch.Log(kind + " " + target + ": " + err.Error())
		return -1
	}
	return -1 // the call is no longer the script's
}

func (s *session) appMusicOnHold(a []string) int {
	stop := s.ch.Music()
	defer stop()
	secs, _ := strconv.Atoi(arg(a, 1))
	var limit <-chan time.Time
	if secs > 0 {
		limit = time.After(time.Duration(min(secs, 3600)) * time.Second)
	} else {
		limit = time.After(time.Hour)
	}
	select {
	case <-limit:
		return 0
	case <-s.ch.Hungup():
		return -1
	}
}

// appRecord is Record(filename.format,silence,maxduration,options).
func (s *session) appRecord(a []string) int {
	name := arg(a, 0)
	if i := strings.LastIndexByte(name, '.'); i > strings.LastIndexByte(name, '/') {
		name = name[:i]
	}
	silence, _ := strconv.Atoi(arg(a, 1))
	maxSecs, _ := strconv.Atoi(arg(a, 2))
	max := time.Hour
	if maxSecs > 0 {
		max = time.Duration(min(maxSecs, 3600)) * time.Second
	}
	opts := arg(a, 3)
	if !strings.Contains(opts, "q") {
		if pcm, ok := s.ch.Sound("beep"); ok {
			if _, _, hungup := s.ch.Play(pcm, ""); hungup {
				s.vars["RECORD_STATUS"] = "HANGUP"
				return -1
			}
		}
	}
	escape := "#"
	if strings.Contains(opts, "t") {
		escape = "*"
	}
	if strings.Contains(opts, "x") {
		escape = ""
	}
	pcm, _, reason := s.ch.Record(max, time.Duration(silence)*time.Second, escape)
	if s.cfg.Library == nil || s.cfg.Library.SaveRecording(name, pcm) != nil {
		s.vars["RECORD_STATUS"] = "ERROR"
		return 0
	}
	s.vars["RECORDED_FILE"] = name
	switch reason {
	case "dtmf":
		s.vars["RECORD_STATUS"] = "DTMF"
	case "hangup":
		s.vars["RECORD_STATUS"] = "HANGUP"
		return -1
	default:
		s.vars["RECORD_STATUS"] = "TIMEOUT"
		if silence > 0 && len(pcm) < int(max/time.Second)*8000 {
			s.vars["RECORD_STATUS"] = "SILENCE"
		}
	}
	return 0
}
