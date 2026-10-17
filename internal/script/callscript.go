package script

// Call scripts run a whole call, not only choose where it goes: they play prompts and speech, collect
// keys, record messages, ring people and hand the call on. They are written as plain top-to-bottom
// code, each step waiting for its end (no callbacks). The same sandbox holds as for routing scripts,
// and a few limits more: the script's own code may run at most StepLimit between two steps, a call
// takes at most MaxSteps steps, and the whole script stops after the time its owner allows. When the
// script fails, stops, or ends without hanging up or handing the call on, the call goes to the script's
// fallback: it is never dropped by a script.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	lua "github.com/yuin/gopher-lua"
)

// Destination is where a call script sends the call: a number (an extension, ring group or queue) or
// a destination of any type.
type Destination struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

func (d Destination) String() string {
	if d.Type == "" || d.Type == "number" {
		return d.Target
	}
	if d.Target == "" {
		return d.Type
	}
	return d.Type + " " + d.Target
}

// CollectOptions say how keys are gathered.
type CollectOptions struct {
	Max        int     // digits at most (1 to 32, default 32)
	Timeout    float64 // seconds to wait for the first key (default 5)
	InterDigit float64 // seconds to wait for each next key (default 3)
	End        string  // the key that ends early ("#" by default; "" for none)
	Prompt     string  // a prompt to play first (a key interrupts it and counts)
	Say        string  // words to say first (a key interrupts them and counts)
}

// RecordOptions say where a recording goes: a message in a mailbox.
type RecordOptions struct {
	Box     string
	Seconds int  // the longest (default and maximum: the mailbox's own)
	Beep    bool // a beep first
}

// Host is what a call script may do with its call. Every method waits until it is done. A method
// returns ErrCallEnded once the caller has hung up, and ErrHandedOver once the call was handed on.
type Host interface {
	Play(name string) (key string, err error)
	Say(text string) (key string, err error)
	Collect(o CollectOptions) (digits string, err error)
	Record(o RecordOptions) (seconds int, err error)
	Wait(seconds float64) (key string, err error)
	// Dial rings a destination for the caller. When someone answers they talk and the call is theirs:
	// ErrHandedOver. Nobody answering returns false and the script carries on.
	Dial(d Destination, ringSeconds int) (answered bool, err error)
	Transfer(d Destination) error
	Hangup() error
}

var (
	ErrCallEnded  = errors.New("the caller hung up")
	ErrHandedOver = errors.New("the call was handed on")
	// ErrTooManySteps stops a script that loops through steps without end.
	ErrTooManySteps = fmt.Errorf("the script took more than %d steps", MaxSteps)
)

const (
	// StepLimit is how long the script's own code may run between two steps.
	StepLimit = 250 * time.Millisecond
	// MaxSteps is how many steps (play, say, collect...) one call may take.
	MaxSteps = 1000
	// DefaultMinutes is how long a call script may run when its owner does not say.
	DefaultMinutes = 10
)

// Outcome is how a call script ended.
type Outcome struct {
	// Ended: the script hung up, transferred, handed the call to someone who answered, or the caller left.
	Ended bool
	// Next is where the script sent the call by returning a destination (Ended is false then).
	Next  *Destination
	Steps []string
	Log   []string
}

// session runs one call script; its fields are shared by the script's goroutine and the watchdog.
type session struct {
	host     Host
	deadline time.Time
	maxRun   time.Duration

	mu      sync.Mutex
	inStep  bool
	since   time.Time // when the script's own code last started running
	stopErr error     // set once: why the script must stop
	out     Outcome
	steps   int

	interrupt func(error)
}

func (s *session) stop(err error) {
	s.mu.Lock()
	first := s.stopErr == nil
	if first {
		s.stopErr = err
	}
	s.mu.Unlock()
	if first && s.interrupt != nil {
		s.interrupt(err)
	}
}

func (s *session) stopped() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopErr
}

