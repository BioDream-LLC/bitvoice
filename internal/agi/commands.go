package agi

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseArgs splits a command line as Asterisk does: words separated by spaces or tabs, double quotes
// group words (and an empty "" is an argument of its own), and a backslash makes the next character
// literal.
func parseArgs(line string) []string {
	var args []string
	var cur strings.Builder
	inWord, quoted, escaped := false, false, false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			inWord, escaped = true, false
		case r == '\\':
			escaped = true
		case r == '"':
			if !quoted && !inWord {
				inWord = true // a quote starts a word, even an empty one
			}
			quoted = !quoted
		case (r == ' ' || r == '\t') && !quoted:
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
		if len(args) >= 127 {
			break
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args
}

// command is one AGI command: the words that name it, whether it may run once the call has ended,
// and what it does with its arguments (argv includes the command's own words).
type command struct {
	words []string
	dead  bool
	usage string
	run   func(s *session, argv []string) bool // false: wrong arguments (520)
}

var commands []command

func init() {
	commands = []command{
		{[]string{"answer"}, false, "ANSWER", (*session).cmdAnswer},
		{[]string{"asyncagi", "break"}, true, "ASYNCAGI BREAK", func(s *session, _ []string) bool { s.result(0); return true }},
		{[]string{"channel", "status"}, false, "CHANNEL STATUS [CHANNELNAME]", (*session).cmdChannelStatus},
		{[]string{"control", "stream", "file"}, false, "CONTROL STREAM FILE FILENAME ESCAPE_DIGITS [SKIPMS [FFCHAR [REWCHR [PAUSECHR [OFFSETMS]]]]]", (*session).cmdControlStream},
		{[]string{"database", "del"}, true, "DATABASE DEL FAMILY KEY", (*session).cmdDBDel},
		{[]string{"database", "deltree"}, true, "DATABASE DELTREE FAMILY [KEYTREE]", (*session).cmdDBDelTree},
		{[]string{"database", "get"}, true, "DATABASE GET FAMILY KEY", (*session).cmdDBGet},
		{[]string{"database", "put"}, true, "DATABASE PUT FAMILY KEY VALUE", (*session).cmdDBPut},
		{[]string{"exec"}, true, "EXEC APPLICATION OPTIONS", (*session).cmdExec},
		{[]string{"get", "data"}, false, "GET DATA FILE [TIMEOUT [MAXDIGITS]]", (*session).cmdGetData},
		{[]string{"get", "full", "variable"}, true, "GET FULL VARIABLE EXPRESSION [CHANNELNAME]", (*session).cmdGetFullVariable},
		{[]string{"get", "option"}, false, "GET OPTION FILENAME ESCAPE_DIGITS [TIMEOUT]", (*session).cmdGetOption},
		{[]string{"get", "variable"}, true, "GET VARIABLE VARIABLENAME", (*session).cmdGetVariable},
		{[]string{"gosub"}, false, "GOSUB CONTEXT EXTENSION PRIORITY [OPTIONAL-ARGUMENT]", func(s *session, argv []string) bool {
			if len(argv) < 4 {
				return false
			}
			s.send("200 result=-1 Gosub label not found") // BitVoice has no dialplan to run a subroutine in
			return true
		}},
		{[]string{"hangup"}, false, "HANGUP [CHANNELNAME]", (*session).cmdHangup},
		{[]string{"noop"}, true, "NOOP", func(s *session, _ []string) bool { s.result(0); return true }},
		{[]string{"receive", "char"}, false, "RECEIVE CHAR TIMEOUT", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(0, "(timeout)") // calls here carry no text characters
			return true
		}},
		{[]string{"receive", "text"}, false, "RECEIVE TEXT TIMEOUT", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(-1)
			return true
		}},
		{[]string{"record", "file"}, false, "RECORD FILE FILENAME FORMAT ESCAPE_DIGITS TIMEOUT [OFFSET_SAMPLES] [BEEP] [s=SILENCE]", (*session).cmdRecordFile},
		{[]string{"say", "alpha"}, false, "SAY ALPHA NUMBER ESCAPE_DIGITS", (*session).cmdSayAlpha},
		{[]string{"say", "date"}, false, "SAY DATE DATE ESCAPE_DIGITS", (*session).cmdSayDate},
		{[]string{"say", "datetime"}, false, "SAY DATETIME TIME [ESCAPE_DIGITS [FORMAT [TIMEZONE]]]", (*session).cmdSayDateTime},
		{[]string{"say", "digits"}, false, "SAY DIGITS NUMBER ESCAPE_DIGITS", (*session).cmdSayDigits},
		{[]string{"say", "number"}, false, "SAY NUMBER NUMBER ESCAPE_DIGITS [GENDER]", (*session).cmdSayNumber},
		{[]string{"say", "phonetic"}, false, "SAY PHONETIC STRING ESCAPE_DIGITS", (*session).cmdSayAlpha},
		{[]string{"say", "time"}, false, "SAY TIME TIME ESCAPE_DIGITS", (*session).cmdSayTime},
		{[]string{"send", "image"}, false, "SEND IMAGE IMAGE", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(0)
			return true
		}},
		{[]string{"send", "text"}, false, "SEND TEXT \"TEXT TO SEND\"", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(0)
			return true
		}},
		{[]string{"set", "autohangup"}, false, "SET AUTOHANGUP TIME", (*session).cmdAutoHangup},
		{[]string{"set", "callerid"}, false, "SET CALLERID NUMBER", (*session).cmdSetCallerID},
		{[]string{"set", "context"}, false, "SET CONTEXT DESIRED CONTEXT", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.vars["CONTEXT"] = argv[2]
			s.result(0)
			return true
		}},
		{[]string{"set", "extension"}, false, "SET EXTENSION NEW EXTENSION", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.goTo(argv[2])
			s.result(0)
			return true
		}},
		{[]string{"set", "music"}, false, "SET MUSIC {ON|OFF} [CLASS]", (*session).cmdSetMusic},
		{[]string{"set", "priority"}, false, "SET PRIORITY PRIORITY", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(0)
			return true
		}},
		{[]string{"set", "variable"}, true, "SET VARIABLE VARIABLENAME VALUE", (*session).cmdSetVariable},
		{[]string{"speech", "activate", "grammar"}, false, "SPEECH ACTIVATE GRAMMAR GRAMMAR_NAME", (*session).cmdSpeechGrammar},
		{[]string{"speech", "create"}, false, "SPEECH CREATE ENGINE", (*session).cmdSpeechCreate},
		{[]string{"speech", "deactivate", "grammar"}, false, "SPEECH DEACTIVATE GRAMMAR GRAMMAR_NAME", (*session).cmdSpeechGrammar},
		{[]string{"speech", "destroy"}, true, "SPEECH DESTROY", (*session).cmdSpeechDestroy},
		{[]string{"speech", "load", "grammar"}, false, "SPEECH LOAD GRAMMAR GRAMMAR_NAME PATH_TO_GRAMMAR", (*session).cmdSpeechGrammar},
		{[]string{"speech", "recognize"}, false, "SPEECH RECOGNIZE PROMPT TIMEOUT [OFFSET]", (*session).cmdSpeechRecognize},
		{[]string{"speech", "set"}, false, "SPEECH SET NAME VALUE", (*session).cmdSpeechSet},
		{[]string{"speech", "unload", "grammar"}, true, "SPEECH UNLOAD GRAMMAR GRAMMAR_NAME", (*session).cmdSpeechGrammar},
		{[]string{"stream", "file"}, false, "STREAM FILE FILENAME ESCAPE_DIGITS [SAMPLE OFFSET]", (*session).cmdStreamFile},
		{[]string{"tdd", "mode"}, false, "TDD MODE {ON|OFF}", func(s *session, argv []string) bool {
			if len(argv) != 3 {
				return false
			}
			s.result(0)
			return true
		}},
		{[]string{"verbose"}, true, "VERBOSE MESSAGE LEVEL", func(s *session, argv []string) bool {
			if len(argv) < 2 {
				return false
			}
			s.ch.Log(argv[1])
			s.result(1)
			return true
		}},
		{[]string{"wait", "for", "digit"}, false, "WAIT FOR DIGIT TIMEOUT", (*session).cmdWaitForDigit},
	}
}

