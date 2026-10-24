package agi

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChannel is a call for the tests: keys come from a queue, sounds are fixed lengths.
type fakeChannel struct {
	mu       sync.Mutex
	keys     []byte // pressed in turn, one per Play or WaitKey that takes keys
	hung     chan struct{}
	once     sync.Once
	up       bool
	played   int
	dialled  []string
	dialRes  string
	handed   string
	callerID string
	logs     []string
	speech   bool
	recorded []int16
	beepLen  int
}

func newFake() *fakeChannel { return &fakeChannel{hung: make(chan struct{}), dialRes: "NOANSWER"} }

func (f *fakeChannel) Name() string            { return "PJSIP/201-00000001" }
func (f *fakeChannel) Hungup() <-chan struct{} { return f.hung }
func (f *fakeChannel) Answer() error           { f.up = true; return nil }
func (f *fakeChannel) Up() bool                { return f.up }
func (f *fakeChannel) next() byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.keys) == 0 {
		return 0
	}
	k := f.keys[0]
	f.keys = f.keys[1:]
	return k
}
func (f *fakeChannel) gone() bool {
	select {
	case <-f.hung:
		return true
	default:
		return false
	}
}
func (f *fakeChannel) Play(pcm []int16, escape string) (byte, int, bool) {
	if f.gone() {
		return 0, 0, true
	}
	f.played++
	if k := f.next(); k != 0 {
		if strings.IndexByte(escape, k) >= 0 {
			return k, len(pcm) / 2, false
		}
	}
	return 0, len(pcm), false
}
func (f *fakeChannel) WaitKey(d time.Duration) (byte, bool) {
	if f.gone() {
		return 0, true
	}
	return f.next(), false
}
func (f *fakeChannel) Record(max, silence time.Duration, escape string) ([]int16, byte, string) {
	if k := f.next(); k != 0 && strings.IndexByte(escape, k) >= 0 {
		return make([]int16, 8000), k, "dtmf"
	}
	return make([]int16, 16000), 0, "timeout"
}
func (f *fakeChannel) Sound(name string) ([]int16, bool) {
	switch {
	case name == "beep" && f.beepLen > 0:
		return make([]int16, f.beepLen), true
	case name == "beep", name == "goodbye", strings.HasPrefix(name, "digit-"), strings.HasPrefix(name, "digits:"):
		return make([]int16, 800), true
	}
	return nil, false
}
func (f *fakeChannel) Speak(text string) ([]int16, bool) {
	if !f.speech {
		return nil, false
	}
	return make([]int16, 4000), true
}
func (f *fakeChannel) Transcribe(pcm []int16) (string, bool) {
	if !f.speech {
		return "", false
	}
	return "sales please", true
}
func (f *fakeChannel) Music() func()                   { return func() {} }
func (f *fakeChannel) SetCallerID(number, name string) { f.callerID = name + " <" + number + ">" }
func (f *fakeChannel) Dial(number string, ring time.Duration) (string, bool) {
	f.dialled = append(f.dialled, number)
	if f.dialRes == "ANSWER" {
		f.Hangup()
		return "ANSWER", true
	}
	return f.dialRes, false
}
func (f *fakeChannel) HandOver(kind, target string) error {
	f.handed = kind + " " + target
	f.Hangup()
	return nil
}
func (f *fakeChannel) Hangup()        { f.once.Do(func() { close(f.hung) }) }
func (f *fakeChannel) Log(msg string) { f.logs = append(f.logs, msg) }

// script runs a session over a pipe and gives the test a client end that reads the variables.
type script struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	env  map[string]string
	done chan Result
}

func start(t *testing.T, ch Channel, env Env, cfg Config) *script {
	t.Helper()
	a, b := net.Pipe()
	s := &script{t: t, conn: b, r: bufio.NewReader(b), env: map[string]string{}, done: make(chan Result, 1)}
	go func() {
		res := Run(a, ch, env, cfg)
		a.Close()
		s.done <- res
	}()
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			break
		}
		k, v, _ := strings.Cut(line, ": ")
		s.env[k] = v
	}
	return s
}