// step brackets one host call: the watchdog does not count its time, and an ending step stops the script.
func (s *session) step(what string, f func() error) error {
	if err := s.stopped(); err != nil {
		return err
	}
	s.mu.Lock()
	s.steps++
	if s.steps > MaxSteps {
		s.mu.Unlock()
		s.stop(ErrTooManySteps)
		return ErrTooManySteps
	}
	if len(s.out.Steps) < 200 {
		s.out.Steps = append(s.out.Steps, what)
	}
	s.inStep = true
	s.mu.Unlock()
	err := f()
	s.mu.Lock()
	s.inStep = false
	s.since = time.Now()
	s.mu.Unlock()
	switch {
	case errors.Is(err, ErrCallEnded), errors.Is(err, ErrHandedOver):
		s.mu.Lock()
		s.out.Ended = true
		s.mu.Unlock()
		s.stop(err)
		return err
	}
	return err
}

func (s *session) log(line string) {
	s.mu.Lock()
	if len(s.out.Log) < 50 {
		s.out.Log = append(s.out.Log, line)
	}
	s.mu.Unlock()
}

// watch stops the script when its own code runs too long between steps, or the whole run too long.
func (s *session) watch(done <-chan struct{}) {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			s.mu.Lock()
			busy := !s.inStep && now.Sub(s.since) > StepLimit
			late := now.After(s.deadline)
			s.mu.Unlock()
			switch {
			case late:
				s.stop(fmt.Errorf("the script ran longer than it may (%s) and was stopped", s.maxRun))
				return
			case busy:
				s.stop(ErrTimeout)
				return
			}
		}
	}
}

// RunCall runs a call script for one call. maxRun bounds the whole script (DefaultMinutes when zero).
// An error means the script failed or was stopped: the caller sends the call to the fallback unless
// the outcome says it Ended.
func RunCall(language, source string, c Call, h Host, maxRun time.Duration) (Outcome, error) {
	if maxRun <= 0 {
		maxRun = DefaultMinutes * time.Minute
	}
	now := time.Now()
	s := &session{host: h, deadline: now.Add(maxRun), maxRun: maxRun, since: now}
	done := make(chan struct{})
	defer close(done)
	var err error
	switch language {
	case "js", "javascript":
		err = s.runJS(source, c, done)
	case "lua":
		err = s.runLua(source, c, done)
	default:
		return Outcome{}, fmt.Errorf("unknown script language %q", language)
	}
	s.mu.Lock()
	out := s.out
	stopErr := s.stopErr
	s.mu.Unlock()
	if errors.Is(stopErr, ErrCallEnded) || errors.Is(stopErr, ErrHandedOver) {
		return out, nil // the call is over or someone else's: nothing went wrong
	}
	if stopErr != nil {
		return out, stopErr
	}
	return out, err
}

func destOf(v any) (Destination, bool) {
	switch x := v.(type) {
	case string:
		return Destination{Type: "number", Target: strings.TrimSpace(x)}, strings.TrimSpace(x) != ""
	case int64:
		return Destination{Type: "number", Target: strconv.FormatInt(x, 10)}, true
	case float64:
		return Destination{Type: "number", Target: strconv.FormatFloat(x, 'f', -1, 64)}, true
	case map[string]any:
		d := Destination{}
		if t, ok := x["type"].(string); ok {
			d.Type = t
		}
		switch t := x["target"].(type) {
		case string:
			d.Target = t
		case int64:
			d.Target = strconv.FormatInt(t, 10)
		case float64:
			d.Target = strconv.FormatFloat(t, 'f', -1, 64)
		}
		return d, d.Type != ""
	}
	return Destination{}, false
}