// find returns the command a line names: the one whose words match the most leading words.
func find(argv []string) *command {
	var best *command
	for i := range commands {
		c := &commands[i]
		if len(c.words) > len(argv) || (best != nil && len(c.words) <= len(best.words)) {
			continue
		}
		ok := true
		for j, w := range c.words {
			if !strings.EqualFold(argv[j], w) {
				ok = false
				break
			}
		}
		if ok {
			best = c
		}
	}
	return best
}

func (s *session) handle(line string) {
	argv := parseArgs(line)
	c := find(argv)
	if c == nil {
		s.send("510 Invalid or unknown command")
		return
	}
	if s.dead && !c.dead {
		s.send("511 Command Not Permitted on a dead channel or intercept routine")
		return
	}
	if !c.run(s, argv) {
		s.send("520-Invalid command syntax.  Proper usage follows:")
		s.send("Usage: " + c.usage)
		s.send("520 End of proper usage.")
	}
	// (Asterisk sends its long description in the middle; scripts only look for the 520 lines.)
}

// ms reads a time in milliseconds; negative means "no limit".
func ms(v string) (time.Duration, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	if n < 0 {
		return -1, true
	}
	return time.Duration(n) * time.Millisecond, true
}

func (s *session) cmdAnswer(argv []string) bool {
	if err := s.ch.Answer(); err != nil {
		s.result(-1)
		return true
	}
	s.result(0)
	return true
}