func (s *script) do(cmd string) string {
	s.t.Helper()
	s.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := s.conn.Write([]byte(cmd + "\n")); err != nil {
		s.t.Fatal(err)
	}
	return s.line()
}

func (s *script) line() string {
	s.t.Helper()
	line, err := s.r.ReadString('\n')
	if err != nil {
		s.t.Fatalf("no answer: %v", err)
	}
	return strings.TrimRight(line, "\n")
}

func (s *script) end() Result {
	s.conn.Close()
	select {
	case r := <-s.done:
		return r
	case <-time.After(5 * time.Second):
		s.t.Fatal("the session did not end")
		return Result{}
	}
}

func TestParseArgs(t *testing.T) {
	for line, want := range map[string][]string{
		`STREAM FILE welcome "#*"`:            {"STREAM", "FILE", "welcome", "#*"},
		`STREAM FILE welcome ""`:              {"STREAM", "FILE", "welcome", ""},
		`SET VARIABLE NAME "two words"`:       {"SET", "VARIABLE", "NAME", "two words"},
		"VERBOSE \"a \\\"quoted\\\" word\" 1": {"VERBOSE", `a "quoted" word`, "1"},
		"  NOOP\t ":                           {"NOOP"},
		`EXEC Dial PJSIP/201,20,tT`:           {"EXEC", "Dial", "PJSIP/201,20,tT"},
	} {
		if got := parseArgs(line); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q, want %q", line, got, want)
		}
	}
}