func num(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

func str(m map[string]any, k string) (string, bool) {
	v, ok := m[k].(string)
	return v, ok
}

func collectOptions(m map[string]any) CollectOptions {
	o := CollectOptions{Max: 32, Timeout: 5, InterDigit: 3, End: "#"}
	if v, ok := num(m, "max"); ok {
		o.Max = min(max(int(v), 1), 32)
	}
	if v, ok := num(m, "timeout"); ok && v > 0 {
		o.Timeout = min(v, 60)
	}
	if v, ok := num(m, "interDigit"); ok && v > 0 {
		o.InterDigit = min(v, 30)
	}
	if v, ok := str(m, "end"); ok {
		o.End = v
	}
	o.Prompt, _ = str(m, "prompt")
	o.Say, _ = str(m, "say")
	return o
}

func recordOptions(m map[string]any) RecordOptions {
	o := RecordOptions{Beep: true}
	o.Box, _ = str(m, "box")
	if v, ok := num(m, "box"); ok {
		o.Box = strconv.Itoa(int(v))
	}
	if v, ok := num(m, "seconds"); ok {
		o.Seconds = int(v)
	}
	if v, ok := m["beep"].(bool); ok {
		o.Beep = v
	}
	return o
}

// --- JavaScript ------------------------------------------------------------------------------------

func (s *session) runJS(source string, c Call, done <-chan struct{}) (err error) {
	prog, err := goja.Compile("call.js", wrapJS(source), true)
	if err != nil {
		return cleanJS(err)
	}
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	s.interrupt = func(e error) { vm.Interrupt(e) }
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the script failed: %v", r)
		}
	}()
	// A step's error becomes an exception the script cannot carry on past: once the call ended or was
	// handed on, the session is stopped and the next instruction interrupts the script.
	throw := func(e error) { panic(vm.NewGoError(e)) }
	obj := func(v goja.Value) map[string]any {
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			return map[string]any{}
		}
		if m, ok := v.Export().(map[string]any); ok {
			return m
		}
		return map[string]any{}
	}
	dest := func(v goja.Value, what string) Destination {
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			throw(fmt.Errorf("%s needs a destination: a number, or {type, target}", what))
		}
		d, ok := destOf(v.Export())
		if !ok {
			throw(fmt.Errorf("%s needs a destination: a number, or {type, target}", what))
		}
		return d
	}
	_ = vm.Set("log", func(line string) { s.log(line) })
	_ = vm.Set("play", func(name string) string {
		var key string
		if e := s.step("play "+name, func() (e error) { key, e = s.host.Play(name); return }); e != nil {
			throw(e)
		}
		return key
	})
	_ = vm.Set("say", func(text string) string {
		var key string
		if e := s.step("say "+strconv.Quote(text), func() (e error) { key, e = s.host.Say(text); return }); e != nil {
			throw(e)
		}
		return key
	})
	_ = vm.Set("collect", func(v goja.Value) string {
		o := collectOptions(obj(v))
		var got string
		if e := s.step(fmt.Sprintf("collect up to %d keys", o.Max), func() (e error) { got, e = s.host.Collect(o); return }); e != nil {
			throw(e)
		}
		return got
	})
	_ = vm.Set("record", func(v goja.Value) int {
		o := recordOptions(obj(v))
		var n int
		if e := s.step("record into mailbox "+o.Box, func() (e error) { n, e = s.host.Record(o); return }); e != nil {
			throw(e)
		}
		return n
	})
	_ = vm.Set("wait", func(seconds float64) string {
		var key string
		if e := s.step(fmt.Sprintf("wait %g s", seconds), func() (e error) { key, e = s.host.Wait(min(max(seconds, 0), 600)); return }); e != nil {
			throw(e)
		}
		return key
	})
	_ = vm.Set("dial", func(v goja.Value, ring goja.Value) bool {
		d := dest(v, "dial")
		secs := 30
		if ring != nil && !goja.IsUndefined(ring) {
			secs = min(max(int(ring.ToInteger()), 5), 300)
		}
		var ok bool
		if e := s.step("dial "+d.String(), func() (e error) { ok, e = s.host.Dial(d, secs); return }); e != nil {
			throw(e)
		}
		return ok
	})
	_ = vm.Set("transfer", func(v goja.Value) {
		d := dest(v, "transfer")
		if e := s.step("transfer to "+d.String(), func() error {
			if err := s.host.Transfer(d); err != nil {
				return err
			}
			return ErrHandedOver
		}); e != nil {
			throw(e)
		}
	})
	_ = vm.Set("hangup", func() {
		if e := s.step("hang up", func() error { _ = s.host.Hangup(); return ErrCallEnded }); e != nil {
			throw(e)
		}
	})
	go s.watch(done)
	fnv, err := vm.RunProgram(prog)
	if err != nil {
		return interrupted(err)
	}
	fn, ok := goja.AssertFunction(fnv)
	if !ok {
		return errors.New("not a function")
	}
	res, err := fn(goja.Undefined(), vm.ToValue(c))
	if err != nil {
		if stop := s.stopped(); stop != nil {
			return stop
		}
		return interrupted(err)
	}
	if res != nil && !goja.IsUndefined(res) && !goja.IsNull(res) {
		if d, ok := destOf(res.Export()); ok {
			s.mu.Lock()
			s.out.Next = &d
			s.mu.Unlock()
		}
	}
	return nil
}