func (s *session) cmdChannelStatus(argv []string) bool {
	if len(argv) > 3 {
		return false
	}
	if len(argv) == 3 && argv[2] != s.ch.Name() {
		s.result(-1)
		return true
	}
	if s.ch.Up() {
		s.result(6) // AST_STATE_UP
	} else {
		s.result(4) // AST_STATE_RING
	}
	return true
}

func (s *session) cmdHangup(argv []string) bool {
	switch len(argv) {
	case 1:
	case 2:
		if argv[1] != s.ch.Name() {
			s.result(-1) // BitVoice scripts may only hang up their own call
			return true
		}
	default:
		return false
	}
	s.ch.Hangup()
	s.result(1)
	return true
}

// key turns a key into the number Asterisk answers with: its character code, 0 for none, -1 when
// the call ended.
func keyResult(k byte, hungup bool) int {
	if hungup {
		return -1
	}
	return int(k)
}

func (s *session) cmdWaitForDigit(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	d, ok := ms(argv[3])
	if !ok {
		return false
	}
	k, hungup := s.ch.WaitKey(d)
	s.result(keyResult(k, hungup))
	return true
}

// sounds finds each of a list of sounds joined with &, as Asterisk plays them one after the other.
func (s *session) sounds(names string) ([]int16, bool) {
	var all []int16
	for _, n := range strings.Split(names, "&") {
		pcm, ok := s.sound(n)
		if !ok {
			return nil, false
		}
		all = append(all, pcm...)
	}
	return all, true
}

// sound finds one sound: a file in the sound library or one the script recorded, a sound Asterisk
// ships that BitVoice has too (digits/1, beep, silence/2, vm-goodbye), or a prompt by BitVoice's name.
func (s *session) sound(name string) ([]int16, bool) {
	if s.cfg.Library != nil {
		if pcm, ok := s.cfg.Library.Find(name, s.env.Language); ok {
			return pcm, true
		}
	}
	key := soundKey(name)
	if strings.HasPrefix(key, "silence:") {
		n, _ := strconv.Atoi(strings.TrimPrefix(key, "silence:"))
		return make([]int16, min(n, 60)*8000), true
	}
	return s.ch.Sound(key)
}

func (s *session) playOrFail(names, escape string, offset int) (key byte, endpos int, hungup, ok bool) {
	pcm, found := s.sounds(names)
	if !found {
		return 0, offset, false, false
	}
	offset = min(max(offset, 0), len(pcm))
	k, played, hungup := s.ch.Play(pcm[offset:], escape)
	return k, offset + played, hungup, true
}

func (s *session) cmdStreamFile(argv []string) bool {
	if len(argv) < 4 || len(argv) > 5 {
		return false
	}
	offset := 0
	if len(argv) == 5 {
		n, err := strconv.Atoi(argv[4])
		if err != nil {
			return false
		}
		offset = n
	}
	k, end, hungup, ok := s.playOrFail(argv[2], argv[3], offset)
	if !ok {
		s.result(-1, "endpos="+strconv.Itoa(offset))
		return true
	}
	status := "SUCCESS"
	if hungup {
		status = "FAILED"
	}
	s.vars["PLAYBACKSTATUS"] = status
	s.result(keyResult(k, hungup), "endpos="+strconv.Itoa(end))
	return true
}

