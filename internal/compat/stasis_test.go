package compat

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// fakeChannel is a call in an application, without the engine: it plays in a few milliseconds and
// records a second of tone.
type fakeChannel struct {
	id    string
	mu    sync.Mutex
	key   func(byte)
	vars  map[string]string
	gone  chan struct{}
	ended bool
	left  *config.Destination
	inBr  *media.Bridge
	plays []int
	once  sync.Once
}

func newFakeChannel(id string) *fakeChannel {
	return &fakeChannel{id: id, gone: make(chan struct{}), vars: map[string]string{}}
}
func (f *fakeChannel) ID() string     { return f.id + "-1" }
func (f *fakeChannel) CallID() string { return f.id }
func (f *fakeChannel) Info() pbx.CallInfo {
	return pbx.CallInfo{ID: f.id, State: "answered", Direction: "internal", From: "100", FromName: "Reception", To: "700", Start: time.Now(), Legs: make([]pbx.LegInfo, 1)}
}
func (f *fakeChannel) Gone() <-chan struct{} { return f.gone }
func (f *fakeChannel) Ended() bool           { f.mu.Lock(); defer f.mu.Unlock(); return f.ended }
func (f *fakeChannel) OnKey(k func(byte))    { f.mu.Lock(); f.key = k; f.mu.Unlock() }
func (f *fakeChannel) press(k byte) {
	f.mu.Lock()
	h := f.key
	f.mu.Unlock()
	h(k)
}
func (f *fakeChannel) Var(n string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vars[n]
	return v, ok
}
func (f *fakeChannel) SetVar(n, v string) { f.mu.Lock(); f.vars[n] = v; f.mu.Unlock() }
func (f *fakeChannel) Sound(n string) ([]int16, error) {
	if n == "nothing-here" {
		return nil, errors.New("no prompt")
	}
	return make([]int16, 80), nil
}
func (f *fakeChannel) Play(pcm []int16, stop <-chan struct{}) (bool, bool, error) {
	f.mu.Lock()
	f.plays = append(f.plays, len(pcm))
	f.mu.Unlock()
	select {
	case <-stop:
		return true, false, nil
	case <-time.After(20 * time.Millisecond):
		return false, false, nil
	}
}
func (f *fakeChannel) Record(maxSeconds, _ int, _ bool, terminateOn string, stop <-chan struct{}) (pbx.RecordResult, error) {
	select {
	case <-stop:
		return pbx.RecordResult{PCM: media.ToneSamples(media.ToneBeep), Reason: "stopped"}, nil
	case <-time.After(time.Duration(maxSeconds) * time.Second):
		return pbx.RecordResult{PCM: make([]int16, media.Rate*maxSeconds), Reason: "time"}, nil
	}
}
func (f *fakeChannel) JoinBridge(b *media.Bridge) error {
	f.mu.Lock()
	f.inBr = b
	f.mu.Unlock()
	return nil
}
func (f *fakeChannel) LeaveBridge() { f.mu.Lock(); f.inBr = nil; f.mu.Unlock() }
func (f *fakeChannel) Hangup(string) {
	f.mu.Lock()
	f.ended = true
	f.mu.Unlock()
	f.once.Do(func() { close(f.gone) })
}
func (f *fakeChannel) Continue(d *config.Destination) error {
	f.mu.Lock()
	f.left = d
	f.mu.Unlock()
	f.once.Do(func() { close(f.gone) })
	return nil
}

// appWS is an ARI program's events connection, read frame by frame.
type appWS struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialApp(t *testing.T, srv *httptest.Server, app string) *appWS {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	_, _ = conn.Write([]byte("GET /ari/events?app=" + app + "&api_key=crm:a%20long%20crm%20secret HTTP/1.1\r\nHost: x\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"))
	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if resp, err := http.ReadResponse(r, nil); err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade %v %v", resp, err)
	}
	return &appWS{t: t, conn: conn, r: r}
}

