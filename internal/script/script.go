// Package script runs routing scripts written in JavaScript (goja) or Lua (gopher-lua). A script sees one
// call and may return where it should go. Scripts are sandboxed (no files, network, process or module
// loading), stopped after a time limit, and every failure has a fixed answer: the caller of Route uses its
// fallback. A script can never drop a call, and emergency calls never reach one.
package script

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/dop251/goja"
	lua "github.com/yuin/gopher-lua"
)

// Call is what a script sees.
type Call struct {
	From      string `json:"from"`
	FromName  string `json:"fromName"`
	To        string `json:"to"`
	Direction string `json:"direction"` // inbound, internal
	Trunk     string `json:"trunk"`
	// Time is the local time as the script should reason about it.
	Weekday int    `json:"weekday"` // 0 Sunday
	Hour    int    `json:"hour"`
	Minute  int    `json:"minute"`
	Date    string `json:"date"` // YYYY-MM-DD
}

// Decision is where a script sends the call. Empty Type means "no opinion": use the fallback.
type Decision struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Log    []string
}

// Limit is how long a script may run per call.
const Limit = 200 * time.Millisecond

// ErrTimeout is returned when a script runs past Limit.
var ErrTimeout = errors.New("the script took too long and was stopped")

// Route runs a script for one call.
func Route(language, source string, c Call) (Decision, error) {
	switch language {
	case "js", "javascript":
		return routeJS(source, c)
	case "lua":
		return routeLua(source, c)
	}
	return Decision{}, fmt.Errorf("unknown script language %q", language)
}

// Check compiles a script without running it, for the editor.
func Check(language, source string) error {
	switch language {
	case "js", "javascript":
		_, err := goja.Compile("route.js", wrapJS(source), true)
		return cleanJS(err)
	case "lua":
		L := lua.NewState(lua.Options{SkipOpenLibs: true})
		defer L.Close()
		_, err := L.LoadString(source)
		return err
	}
	return fmt.Errorf("unknown script language %q", language)
}

// A JavaScript script is the body of function route(call); it returns {type, target} or nothing.
func wrapJS(source string) string {
	return "(function(call){\n" + source + "\n})"
}

var (
	jsLineRe  = regexp.MustCompile(`Line (\d+):(\d+)`)
	jsFrameRe = regexp.MustCompile(`(?:route|call)\.js:(\d+):(\d+)`)
)

// cleanJS turns a goja error into one sentence with line numbers counted in the person's own script
// (wrapJS adds one line above it).
func cleanJS(err error) error {
	if err == nil {
		return nil
	}
	ownLine := func(n string) int { v, _ := strconv.Atoi(n); return max(v-1, 1) }
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := ex.Value().String()
		if m := jsFrameRe.FindStringSubmatch(ex.Error()); m != nil {
			msg += fmt.Sprintf(" (line %d)", ownLine(m[1]))
		}
		return errors.New(msg)
	}
	return errors.New(jsLineRe.ReplaceAllStringFunc(err.Error(), func(s string) string {
		m := jsLineRe.FindStringSubmatch(s)
		return fmt.Sprintf("Line %d:%s", ownLine(m[1]), m[2])
	}))
}

func routeJS(source string, c Call) (d Decision, err error) {
	prog, err := goja.Compile("route.js", wrapJS(source), true)
	if err != nil {
		return d, cleanJS(err)
	}
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	// No require, no host objects: the runtime only has the language's own built-ins.
	_ = vm.Set("log", func(s string) {
		if len(d.Log) < 20 {
			d.Log = append(d.Log, s)
		}
	})
	timer := time.AfterFunc(Limit, func() { vm.Interrupt(ErrTimeout) })
	defer timer.Stop()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the script failed: %v", r)
		}
	}()
	fnv, err := vm.RunProgram(prog)
	if err != nil {
		return d, interrupted(err)
	}
	fn, ok := goja.AssertFunction(fnv)
	if !ok {
		return d, errors.New("not a function")
	}
	res, err := fn(goja.Undefined(), vm.ToValue(c))
	if err != nil {
		return d, interrupted(err)
	}
	if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		return d, nil
	}
	obj := res.ToObject(vm)
	if t := obj.Get("type"); t != nil && !goja.IsUndefined(t) {
		d.Type = t.String()
	}
	if t := obj.Get("target"); t != nil && !goja.IsUndefined(t) {
		d.Target = t.String()
	}
	return d, nil
}

func interrupted(err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		return ErrTimeout
	}
	return cleanJS(err)
}

// A Lua script is a chunk with `call` defined; it returns a table {type=..., target=...} or nothing.
func routeLua(source string, c Call) (d Decision, err error) {
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
	// The base library installs loaders and file access; remove them.
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "collectgarbage", "print", "require", "module", "newproxy"} {
		L.SetGlobal(name, lua.LNil)
	}
	t := L.NewTable()
	for k, v := range map[string]lua.LValue{"from": lua.LString(c.From), "fromName": lua.LString(c.FromName), "to": lua.LString(c.To),
		"direction": lua.LString(c.Direction), "trunk": lua.LString(c.Trunk), "weekday": lua.LNumber(c.Weekday),
		"hour": lua.LNumber(c.Hour), "minute": lua.LNumber(c.Minute), "date": lua.LString(c.Date)} {
		t.RawSetString(k, v)
	}
	L.SetGlobal("call", t)
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		if len(d.Log) < 20 {
			d.Log = append(d.Log, L.CheckString(1))
		}
		return 0
	}))
	ctx, cancel := context.WithTimeout(context.Background(), Limit)
	defer cancel()
	L.SetContext(ctx)
	fn, err := L.LoadString(source)
	if err != nil {
		return d, err
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		if ctx.Err() != nil {
			return d, ErrTimeout
		}
		msg := err.Error()
		if i := strings.Index(msg, "\nstack traceback"); i > 0 {
			msg = msg[:i]
		}
		return d, errors.New(msg)
	}
	if r, ok := L.Get(-1).(*lua.LTable); ok {
		d.Type = lua.LVAsString(r.RawGetString("type"))
		d.Target = lua.LVAsString(r.RawGetString("target"))
	}
	return d, nil
}

func init() { config.ScriptCheck = Check }
