// Package agi runs Asterisk Gateway Interface scripts on BitVoice calls, so the IVRs and call-centre
// integrations written for Asterisk keep working: FastAGI servers over TCP (agi:// and hagi://, with
// an SRV lookup) and programs on this server (AGI, and EAGI with the caller's audio on file
// descriptor 3). The script reads the agi_ variables, then sends one command per line and reads one
// answer per command ("200 result=..."), as with Asterisk.
//
// BitVoice has no dialplan: where a script would leave Asterisk's dialplan at a context, extension
// and priority (SET EXTENSION, EXEC Goto), the call goes to that number here when the script ends.
package agi

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Channel is a call as an AGI script runs it. BitVoice's call engine provides it.
type Channel interface {
	// Name is the channel name scripts see (agi_channel, CHANNEL).
	Name() string
	// Hungup is closed when the call has ended, or been handed on to something else.
	Hungup() <-chan struct{}
	// Answer answers the call (a no-op when it already is).
	Answer() error
	// Up says whether the call is answered.
	Up() bool
	// Play plays audio until it ends, a key in escape is pressed, or the call ends. It returns the
	// key (0 for none), how many samples were played, and whether the call ended. Keys not in escape
	// are ignored. "" escapes nothing.
	Play(pcm []int16, escape string) (key byte, played int, hungup bool)
	// WaitKey waits for any key for up to d (forever when d < 0).
	WaitKey(d time.Duration) (key byte, hungup bool)
	// Record records the caller until max, silence seconds of quiet (0: no limit), a key in escape,
	// or the call ending. Reason is "dtmf", "timeout" (time or silence) or "hangup".
	Record(max, silence time.Duration, escape string) (pcm []int16, key byte, reason string)
	// Sound returns a sound BitVoice has itself: a prompt from Sounds ("goodbye", "digit-1") in the
	// caller's language, "digits:123", or a tone ("beep").
	Sound(name string) ([]int16, bool)
	// Speak turns words into speech with the speech service in Settings, AI; false when there is none.
	Speak(text string) ([]int16, bool)
	// Transcribe writes down what the caller said with the speech-to-text in Settings, AI; false when
	// there is none.
	Transcribe(pcm []int16) (string, bool)
	// Music plays hold music to the caller until the function returned is called.
	Music() (stop func())
	// SetCallerID changes the caller ID the call carries from now on.
	SetCallerID(number, name string)
	// Dial rings a number (an extension, ring group, queue or an outside number through the outbound
	// routes) for up to ring. When someone answers, the call is theirs: answered is true and Dial
	// returns once that call has ended. Status is ANSWER, NOANSWER, BUSY or CHANUNAVAIL.
	Dial(number string, ring time.Duration) (status string, answered bool)
	// HandOver sends the call to a BitVoice destination named the Asterisk way: "voicemail" with the
	// mailbox, or "queue" with the queue's number. The script sees the call end.
	HandOver(kind, target string) error
	// Hangup ends the call.
	Hangup()
	// Log notes a line from the script (VERBOSE, NoOp, Log) in the server log.
	Log(msg string)
}

// Env is what the script is told about the call before its first command (the agi_ variables).
type Env struct {
	Request      string // the agi:// URL or the program's name
	Language     string
	Type         string // the channel technology: PJSIP
	UniqueID     string
	Version      string // BitVoice's version
	CallerID     string
	CallerIDName string
	DNID         string // the number dialled
	RDNIS        string // the number it was forwarded from
	Context      string
	Extension    string
	AccountCode  string
	ThreadID     string
	Enhanced     bool     // EAGI: the caller's audio is on file descriptor 3
	Network      bool     // FastAGI
	Script       string   // FastAGI: the path after the server's address
	Args         []string // agi_arg_1, agi_arg_2 ...
}

// Config is what a session needs besides the call: where sounds are, and the database.
type Config struct {
	Library *Library
	DB      *DB
	// MaxTime ends a script that runs longer (default an hour). The call goes on to Next or the
	// fallback, as when the script ends.
	MaxTime time.Duration
	// HangupGrace is how long a script may go on after the call ended before BitVoice closes the
	// connection (default 30 s; Asterisk waits for the script).
	HangupGrace time.Duration
	// OnHangup is called once when the call ends while the script runs: a program gets SIGHUP.
	OnHangup func()
}

// Result is how a session ended.
type Result struct {
	// Next is the number the script chose to go on to (SET EXTENSION, EXEC Goto), "" for none.
	Next string
	// Hungup says the call ended (or was handed on) while the script ran.
	Hungup bool
	// Commands is how many commands the script sent.
	Commands int
	// Err is why the script ended early: a broken connection, the time limit.
	Err error
}

// ErrTime is a script ended by the time limit.
var ErrTime = errors.New("agi: the script ran longer than its time limit")

// session is one script running one call.
type session struct {
	ch   Channel
	env  Env
	cfg  Config
	w    io.Writer
	wmu  sync.Mutex
	vars map[string]string

	dead     bool // the call has ended: only commands allowed on a dead channel run
	next     string
	autoHang *time.Timer
	speech   *speechState
	commands int
}