// next returns the next event of a type, skipping the generic channel events.
func (w *appWS) next(typ string) map[string]any {
	w.t.Helper()
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(w.r, hdr[:]); err != nil {
			w.t.Fatalf("waiting for %s: %v", typ, err)
		}
		n := int(hdr[1] & 0x7f)
		if n == 126 {
			var l [2]byte
			_, _ = io.ReadFull(w.r, l[:])
			n = int(l[0])<<8 | int(l[1])
		}
		msg := make([]byte, n)
		_, _ = io.ReadFull(w.r, msg)
		if hdr[0]&0x0f != 1 {
			continue
		}
		var ev map[string]any
		_ = json.Unmarshal(msg, &ev)
		if ev["type"] == typ {
			return ev
		}
	}
}

func TestARIApplication(t *testing.T) {
	f := newFake()
	f.cfg.System.Compat.Apps = []config.ARIApp{{Name: "ivr", Fallback: config.Destination{Type: "extension", Target: "100"}}}
	dir := t.TempDir()
	a := &ARI{PBX: f, Bus: events.New(), Started: time.Now(), Recordings: filepath.Join(dir, "rec"), Grace: 200 * time.Millisecond}
	srv := httptest.NewServer(a)
	defer srv.Close()
	do := func(method, path string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.SetBasicAuth("crm", "a long crm secret")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	// No program connected: the engine sends the call to the fallback.
	if a.enter("ivr", newFakeChannel("nobodyhome00"), nil) {
		t.Fatal("entered an application nobody runs")
	}
	app := dialApp(t, srv, "ivr")
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		a.st().mu.Lock()
		n := len(a.st().conns["ivr"])
		a.st().mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the program never connected")
		}
	}
	ch := newFakeChannel("callaaaa0001")
	if !a.enter("ivr", ch, []string{"inbound"}) {
		t.Fatal("not entered")
	}
	ev := app.next("StasisStart")
	if ev["args"].([]any)[0] != "inbound" || ev["channel"].(map[string]any)["id"] != "callaaaa0001-1" {
		t.Fatalf("StasisStart %v", ev)
	}
	if code, _ := do("POST", "/ari/channels/callaaaa0001-1/answer"); code != 204 {
		t.Fatalf("answer %d", code)
	}
	// Play two sounds; the playback starts and finishes.
	code, pb := do("POST", "/ari/channels/callaaaa0001-1/play?media=sound:hello-world,digits:42&playbackId=p1")
	if code != 201 || pb["id"] != "p1" || pb["target_uri"] != "channel:callaaaa0001-1" {
		t.Fatalf("play %d %v", code, pb)
	}
	if ev := app.next("PlaybackStarted"); ev["playback"].(map[string]any)["id"] != "p1" {
		t.Fatalf("%v", ev)
	}
	if ev := app.next("PlaybackFinished"); ev["playback"].(map[string]any)["state"] != "done" {
		t.Fatalf("%v", ev)
	}
	// A sound that is not there fails the playback, not the call.
	do("POST", "/ari/channels/callaaaa0001-1/play?media=sound:nothing-here")
	if ev := app.next("PlaybackFinished"); ev["playback"].(map[string]any)["state"] != "failed" {
		t.Fatalf("%v", ev)
	}
	// Keys reach the program.
	ch.press('5')
	if ev := app.next("ChannelDtmfReceived"); ev["digit"] != "5" {
		t.Fatalf("%v", ev)
	}
	// Variables.
	if code, _ := do("POST", "/ari/channels/callaaaa0001-1/variable?variable=LANG&value=fr"); code != 204 {
		t.Fatal(code)
	}
	if _, v := do("GET", "/ari/channels/callaaaa0001-1/variable?variable=LANG"); v["value"] != "fr" {
		t.Fatalf("%v", v)
	}
	// A recording, stopped by the program, then stored and fetched.
	code, rec := do("POST", "/ari/channels/callaaaa0001-1/record?name=msg1&format=wav&maxDurationSeconds=30&beep=true&terminateOn=%23")
	if code != 201 || rec["name"] != "msg1" {
		t.Fatalf("record %d %v", code, rec)
	}
	app.next("RecordingStarted")
	if code, _ := do("POST", "/ari/recordings/live/msg1/stop"); code != 204 {
		t.Fatal(code)
	}
	if ev := app.next("RecordingFinished"); ev["recording"].(map[string]any)["state"] != "done" {
		t.Fatalf("%v", ev)
	}
	if _, err := os.Stat(filepath.Join(dir, "rec", "msg1.wav")); err != nil {
		t.Fatal(err)
	}
	if code, _ := do("POST", "/ari/channels/callaaaa0001-1/record?name=msg1"); code != 409 {
		t.Fatalf("a second recording of the same name: %d", code)
	}
	if code, _ := do("POST", "/ari/channels/callaaaa0001-1/record?name=../x"); code != 400 {
		t.Fatalf("a path as a name: %d", code)
	}
	if code, r := do("GET", "/ari/recordings/stored/msg1"); code != 200 || r["format"] != "wav" {
		t.Fatalf("stored %d %v", code, r)
	}
	// Play the recording back.
	do("POST", "/ari/channels/callaaaa0001-1/play?media=recording:msg1")
	if ev := app.next("PlaybackFinished"); ev["playback"].(map[string]any)["state"] != "done" {
		t.Fatalf("%v", ev)
	}

	// A bridge with a second channel.
	ch2 := newFakeChannel("callbbbb0002")
	a.enter("ivr", ch2, nil)
	app.next("StasisStart")
	if code, b := do("POST", "/ari/bridges?type=mixing&bridgeId=room1"); code != 200 || b["id"] != "room1" {
		t.Fatalf("bridge %d %v", code, b)
	}
	if code, _ := do("POST", "/ari/bridges/room1/addChannel?channel=callaaaa0001-1,callbbbb0002-1"); code != 204 {
		t.Fatal(code)
	}
	app.next("ChannelEnteredBridge")
	if ev := app.next("ChannelEnteredBridge"); len(ev["bridge"].(map[string]any)["channels"].([]any)) != 2 {
		t.Fatalf("%v", ev)
	}
	if ch.inBr == nil || ch.inBr != ch2.inBr {
		t.Fatal("the two channels are not in one bridge")
	}
	if code, b := do("GET", "/ari/bridges/room1"); code != 200 || len(b["channels"].([]any)) != 2 {
		t.Fatalf("%d %v", code, b)
	}
	// The second caller hangs up: out of the bridge, StasisEnd.
	ch2.Hangup("x")
	app.next("ChannelLeftBridge")
	if ev := app.next("StasisEnd"); ev["channel"].(map[string]any)["id"] != "callbbbb0002-1" {
		t.Fatalf("%v", ev)
	}
	// Continue to an extension: the call leaves the application for there.
	if code, _ := do("POST", "/ari/channels/callaaaa0001-1/continue?extension=101"); code != 204 {
		t.Fatal(code)
	}
	app.next("StasisEnd")
	if ch.left == nil || *ch.left != (config.Destination{Type: "extension", Target: "101"}) || ch.inBr != nil {
		t.Fatalf("left to %+v, bridge %v", ch.left, ch.inBr)
	}
	if code, _ := do("DELETE", "/ari/bridges/room1"); code != 204 {
		t.Fatal(code)
	}

	// The program goes away: after the grace, its calls go to the fallback (Continue with nil).
	ch3 := newFakeChannel("callcccc0003")
	a.enter("ivr", ch3, nil)
	app.next("StasisStart")
	app.conn.Close()
	select {
	case <-ch3.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the orphaned call was never let go")
	}
	if ch3.left != nil {
		t.Fatalf("an orphan went to %+v, not the fallback", ch3.left)
	}
}