// --- Lua -------------------------------------------------------------------------------------------

func luaTable(t *lua.LTable) map[string]any {
	m := map[string]any{}
	if t == nil {
		return m
	}
	t.ForEach(func(k, v lua.LValue) {
		ks, ok := k.(lua.LString)
		if !ok {
			return
		}
		switch x := v.(type) {
		case lua.LString:
			m[string(ks)] = string(x)
		case lua.LNumber:
			m[string(ks)] = float64(x)
		case lua.LBool:
			m[string(ks)] = bool(x)
		}
	})
	return m
}

func (s *session) runLua(source string, c Call, done <-chan struct{}) error {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 256, RegistrySize: 1024 * 16, RegistryMaxSize: 1024 * 256})
	defer L.Close()
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{{lua.BaseLibName, lua.OpenBase}, {lua.StringLibName, lua.OpenString}, {lua.TabLibName, lua.OpenTable}, {lua.MathLibName, lua.OpenMath}} {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "collectgarbage", "print", "require", "module", "newproxy"} {
		L.SetGlobal(name, lua.LNil)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s.interrupt = func(e error) { cancel(e) }
	L.SetContext(ctx)

	t := L.NewTable()
	for k, v := range map[string]lua.LValue{"from": lua.LString(c.From), "fromName": lua.LString(c.FromName), "to": lua.LString(c.To),
		"direction": lua.LString(c.Direction), "trunk": lua.LString(c.Trunk), "weekday": lua.LNumber(c.Weekday),
		"hour": lua.LNumber(c.Hour), "minute": lua.LNumber(c.Minute), "date": lua.LString(c.Date)} {
		t.RawSetString(k, v)
	}
	L.SetGlobal("call", t)
	fail := func(L *lua.LState, e error) int { L.RaiseError("%s", e.Error()); return 0 }
	dest := func(L *lua.LState, what string) (Destination, bool) {
		var d Destination
		var ok bool
		switch v := L.Get(1).(type) {
		case lua.LString:
			d, ok = destOf(string(v))
		case lua.LNumber:
			d, ok = destOf(float64(v))
		case *lua.LTable:
			d, ok = destOf(luaTable(v))
		}
		if !ok {
			L.RaiseError("%s needs a destination: a number, or {type=..., target=...}", what)
		}
		return d, ok
	}
	set := func(name string, f lua.LGFunction) { L.SetGlobal(name, L.NewFunction(f)) }
	set("log", func(L *lua.LState) int { s.log(L.CheckString(1)); return 0 })
	set("play", func(L *lua.LState) int {
		name := L.CheckString(1)
		var key string
		if e := s.step("play "+name, func() (e error) { key, e = s.host.Play(name); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LString(key))
		return 1
	})
	set("say", func(L *lua.LState) int {
		text := L.CheckString(1)
		var key string
		if e := s.step("say "+strconv.Quote(text), func() (e error) { key, e = s.host.Say(text); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LString(key))
		return 1
	})
	set("collect", func(L *lua.LState) int {
		o := collectOptions(luaTable(L.OptTable(1, nil)))
		var got string
		if e := s.step(fmt.Sprintf("collect up to %d keys", o.Max), func() (e error) { got, e = s.host.Collect(o); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LString(got))
		return 1
	})
	set("record", func(L *lua.LState) int {
		o := recordOptions(luaTable(L.OptTable(1, nil)))
		var n int
		if e := s.step("record into mailbox "+o.Box, func() (e error) { n, e = s.host.Record(o); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LNumber(n))
		return 1
	})
	set("wait", func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		var key string
		if e := s.step(fmt.Sprintf("wait %g s", secs), func() (e error) { key, e = s.host.Wait(min(max(secs, 0), 600)); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LString(key))
		return 1
	})
	set("dial", func(L *lua.LState) int {
		d, _ := dest(L, "dial")
		secs := min(max(L.OptInt(2, 30), 5), 300)
		var ok bool
		if e := s.step("dial "+d.String(), func() (e error) { ok, e = s.host.Dial(d, secs); return }); e != nil {
			return fail(L, e)
		}
		L.Push(lua.LBool(ok))
		return 1
	})
	set("transfer", func(L *lua.LState) int {
		d, _ := dest(L, "transfer")
		e := s.step("transfer to "+d.String(), func() error {
			if err := s.host.Transfer(d); err != nil {
				return err
			}
			return ErrHandedOver
		})
		return fail(L, e)
	})
	set("hangup", func(L *lua.LState) int {
		e := s.step("hang up", func() error { _ = s.host.Hangup(); return ErrCallEnded })
		return fail(L, e)
	})
	fn, err := L.LoadString(source)
	if err != nil {
		return err
	}
	go s.watch(done)
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		if stop := s.stopped(); stop != nil {
			return stop
		}
		msg := err.Error()
		if i := strings.Index(msg, "\nstack traceback"); i > 0 {
			msg = msg[:i]
		}
		return errors.New(msg)
	}
	if r, ok := L.Get(-1).(*lua.LTable); ok {
		if d, ok := destOf(luaTable(r)); ok {
			s.mu.Lock()
			s.out.Next = &d
			s.mu.Unlock()
		}
	}
	return nil
}

// Simulator is a Host with no call behind it, for trying a call script in the editor: the caller
// presses Keys (one entry per collect), and someone answers each dial when Answer says so.
type Simulator struct {
	Keys   []string
	Answer bool
	// Exists says whether a destination is there (nil: everything is).
	Exists func(Destination) bool
	next   int
}

func (s *Simulator) Play(string) (string, error) { return "", nil }
func (s *Simulator) Say(string) (string, error)  { return "", nil }
func (s *Simulator) Collect(o CollectOptions) (string, error) {
	if s.next >= len(s.Keys) {
		return "", nil
	}
	k := s.Keys[s.next]
	s.next++
	if o.End != "" {
		k, _, _ = strings.Cut(k, o.End)
	}
	if len(k) > o.Max {
		k = k[:o.Max]
	}
	return k, nil
}
func (s *Simulator) Record(o RecordOptions) (int, error) {
	if s.Exists != nil && !s.Exists(Destination{Type: "voicemail", Target: o.Box}) {
		return 0, fmt.Errorf("there is no mailbox %s", o.Box)
	}
	if o.Seconds > 0 {
		return min(o.Seconds, 10), nil
	}
	return 10, nil
}
func (s *Simulator) Wait(float64) (string, error) { return "", nil }
func (s *Simulator) Dial(d Destination, _ int) (bool, error) {
	if s.Exists != nil && !s.Exists(d) {
		return false, fmt.Errorf("there is no %s here", d)
	}
	if s.Answer {
		return true, ErrHandedOver
	}
	return false, nil
}
func (s *Simulator) Transfer(d Destination) error {
	if s.Exists != nil && !s.Exists(d) {
		return fmt.Errorf("there is no %s here", d)
	}
	return nil
}
func (s *Simulator) Hangup() error { return nil }