// Run runs a script over rw: it sends the variables, then answers commands until the script closes
// the connection (or ends), the time limit, or HangupGrace after the call ended. Run does not close rw.
func Run(rw io.ReadWriter, ch Channel, env Env, cfg Config) Result {
	if cfg.MaxTime <= 0 {
		cfg.MaxTime = time.Hour
	}
	if cfg.HangupGrace <= 0 {
		cfg.HangupGrace = 30 * time.Second
	}
	s := &session{ch: ch, env: env, cfg: cfg, w: rw, vars: map[string]string{}}
	defer func() {
		if s.autoHang != nil {
			s.autoHang.Stop()
		}
	}()
	if err := s.sendEnv(); err != nil {
		return Result{Err: err}
	}
	lines := make(chan string)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		r := bufio.NewReaderSize(rw, 8192)
		for {
			line, err := readLine(r)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case lines <- line:
			case <-stop:
				return
			}
		}
	}()
	limit := time.NewTimer(cfg.MaxTime)
	defer limit.Stop()
	var grace <-chan time.Time
	hungup := ch.Hungup()
	res := Result{}
	for {
		select {
		case <-hungup:
			hungup = nil
			s.markDead()
			grace = time.After(cfg.HangupGrace)
			continue
		case <-limit.C:
			res.Err = ErrTime
		case <-grace:
			// The script went on after the hang-up: BitVoice stops waiting for it.
		case err := <-readErr:
			if !errors.Is(err, io.EOF) {
				res.Err = err
			}
		case line := <-lines:
			if strings.HasPrefix(strings.ToLower(line), "failure") {
				res.Err = errors.New("agi: the script could not start")
				break
			}
			if line == "" {
				continue
			}
			s.commands++
			s.handle(line)
			// A command may have ended the call (HANGUP, Dial answered and ended): say so at once.
			if hungup != nil && s.isGone() {
				hungup = nil
				s.markDead()
				grace = time.After(cfg.HangupGrace)
			}
			continue
		}
		break
	}
	res.Next, res.Hungup, res.Commands = s.next, s.dead, s.commands
	return res
}

// readLine reads one command, up to 8 KB; a longer line is cut there.
func readLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		frag, isPrefix, err := r.ReadLine()
		if err != nil {
			if b.Len() > 0 && errors.Is(err, io.EOF) {
				return b.String(), nil
			}
			return "", err
		}
		if b.Len()+len(frag) <= 8192 {
			b.Write(frag)
		}
		if !isPrefix {
			return strings.TrimRight(b.String(), "\r"), nil
		}
	}
}

func (s *session) isGone() bool {
	select {
	case <-s.ch.Hungup():
		return true
	default:
		return false
	}
}

// markDead switches to dead-channel mode and tells the script, as Asterisk does: HANGUP on the
// connection for FastAGI, SIGHUP for a program.
func (s *session) markDead() {
	if s.dead {
		return
	}
	s.dead = true
	if s.env.Network {
		s.send("HANGUP")
	}
	if s.cfg.OnHangup != nil {
		s.cfg.OnHangup()
	}
}

func (s *session) send(line string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := io.WriteString(s.w, line+"\n")
	return err
}

func (s *session) sendEnv() error {
	e := s.env
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	var b strings.Builder
	if e.Network {
		b.WriteString("agi_network: yes\n")
		if e.Script != "" {
			fmt.Fprintf(&b, "agi_network_script: %s\n", e.Script)
		}
	}
	enhanced := "0.0"
	if e.Enhanced {
		enhanced = "1.0"
	}
	for _, kv := range [][2]string{
		{"request", e.Request}, {"channel", s.ch.Name()}, {"language", or(e.Language, "en")},
		{"type", or(e.Type, "PJSIP")}, {"uniqueid", e.UniqueID}, {"version", e.Version},
		{"callerid", or(e.CallerID, "unknown")}, {"calleridname", or(e.CallerIDName, "unknown")},
		{"callingpres", "0"}, {"callingani2", "0"}, {"callington", "0"}, {"callingtns", "0"},
		{"dnid", or(e.DNID, "unknown")}, {"rdnis", or(e.RDNIS, "unknown")},
		{"context", or(e.Context, "default")}, {"extension", or(e.Extension, "s")}, {"priority", "1"},
		{"enhanced", enhanced}, {"accountcode", e.AccountCode}, {"threadid", or(e.ThreadID, "1")},
	} {
		fmt.Fprintf(&b, "agi_%s: %s\n", kv[0], oneLine(kv[1]))
	}
	for i, a := range e.Args {
		fmt.Fprintf(&b, "agi_arg_%d: %s\n", i+1, oneLine(a))
	}
	b.WriteString("\n")
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := io.WriteString(s.w, b.String())
	return err
}

// oneLine keeps a value from breaking the variables block.
func oneLine(v string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(v) }

// result sends "200 result=N" with an optional tail ("(timeout)", "endpos=0").
func (s *session) result(n int, tail ...string) {
	line := "200 result=" + strconv.Itoa(n)
	for _, t := range tail {
		if t != "" {
			line += " " + t
		}
	}
	s.send(line)
}