func (s *session) cmdGetOption(argv []string) bool {
	if len(argv) < 4 || len(argv) > 5 {
		return false
	}
	wait := 5 * time.Second // Asterisk's digit timeout
	if len(argv) == 5 {
		n, _ := strconv.Atoi(argv[4])
		wait = time.Duration(n) * time.Millisecond
	}
	k, end, hungup, ok := s.playOrFail(argv[2], argv[3], 0)
	if !ok {
		s.result(-1, "endpos=0")
		return true
	}
	if k == 0 && !hungup {
		k, hungup = s.ch.WaitKey(wait)
		if k != 0 && !strings.ContainsRune(argv[3], rune(k)) {
			k = 0
		}
	}
	s.result(keyResult(k, hungup), "endpos="+strconv.Itoa(end))
	return true
}

func (s *session) cmdControlStream(argv []string) bool {
	if len(argv) < 4 || len(argv) > 9 {
		return false
	}
	at := func(i int, d string) string {
		if len(argv) > i && argv[i] != "" {
			return argv[i]
		}
		return d
	}
	skip, _ := strconv.Atoi(at(4, "3000"))
	if skip <= 0 {
		skip = 3000
	}
	ff, rew, pause := at(5, "#"), at(6, "*"), at(7, "")
	offsetMs, _ := strconv.Atoi(at(8, "0"))
	pcm, ok := s.sounds(argv[2])
	if !ok {
		s.vars["CPLAYBACKSTATUS"] = "ERROR"
		s.result(-1, "endpos=0")
		return true
	}
	pos := min(max(offsetMs*8, 0), len(pcm))
	escape := argv[3]
	controls := escape + ff[:1] + rew[:1]
	if pause != "" {
		controls += pause[:1]
	}
	var k byte
	hungup := false
	for pos < len(pcm) {
		var played int
		k, played, hungup = s.ch.Play(pcm[pos:], controls)
		pos += played
		if hungup || k == 0 {
			break
		}
		switch {
		case strings.IndexByte(escape, k) >= 0:
		case k == ff[0]:
			pos, k = min(pos+skip*8, len(pcm)), 0
			continue
		case k == rew[0]:
			pos, k = max(pos-skip*8, 0), 0
			continue
		case pause != "" && k == pause[0]:
			// Paused until the pause key again (or an escape key).
			for {
				var hk byte
				hk, hungup = s.ch.WaitKey(-1)
				if hungup || hk == pause[0] {
					break
				}
				if strings.IndexByte(escape, hk) >= 0 {
					k = hk
					break
				}
			}
			if hungup || (k != 0 && k != pause[0]) {
				break
			}
			k = 0
			continue
		}
		break
	}
	switch {
	case hungup:
		s.vars["CPLAYBACKSTATUS"] = "ERROR"
	case k != 0:
		s.vars["CPLAYBACKSTATUS"] = "USERSTOPPED"
		s.vars["CPLAYBACKSTOPKEY"] = string(k)
	default:
		s.vars["CPLAYBACKSTATUS"] = "SUCCESS"
	}
	s.vars["CPLAYBACKOFFSET"] = strconv.Itoa(pos / 8)
	s.result(keyResult(k, hungup), "endpos="+strconv.Itoa(pos/8))
	return true
}

// readDigits plays prompts and collects up to max keys, # ending early. firstWait is for the first
// key, nextWait between keys. It returns what was pressed, whether time ran out, and a hang-up.
func (s *session) readDigits(prompts string, max int, firstWait, nextWait time.Duration) (string, bool, bool) {
	var got strings.Builder
	if prompts != "" {
		pcm, ok := s.sounds(prompts)
		if ok {
			k, _, hungup := s.ch.Play(pcm, "0123456789*#ABCD")
			if hungup {
				return "", false, true
			}
			if k == '#' {
				return "", false, false
			}
			if k != 0 {
				got.WriteByte(k)
			}
		}
	}
	wait := firstWait
	if got.Len() > 0 {
		wait = nextWait
	}
	for got.Len() < max {
		k, hungup := s.ch.WaitKey(wait)
		if hungup {
			return got.String(), false, true
		}
		if k == 0 {
			return got.String(), true, false
		}
		if k == '#' {
			break
		}
		got.WriteByte(k)
		wait = nextWait
	}
	return got.String(), false, false
}