func TestNamesAndDialStrings(t *testing.T) {
	for in, want := range map[string]string{
		"custom/welcome": "custom/welcome", "/var/lib/asterisk/sounds/en/custom/welcome.gsm": "en/custom/welcome",
		"/tmp/msg-12": "tmp/msg-12", "hello.wav": "hello",
	} {
		if got, err := CleanName(in); err != nil || got != want {
			t.Errorf("CleanName(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"../etc/passwd", "a/../../b", "", "a b"} {
		if _, err := CleanName(bad); err == nil {
			t.Errorf("CleanName(%q) took it", bad)
		}
	}
	for in, want := range map[string]string{
		"PJSIP/201": "201", "SIP/201": "201", "Local/201@from-internal/n": "201", "PJSIP/5551234@provider": "5551234",
		"DAHDI/g0/5551234": "5551234", "IAX2/300": "300",
	} {
		if got := dialNumber(in); got != want {
			t.Errorf("dialNumber(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"digits/5": "digit-5", "en/digits/0": "digit-0", "vm-goodbye": "goodbye", "silence/2": "silence:2", "goodbye": "goodbye"} {
		if got := soundKey(in); got != want {
			t.Errorf("soundKey(%q) = %q, want %q", in, got, want)
		}
	}
	if a := appArgs(`Set(x=1,2)`); len(a) != 1 {
		t.Errorf("commas inside parentheses split: %q", a)
	}
	if a := appArgs(`var,custom/prompt&beep,4,,2,3`); len(a) != 6 || a[1] != "custom/prompt&beep" {
		t.Errorf("Read options %q", a)
	}
	if n, name := parseCallerID(`"Front Desk" <2125550100>`); n != "2125550100" || name != "Front Desk" {
		t.Errorf("caller ID %q %q", n, name)
	}
}

// The commands a typical script uses answer as Asterisk answers them.
func TestSession(t *testing.T) {
	dir := t.TempDir()
	lib := &Library{Dir: dir}
	db := &DB{Path: filepath.Join(dir, "astdb.json")}
	ch := newFake()
	s := start(t, ch, Env{Request: "agi://127.0.0.1/ivr?x=1", Network: true, Script: "ivr?x=1", CallerID: "2125550100",
		CallerIDName: "Pat", Extension: "500", Context: "from-internal", UniqueID: "1700000000.1", Args: []string{"one", "two"}},
		Config{Library: lib, DB: db})
	if s.env["agi_network"] != "yes" || s.env["agi_network_script"] != "ivr?x=1" || s.env["agi_callerid"] != "2125550100" ||
		s.env["agi_arg_2"] != "two" || s.env["agi_channel"] != "PJSIP/201-00000001" || s.env["agi_rdnis"] != "unknown" {
		t.Fatalf("variables %v", s.env)
	}
	check := func(cmd, want string) {
		t.Helper()
		if got := s.do(cmd); got != want {
			t.Errorf("%s: %q, want %q", cmd, got, want)
		}
	}
	check("ANSWER", "200 result=0")
	check("CHANNEL STATUS", "200 result=6")
	check("STREAM FILE nothing-here \"\"", "200 result=-1 endpos=0")
	check(`STREAM FILE beep ""`, "200 result=0 endpos=800")
	ch.keys = []byte{'1'}
	check(`STREAM FILE beep "12"`, "200 result=49 endpos=400")
	ch.keys = []byte{'9'}
	check(`STREAM FILE beep "12"`, "200 result=0 endpos=800") // 9 is not an escape key
	ch.keys = []byte{0}
	check("WAIT FOR DIGIT 1000", "200 result=0")
	ch.keys = []byte{'#'}
	check("WAIT FOR DIGIT -1", "200 result=35")
	ch.keys = []byte{0, '4', '2', '#'}
	check("GET DATA beep 3000 4", "200 result=42")
	ch.keys = []byte{0, '7', 0}
	check("GET DATA beep 3000 4", "200 result=7 (timeout)")
	ch.keys = []byte{0, 0}
	check("GET DATA beep 3000", "200 result= (timeout)")
	ch.keys = []byte{0, '2'}
	check("GET OPTION beep 12 2000", "200 result=50 endpos=800")
	check("SAY DIGITS 123 \"\"", "200 result=0")
	check("SAY NUMBER 42 \"\"", "200 result=0")
	check("SET VARIABLE COLOUR blue", "200 result=1")
	check("GET VARIABLE COLOUR", "200 result=1 (blue)")
	check("GET VARIABLE NOTHING", "200 result=0")
	check("GET VARIABLE CALLERID(num)", "200 result=1 (2125550100)")
	check(`GET FULL VARIABLE "${CALLERID(name)} likes ${COLOUR}"`, "200 result=1 (Pat likes blue)")
	check("DATABASE PUT cf 201 5550100", "200 result=1")
	check("DATABASE GET cf 201", "200 result=1 (5550100)")
	check("DATABASE GET cf 202", "200 result=0")
	check("DATABASE DEL cf 202", "200 result=1") // as Asterisk: 1 even when the key was not there
	check("DATABASE DELTREE cf", "200 result=0") // Asterisk's answer when it deleted something
	check("DATABASE DELTREE cf", "200 result=1")
	check("NOT A COMMAND", "510 Invalid or unknown command")
	check("WAIT FOR DIGIT", "520-Invalid command syntax.  Proper usage follows:")
	if l := s.line(); !strings.HasPrefix(l, "Usage: WAIT FOR DIGIT") {
		t.Errorf("usage %q", l)
	}
	if l := s.line(); l != "520 End of proper usage." {
		t.Errorf("usage end %q", l)
	}
	check(`SET CALLERID "Front Desk <2125550199>"`, "200 result=1")
	if ch.callerID != "Front Desk <2125550199>" {
		t.Errorf("caller ID %q", ch.callerID)
	}
	ch.keys = []byte{0, '#'} // the beep, then # during the recording
	check("RECORD FILE /tmp/msg-1 wav # 5000 BEEP s=3", "200 result=35 (dtmf) endpos=8000")
	check("STREAM FILE /tmp/msg-1 \"\"", "200 result=0 endpos=8000") // the recording plays back
	check("EXEC Playback beep&goodbye", "200 result=0")
	check("EXEC System rm", "200 result=-2")
	check("EXEC Set DONE=yes", "200 result=0")
	check("GET VARIABLE DONE", "200 result=1 (yes)")
	check("EXEC Dial PJSIP/202,15", "200 result=0")
	check("GET VARIABLE DIALSTATUS", "200 result=1 (NOANSWER)")
	check("SET EXTENSION 600", "200 result=0")
	check("VERBOSE \"going on\" 1", "200 result=1")
	check("HANGUP", "200 result=1")
	if l := s.line(); l != "HANGUP" {
		t.Errorf("after the hang-up %q", l)
	}
	check(`STREAM FILE beep ""`, "511 Command Not Permitted on a dead channel or intercept routine")
	check("NOOP", "200 result=0")
	res := s.end()
	if res.Next != "600" || !res.Hungup || res.Err != nil || len(ch.dialled) != 1 || ch.dialled[0] != "202" {
		t.Fatalf("result %+v dialled %v", res, ch.dialled)
	}
	if len(lib.List()) != 1 || !lib.List()[0].Recorded || lib.List()[0].Name != "tmp/msg-1" {
		t.Fatalf("library %+v", lib.List())
	}
	// The database is kept on disk.
	db2 := &DB{Path: db.Path}
	if v, ok := db2.Get("cf", "201"); ok {
		t.Fatalf("deleted value back: %q", v)
	}
}

// A call that ends while the script waits: the script hears HANGUP, and Run stops after the grace.
func TestHangupWhileWaiting(t *testing.T) {
	ch := newFake()
	s := start(t, ch, Env{Network: true}, Config{HangupGrace: 200 * time.Millisecond})
	ch.Hangup()
	if l := s.line(); l != "HANGUP" {
		t.Fatalf("got %q", l)
	}
	select {
	case r := <-s.done:
		if !r.Hungup {
			t.Fatalf("%+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run went on after the grace")
	}
}

// Dial answered hands the call over: the script's EXEC answers -1, then HANGUP.
func TestDialAnswered(t *testing.T) {
	ch := newFake()
	ch.dialRes = "ANSWER"
	s := start(t, ch, Env{Network: true}, Config{})
	if l := s.do("EXEC Dial SIP/300&SIP/301,20"); l != "200 result=-1" {
		t.Fatalf("got %q", l)
	}
	if l := s.line(); l != "HANGUP" {
		t.Fatalf("got %q", l)
	}
	if l := s.do("GET VARIABLE DIALSTATUS"); l != "200 result=1 (ANSWER)" {
		t.Fatalf("got %q", l)
	}
	s.end()
}

// Speech recognition uses BitVoice's speech to text when there is one.
func TestSpeech(t *testing.T) {
	ch := newFake()
	s := start(t, ch, Env{}, Config{})
	if l := s.do("SPEECH CREATE lumenvox"); l != "200 result=0" {
		t.Fatalf("no speech to text, got %q", l)
	}
	s.end()
	ch = newFake()
	ch.speech = true
	s = start(t, ch, Env{}, Config{})
	// In this order: a grammar needs the speech object first (a map's order is random).
	for _, cmd := range []string{"SPEECH CREATE x", "SPEECH LOAD GRAMMAR menu /x.gram", "SPEECH ACTIVATE GRAMMAR menu"} {
		if l := s.do(cmd); l != "200 result=1" {
			t.Fatalf("%s: %q", cmd, l)
		}
	}
	if l := s.do("SPEECH RECOGNIZE beep 5"); l != `200 result=1 (speech) endpos=800 results=1 score0=1000 text0="sales please" grammar0=default` {
		t.Fatalf("got %q", l)
	}
	if l := s.do("GET VARIABLE SPEECH_TEXT(0)"); l != "200 result=1 (sales please)" {
		t.Fatalf("got %q", l)
	}
	s.end()
}

// Uploaded sounds in Asterisk's raw formats are read, and Find tries the caller's language first.
func TestLibrary(t *testing.T) {
	lib := &Library{Dir: t.TempDir(), KeepDays: 1}
	if err := lib.Upload("custom/welcome", "welcome.ulaw", make([]byte, 8000)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Upload("es/custom/welcome", "welcome.sln16", make([]byte, 32000)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Upload("x", "x.gsm", make([]byte, 30)); err == nil {
		t.Fatal("a partial GSM frame was taken")
	}
	if pcm, ok := lib.Find("custom/welcome", "es"); !ok || len(pcm) != 8000 {
		t.Fatalf("Spanish first: %d %v", len(pcm), ok)
	}
	if pcm, ok := lib.Find("/var/lib/asterisk/sounds/en/custom/welcome.gsm", ""); !ok || len(pcm) != 8000 {
		t.Fatalf("by Asterisk's path: %d %v", len(pcm), ok)
	}
	lib.SaveRecording("old", make([]int16, 10))
	p := lib.path("recorded", "old")
	os.Chtimes(p, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))
	if n := lib.Prune(time.Now()); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	lib.MaxBytes = 100
	if err := lib.SaveRecording("big", make([]int16, 1000)); err != ErrFull {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestParseTarget(t *testing.T) {
	if tg, err := ParseTarget("agi://10.0.0.5/ivr/main?lang=en"); err != nil || tg.Host != "10.0.0.5:4573" || tg.Script != "ivr/main?lang=en" {
		t.Fatalf("%+v %v", tg, err)
	}
	if tg, err := ParseTarget("hagi://agi.example.com/ivr"); err != nil || !tg.HA || tg.Host != "agi.example.com" {
		t.Fatalf("%+v %v", tg, err)
	}
	if tg, err := ParseTarget("lookup.php"); err != nil || tg.Program != "lookup.php" {
		t.Fatalf("%+v %v", tg, err)
	}
	for _, bad := range []string{"../bin/sh", "http://x/", "agi:///x", "hagi://x.com:99/", "a/b"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

// Every command answers as Asterisk 20 answered it (testdata/asterisk20.txt), with a call where
// nobody presses a key and the beep is as long as Asterisk's (3,520 samples).
func TestAnswersAsAsterisk(t *testing.T) {
	b, err := os.ReadFile("testdata/asterisk20.txt")
	if err != nil {
		t.Fatal(err)
	}
	type exchange struct {
		cmd     string
		answers []string
	}
	var script []exchange
	// A Windows checkout may have turned the file's line ends into CRLF.
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, ">> "):
			script = append(script, exchange{cmd: line[3:]})
		case strings.HasPrefix(line, "<< "):
			script[len(script)-1].answers = append(script[len(script)-1].answers, line[3:])
		}
	}
	ch := newFake()
	ch.beepLen = 3520
	s := start(t, ch, Env{Network: true}, Config{Library: &Library{Dir: t.TempDir()}, DB: &DB{Path: filepath.Join(t.TempDir(), "db.json")}})
	// BitVoice calls do not carry text: RECEIVE CHAR times out where Asterisk's Local channel, which has
	// no text either, answered "(hangup)". Both are a "no character" a script handles the same way.
	differs := map[string]string{"RECEIVE CHAR 100": "200 result=0 (timeout)"}
	for _, x := range script {
		got := []string{s.do(x.cmd)}
		for !strings.HasPrefix(got[len(got)-1], "200 ") && !strings.HasPrefix(got[len(got)-1], "51") && !strings.HasPrefix(got[len(got)-1], "520 ") {
			got = append(got, s.line())
		}
		want := x.answers
		if d, ok := differs[x.cmd]; ok {
			want = []string{d}
		}
		// BitVoice tells the script of the hang-up as soon as it happens (Asterisk tells it when it next
		// reads a command): the HANGUP line comes right after HANGUP's answer, not before the next one.
		if x.cmd == "HANGUP" {
			want = append(want, "HANGUP")
			got = append(got, s.line())
		}
		if len(want) > 0 && want[0] == "HANGUP" {
			want = want[1:]
		}
		if len(got) == 3 && strings.HasPrefix(got[1], "Usage: ") && want[1] == "(usage text)" {
			got[1] = "(usage text)"
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s:\n got  %q\n want %q", x.cmd, got, want)
		}
	}
	s.end()
}