func (s *session) cmdGetData(argv []string) bool {
	if len(argv) < 3 {
		return false
	}
	first, next := 10*time.Second, 5*time.Second // Asterisk's response and digit timeouts
	if len(argv) >= 4 {
		if n, err := strconv.Atoi(argv[3]); err == nil && n > 0 {
			first, next = time.Duration(n)*time.Millisecond, time.Duration(n)*time.Millisecond
		} else if n < 0 {
			first, next = time.Hour, time.Hour
		}
	}
	max := 1024
	if len(argv) >= 5 {
		if n, err := strconv.Atoi(argv[4]); err == nil && n > 0 {
			max = min(n, 1024)
		}
	}
	digits, timedOut, hungup := s.readDigits(argv[2], max, first, next)
	switch {
	case hungup:
		s.result(-1)
	case timedOut:
		s.send("200 result=" + digits + " (timeout)")
	default:
		s.send("200 result=" + digits)
	}
	return true
}

func (s *session) cmdRecordFile(argv []string) bool {
	if len(argv) < 6 {
		return false
	}
	maxMs, err := strconv.Atoi(argv[5])
	if err != nil {
		return false
	}
	silence := 0
	beep := false
	for i, a := range argv[6:] {
		switch {
		case strings.HasPrefix(a, "s="):
			silence, _ = strconv.Atoi(a[2:])
		case i == 0:
			if _, err := strconv.Atoi(a); err != nil {
				beep = true // anything that is neither an offset nor s= asks for a beep
			}
		default:
			beep = true
		}
	}
	if beep {
		if pcm, ok := s.ch.Sound("beep"); ok {
			if _, _, hungup := s.ch.Play(pcm, ""); hungup {
				s.send("200 result=-1 (hangup) endpos=0")
				return true
			}
		}
	}
	max := time.Hour
	if maxMs > 0 {
		max = min(time.Duration(maxMs)*time.Millisecond, time.Hour)
	}
	pcm, k, reason := s.ch.Record(max, time.Duration(silence)*time.Second, argv[4])
	if s.cfg.Library == nil || s.cfg.Library.SaveRecording(argv[2], pcm) != nil {
		s.send("200 result=-1 (writefile)")
		return true
	}
	end := strconv.Itoa(len(pcm))
	switch reason {
	case "dtmf":
		s.send(fmt.Sprintf("200 result=%d (dtmf) endpos=%s", k, end))
	case "hangup":
		s.send("200 result=-1 (hangup) endpos=" + end)
	default:
		s.send("200 result=0 (timeout) endpos=" + end)
	}
	return true
}

// saySounds plays a run of BitVoice sounds, stopping at an escape key.
func (s *session) saySounds(names []string, escape string) {
	var pcm []int16
	for _, n := range names {
		if p, ok := s.ch.Sound(n); ok {
			pcm = append(pcm, p...)
			pcm = append(pcm, make([]int16, 1000)...)
		}
	}
	k, _, hungup := s.ch.Play(pcm, escape)
	s.result(keyResult(k, hungup))
}

func digitSounds(v string) []string {
	var out []string
	for _, r := range v {
		if r >= '0' && r <= '9' {
			out = append(out, "digit-"+string(r))
		}
	}
	return out
}

func (s *session) cmdSayDigits(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	s.saySounds(digitSounds(argv[2]), argv[3])
	return true
}

// cmdSayNumber says a number. BitVoice says numbers digit by digit in every language, unless a speech
// service is set up, which says it as a number.
func (s *session) cmdSayNumber(argv []string) bool {
	if len(argv) < 4 || len(argv) > 5 {
		return false
	}
	if _, err := strconv.Atoi(argv[2]); err != nil {
		return false
	}
	if pcm, ok := s.ch.Speak(argv[2]); ok {
		k, _, hungup := s.ch.Play(pcm, argv[3])
		s.result(keyResult(k, hungup))
		return true
	}
	s.saySounds(digitSounds(argv[2]), argv[3])
	return true
}

// speakOr says words with the speech service, or the digits in them when there is none.
func (s *session) speakOr(text, digits, escape string) {
	if pcm, ok := s.ch.Speak(text); ok {
		k, _, hungup := s.ch.Play(pcm, escape)
		s.result(keyResult(k, hungup))
		return
	}
	s.saySounds(digitSounds(digits), escape)
}

func (s *session) cmdSayAlpha(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	spelled := strings.Join(strings.Split(argv[2], ""), " ")
	s.speakOr(spelled, argv[2], argv[3])
	return true
}

func epoch(v string) (time.Time, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

func (s *session) cmdSayDate(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	t, ok := epoch(argv[2])
	if !ok {
		return false
	}
	s.speakOr(t.Format("Monday, January 2, 2006"), t.Format("20060102"), argv[3])
	return true
}

func (s *session) cmdSayTime(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	t, ok := epoch(argv[2])
	if !ok {
		return false
	}
	s.speakOr(t.Format("3:04 PM"), t.Format("1504"), argv[3])
	return true
}

func (s *session) cmdSayDateTime(argv []string) bool {
	if len(argv) < 3 || len(argv) > 6 {
		return false
	}
	t, ok := epoch(argv[2])
	if !ok {
		return false
	}
	escape := ""
	if len(argv) > 3 {
		escape = argv[3]
	}
	if len(argv) > 5 {
		if loc, err := time.LoadLocation(argv[5]); err == nil {
			t = t.In(loc)
		}
	}
	s.speakOr(t.Format("Monday, January 2, 2006, 3:04 PM"), t.Format("200601021504"), escape)
	return true
}

func (s *session) cmdAutoHangup(argv []string) bool {
	if len(argv) != 3 {
		return false
	}
	secs, err := strconv.ParseFloat(argv[2], 64)
	if err != nil {
		return false
	}
	if s.autoHang != nil {
		s.autoHang.Stop()
		s.autoHang = nil
	}
	if secs > 0 {
		s.autoHang = time.AfterFunc(time.Duration(secs*float64(time.Second)), s.ch.Hangup)
	}
	s.result(0)
	return true
}

// parseCallerID reads "Name" <number>, Name <number>, <number> or a bare number.
func parseCallerID(v string) (number, name string) {
	v = strings.TrimSpace(v)
	if i := strings.LastIndexByte(v, '<'); i >= 0 {
		if j := strings.IndexByte(v[i:], '>'); j > 0 {
			number = v[i+1 : i+j]
			name = strings.Trim(strings.TrimSpace(v[:i]), `"`)
			return strings.TrimSpace(number), name
		}
	}
	if strings.ContainsAny(v, "0123456789") && strings.Trim(v, "0123456789+*#-() ") == "" {
		return v, ""
	}
	return "", strings.Trim(v, `"`)
}

func (s *session) cmdSetCallerID(argv []string) bool {
	if len(argv) < 3 {
		return false
	}
	num, name := parseCallerID(argv[2])
	num = strings.Map(func(r rune) rune {
		if strings.ContainsRune("0123456789+*#", r) {
			return r
		}
		return -1
	}, num)
	s.ch.SetCallerID(num, name)
	s.vars["CALLERID(num)"], s.vars["CALLERID(name)"] = num, name
	s.result(1)
	return true
}

func (s *session) cmdSetMusic(argv []string) bool {
	if len(argv) < 3 || len(argv) > 4 {
		return false
	}
	switch strings.ToLower(argv[2]) {
	case "on":
		if s.speech == nil {
			s.speech = &speechState{}
		}
		if s.speech.music == nil {
			s.speech.music = s.ch.Music()
		}
	case "off":
		if s.speech != nil && s.speech.music != nil {
			s.speech.music()
			s.speech.music = nil
		}
	default:
		return false
	}
	s.result(0)
	return true
}

func (s *session) cmdSetVariable(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	s.setVar(argv[2], argv[3])
	s.result(1)
	return true
}

func (s *session) setVar(name, value string) {
	switch strings.ToUpper(name) {
	case "CALLERID(NUM)", "CALLERID(NUMBER)":
		s.ch.SetCallerID(value, s.vars["CALLERID(name)"])
		s.vars["CALLERID(num)"] = value
		return
	case "CALLERID(NAME)":
		s.ch.SetCallerID(s.vars["CALLERID(num)"], value)
		s.vars["CALLERID(name)"] = value
		return
	}
	if len(s.vars) < 1000 {
		s.vars[name] = value
	}
}

// variable returns a variable the script set or one BitVoice knows about the call.
func (s *session) variable(name string) (string, bool) {
	if v, ok := s.vars[name]; ok {
		return v, true
	}
	e := s.env
	switch strings.ToUpper(name) {
	case "CALLERID(NUM)", "CALLERID(NUMBER)":
		return e.CallerID, true
	case "CALLERID(NAME)":
		return e.CallerIDName, true
	case "CALLERID(ALL)":
		return fmt.Sprintf("%q <%s>", e.CallerIDName, e.CallerID), true
	case "CALLERID(DNID)":
		return e.DNID, true
	case "CALLERID(RDNIS)", "REDIRECTING(FROM-NUM)":
		return e.RDNIS, e.RDNIS != ""
	case "EXTEN":
		return e.Extension, true
	case "CONTEXT":
		return e.Context, true
	case "PRIORITY":
		return "1", true
	case "UNIQUEID":
		return e.UniqueID, true
	case "CHANNEL", "CHANNEL(NAME)":
		return s.ch.Name(), true
	case "LANGUAGE", "CHANNEL(LANGUAGE)":
		return e.Language, true
	case "ACCOUNTCODE", "CHANNEL(ACCOUNTCODE)":
		return e.AccountCode, true
	case "EPOCH":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	case "ASTERISK_VERSION", "VERSION()":
		return e.Version, true
	}
	if n, ok := strings.CutPrefix(name, "ARG"); ok {
		if i, err := strconv.Atoi(n); err == nil && i >= 1 && i <= len(e.Args) {
			return e.Args[i-1], true
		}
	}
	return "", false
}

func (s *session) cmdGetVariable(argv []string) bool {
	if len(argv) != 3 {
		return false
	}
	if v, ok := s.variable(argv[2]); ok {
		s.send("200 result=1 (" + v + ")")
	} else {
		s.result(0)
	}
	return true
}

// expand replaces ${NAME} in an expression with the variables' values (unknown ones become empty).
func (s *session) expand(expr string) string {
	var b strings.Builder
	for {
		i := strings.Index(expr, "${")
		if i < 0 {
			b.WriteString(expr)
			return b.String()
		}
		b.WriteString(expr[:i])
		rest := expr[i+2:]
		depth, j := 1, 0
		for ; j < len(rest) && depth > 0; j++ {
			switch rest[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if depth > 0 {
			b.WriteString(expr[i:])
			return b.String()
		}
		name := s.expand(rest[:j-1])
		v, _ := s.variable(name)
		b.WriteString(v)
		expr = rest[j:]
	}
}

func (s *session) cmdGetFullVariable(argv []string) bool {
	if len(argv) < 4 || len(argv) > 5 {
		return false
	}
	if len(argv) == 5 && argv[4] != s.ch.Name() {
		s.result(0)
		return true
	}
	s.send("200 result=1 (" + s.expand(argv[3]) + ")")
	return true
}

// goTo records where the call goes when the script ends: a number dialled here.
func (s *session) goTo(exten string) {
	exten = strings.TrimSpace(exten)
	if exten == "" || exten == "s" || exten == "h" || exten == "i" || exten == "t" {
		return // Asterisk's special extensions have no meaning here
	}
	s.next = exten
}

func (s *session) cmdDBGet(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	if s.cfg.DB == nil {
		s.result(0)
		return true
	}
	if v, ok := s.cfg.DB.Get(argv[2], argv[3]); ok {
		s.send("200 result=1 (" + v + ")")
	} else {
		s.result(0)
	}
	return true
}

func (s *session) cmdDBPut(argv []string) bool {
	if len(argv) != 5 {
		return false
	}
	if s.cfg.DB == nil || s.cfg.DB.Put(argv[2], argv[3], argv[4]) != nil {
		s.result(0)
		return true
	}
	s.result(1)
	return true
}

func (s *session) cmdDBDel(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	// Asterisk answers 1 whether or not the key was there (0 only when its database fails).
	if s.cfg.DB != nil {
		s.cfg.DB.Del(argv[2], argv[3])
	}
	s.result(1)
	return true
}

func (s *session) cmdDBDelTree(argv []string) bool {
	if len(argv) < 3 || len(argv) > 4 {
		return false
	}
	prefix := ""
	if len(argv) == 4 {
		prefix = argv[3]
	}
	// Asterisk answers 0 when it deleted something and 1 when there was nothing to delete; scripts
	// written for it expect that, so BitVoice answers the same.
	if s.cfg.DB != nil && s.cfg.DB.DelTree(argv[2], prefix) > 0 {
		s.result(0)
		return true
	}
	s.result(1)
	return true
}
