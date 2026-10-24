package compat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

var (
	_ moreChannel = (*pbx.AppChannel)(nil)
	_ MorePBX     = (*pbx.Engine)(nil)
	_ SoundLister = (*pbx.Engine)(nil)
)

// morePBXFake is the fake PBX with what the rest of the ARI uses.
type morePBXFake struct {
	*fakePBX
	mu      sync.Mutex
	texts   []string
	dialled []string
}

func (f *morePBXFake) MailboxCounts(box string) (int, int, bool) {
	if box == "100" {
		return 2, 5, true
	}
	return 0, 0, false
}
func (f *morePBXFake) SendText(ext, from, text string) int {
	f.mu.Lock()
	f.texts = append(f.texts, ext+":"+text)
	f.mu.Unlock()
	return 1
}
func (f *morePBXFake) HoldMusicPCM() []int16 { return make([]int16, media.Rate/10) }
func (f *morePBXFake) CallIntoAppAs(id, ext, app string, args []string, name string, ring time.Duration, st func(string)) (string, error) {
	f.mu.Lock()
	f.dialled = append(f.dialled, ext)
	f.mu.Unlock()
	if st != nil {
		st("RINGING")
	}
	if id == "" {
		id = "orig0001"
	}
	return id + "-1", nil
}
func (f *morePBXFake) CallIntoApp(ext, app string, args []string, name string, ring time.Duration) (string, error) {
	return f.CallIntoAppAs("", ext, app, args, name, ring, nil)
}
func (f *morePBXFake) ARISounds() []pbx.ARISound {
	return []pbx.ARISound{{ID: "goodbye", Text: "Goodbye.", Languages: []string{"en", "es"}}, {ID: "custom/welcome", Languages: []string{"en"}}}
}

// moreFakeChannel adds mute, hold and the rest to the fake channel, and plays slowly enough to control.
type moreFakeChannel struct {
	*fakeChannel
	mu       sync.Mutex
	in, out  bool
	music    bool
	silenced int
	tap      func([]int16)
	paused   bool
}

func (f *moreFakeChannel) Mute(in, out, on bool) {
	f.mu.Lock()
	if in {
		f.in = on
	}
	if out {
		f.out = on
	}
	f.mu.Unlock()
}
func (f *moreFakeChannel) Muted() (bool, bool) { f.mu.Lock(); defer f.mu.Unlock(); return f.in, f.out }
func (f *moreFakeChannel) Music(on bool) error { f.mu.Lock(); f.music = on; f.mu.Unlock(); return nil }
func (f *moreFakeChannel) Silence()            { f.mu.Lock(); f.silenced++; f.mu.Unlock() }
func (f *moreFakeChannel) RTPStats() (media.Stats, media.Stats) {
	return media.Stats{Packets: 50, Bytes: 8000, Lost: 1}, media.Stats{Packets: 49, Bytes: 7840}
}
func (f *moreFakeChannel) Tap(fn func([]int16)) func() {
	f.mu.Lock()
	f.tap = fn
	f.mu.Unlock()
	go func() {
		for range 20 {
			f.mu.Lock()
			t := f.tap
			f.mu.Unlock()
			if t == nil {
				return
			}
			t(make([]int16, 160))
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return func() { f.mu.Lock(); f.tap = nil; f.mu.Unlock() }
}
func (f *moreFakeChannel) PauseRecording(on bool) { f.mu.Lock(); f.paused = on; f.mu.Unlock() }
func (f *moreFakeChannel) MuteRecording(on bool)  {}

// Play takes a second, so a playback can be controlled while it plays.
func (f *moreFakeChannel) Play(pcm []int16, stop <-chan struct{}) (bool, bool, error) {
	select {
	case <-stop:
		return true, false, nil
	case <-time.After(time.Duration(len(pcm)) * time.Second / media.Rate):
		return false, false, nil
	case <-f.gone:
		return false, true, nil
	}
}
func (f *moreFakeChannel) Sound(n string) ([]int16, error) {
	if n == "long" {
		return make([]int16, 30*media.Rate), nil
	}
	return f.fakeChannel.Sound(n)
}

type specOp struct{ method, path, model string }

func loadSpec(t *testing.T) ([]specOp, map[string][]string) {
	t.Helper()
	b, err := os.ReadFile("testdata/ari-spec.txt")
	if err != nil {
		t.Fatal(err)
	}
	var ops []specOp
	models := map[string][]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 4 && f[0] == "op":
			ops = append(ops, specOp{f[1], f[2], f[3]})
		case len(f) >= 2 && f[0] == "model":
			models[f[1]] = f[2:]
		}
	}
	return ops, models
}

// Every operation of Asterisk's published ARI is answered: carried out, or refused with 501 and the
// reason when BitVoice has no such thing (notOffered). What each answers has the fields its model
// requires.
func TestARICoversTheSpec(t *testing.T) {
	ops, models := loadSpec(t)
	if len(ops) < 100 {
		t.Fatalf("only %d operations in the spec file", len(ops))
	}
	f := &morePBXFake{fakePBX: newFake()}
	f.fakePBX.cfg.Extensions[0].Voicemail.Enabled = true
	f.fakePBX.cfg.System.Compat.Apps = []config.ARIApp{{Name: "ivr", Fallback: config.Destination{Type: "hangup"}}, {Name: "other", Fallback: config.Destination{Type: "hangup"}}}
	dir := t.TempDir()
	a := &ARI{PBX: f, Bus: events.New(), Started: time.Now(), Recordings: filepath.Join(dir, "rec"), Grace: time.Second}
	os.MkdirAll(a.Recordings, 0o700)
	os.WriteFile(filepath.Join(a.Recordings, "stored1.wav"), media.EncodeWAV(make([]int16, 800)), 0o600)
	srv := httptest.NewServer(a)
	defer srv.Close()
	app := dialApp(t, srv, "ivr")
	other := dialApp(t, srv, "other")
	waitConn := func(name string) {
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			a.st().mu.Lock()
			n := len(a.st().conns[name])
			a.st().mu.Unlock()
			if n > 0 {
				return
			}
			if time.Now().After(end) {
				t.Fatal("not connected")
			}
		}
	}
	waitConn("ivr")
	waitConn("other")
	_ = other
	ch := &moreFakeChannel{fakeChannel: newFakeChannel("callmore0001")}
	if !a.enter("ivr", ch, nil) {
		t.Fatal("not entered")
	}
	app.next("StasisStart")
	for _, id := range []string{"callmore0002", "callmore0003", "callmore0004"} {
		a.enter("ivr", &moreFakeChannel{fakeChannel: newFakeChannel(id)}, nil)
		app.next("StasisStart")
	}
	ch.SetVar("X", "1")

	do := func(method, path string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+"/ari"+path, rd)
		req.SetBasicAuth("crm", "a long crm secret")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	// Things to act on: a bridge, a long playback, a live recording, a created channel, a snoop.
	if code, b := do("POST", "/bridges?bridgeId=room1&type=mixing", nil); code != 200 {
		t.Fatalf("bridge %d %s", code, b)
	}
	if code, b := do("POST", "/channels/callmore0002-1/play?media=sound:long&playbackId=pb1", nil); code != 201 {
		t.Fatalf("play %d %s", code, b)
	}
	app.next("PlaybackStarted")
	// What the spec's GETs read before their POSTs make it.
	do("POST", "/bridges/room1/variable?variable=X&value=1", nil)
	do("PUT", "/deviceStates/Custom:lamp?deviceState=INUSE", nil)
	do("PUT", "/mailboxes/door@external?oldMessages=0&newMessages=1", nil)

	// The values each placeholder takes, and the query each operation needs.
	ids := map[string]string{"{channelId}": "callmore0001-1", "{bridgeId}": "room1", "{playbackId}": "pb1", "{recordingName}": "stored1",
		"{applicationName}": "ivr", "{deviceName}": "Custom:lamp", "{mailboxName}": "door@external", "{tech}": "PJSIP", "{resource}": "100",
		"{soundId}": "goodbye", "{eventName}": "hello", "{snoopId}": "snoop1", "{moduleName}": "res_ari.so", "{logChannelName}": "bitvoice",
		"{configClass}": "res_pjsip", "{objectType}": "endpoint", "{id}": "x", "{variableName}": "X"}
	query := map[string]string{
		"POST /channels":                                      "endpoint=PJSIP/100&app=ivr",
		"POST /channels/create":                               "endpoint=PJSIP/101&app=ivr&channelId=made0001",
		"POST /channels/{channelId}":                          "endpoint=PJSIP/101&app=ivr",
		"POST /channels/{channelId}/dial":                     "",
		"POST /channels/{channelId}/redirect":                 "endpoint=PJSIP/101",
		"POST /channels/{channelId}/dtmf":                     "dtmf=12",
		"POST /channels/{channelId}/play":                     "media=sound:goodbye",
		"POST /channels/{channelId}/play/{playbackId}":        "media=sound:goodbye",
		"POST /channels/{channelId}/record":                   "name=rec2&format=wav&maxDurationSeconds=1",
		"GET /channels/{channelId}/variable":                  "variable=X",
		"POST /channels/{channelId}/variable":                 "variable=X&value=1",
		"GET /channels/{channelId}/variables":                 "variables=X,Y",
		"POST /channels/{channelId}/snoop":                    "spy=in&app=ivr&snoopId=snoop0",
		"POST /channels/{channelId}/snoop/{snoopId}":          "spy=in&app=ivr",
		"POST /channels/{channelId}/move":                     "app=other",
		"POST /channels/{channelId}/transfer_progress":        "states=channel_progress",
		"POST /channels/externalMedia":                        "external_host=127.0.0.1:9&format=ulaw",
		"POST /bridges":                                       "bridgeId=room2",
		"POST /bridges/{bridgeId}":                            "",
		"GET /bridges/{bridgeId}/variable":                    "variable=X",
		"POST /bridges/{bridgeId}/variable":                   "variable=X&value=1",
		"GET /bridges/{bridgeId}/variables":                   "variables=X",
		"POST /bridges/{bridgeId}/addChannel":                 "channel=callmore0001-1",
		"POST /bridges/{bridgeId}/removeChannel":              "channel=callmore0001-1",
		"POST /bridges/{bridgeId}/play":                       "media=sound:goodbye",
		"POST /bridges/{bridgeId}/play/{playbackId}":          "media=sound:goodbye",
		"POST /bridges/{bridgeId}/record":                     "name=brec&maxDurationSeconds=1",
		"POST /playbacks/{playbackId}/control":                "operation=pause",
		"POST /recordings/stored/{recordingName}/copy":        "destinationRecordingName=stored2",
		"GET /asterisk/variable":                              "variable=G",
		"POST /asterisk/variable":                             "variable=G&value=1",
		"PUT /deviceStates/{deviceName}":                      "deviceState=INUSE",
		"PUT /mailboxes/{mailboxName}":                        "oldMessages=1&newMessages=2",
		"POST /applications/{applicationName}/subscription":   "eventSource=deviceState:Custom:lamp",
		"DELETE /applications/{applicationName}/subscription": "eventSource=deviceState:Custom:lamp",
		"PUT /endpoints/sendMessage":                          "to=pjsip:100&from=pjsip:ivr&body=hi",
		"PUT /endpoints/{tech}/{resource}/sendMessage":        "from=pjsip:ivr&body=hi",
		"POST /events/user/{eventName}":                       "application=ivr&source=channel:callmore0001-1",
		"POST /events/claim":                                  "channelId=callmore0001-1&application=ivr",
		"POST /endpoints/refer":                               "to=pjsip:100&from=pjsip:ivr&refer_to=sip:101@x",
		"POST /endpoints/{tech}/{resource}/refer":             "from=pjsip:ivr&refer_to=sip:101@x",
	}
	bodies := map[string]any{
		"POST /channels/{channelId}/variables":            map[string]any{"variables": map[string]string{"A": "1"}},
		"POST /bridges/{bridgeId}/variables":              map[string]any{"variables": map[string]string{"A": "1"}},
		"PUT /applications/{applicationName}/eventFilter": map[string]any{"disallowed": []map[string]string{{"type": "ChannelVarset"}}},
		"POST /events/user/{eventName}":                   map[string]any{"variables": map[string]string{"k": "v"}},
	}
	// Some need a different object than the shared one, or must run last (they end what they act on).
	special := map[string]map[string]string{
		"POST /channels/{channelId}/dial":               {"{channelId}": "made0001-1"},
		"DELETE /channels/{channelId}":                  {"{channelId}": "callmore0002-1"},
		"POST /channels/{channelId}/move":               {"{channelId}": "callmore0002-1"},
		"DELETE /recordings/live/{recordingName}":       {"{recordingName}": "rec2"},
		"GET /recordings/live/{recordingName}":          {"{recordingName}": "rec2"},
		"POST /recordings/live/{recordingName}/stop":    {"{recordingName}": "rec2"},
		"POST /recordings/live/{recordingName}/pause":   {"{recordingName}": "rec2"},
		"DELETE /recordings/live/{recordingName}/pause": {"{recordingName}": "rec2"},
		"POST /recordings/live/{recordingName}/mute":    {"{recordingName}": "rec2"},
		"DELETE /recordings/live/{recordingName}/mute":  {"{recordingName}": "rec2"},
		"GET /recordings/stored/{recordingName}/file":   {"{recordingName}": "stored1"},
		"DELETE /recordings/stored/{recordingName}":     {"{recordingName}": "stored2"},
		"DELETE /bridges/{bridgeId}":                    {"{bridgeId}": "room2"},
		"DELETE /playbacks/{playbackId}":                {"{playbackId}": "pb1"},
		"POST /channels/{channelId}/continue":           {"{channelId}": "callmore0003-1"},
		"POST /channels/{channelId}/redirect":           {"{channelId}": "callmore0004-1"},
	}
	last := map[string]int{"DELETE /channels/{channelId}": 3, "DELETE /bridges/{bridgeId}": 2, "DELETE /playbacks/{playbackId}": 2,
		"POST /channels/{channelId}/move": 1, "DELETE /recordings/live/{recordingName}": 2, "POST /recordings/live/{recordingName}/stop": 1,
		"DELETE /recordings/stored/{recordingName}": 1, "POST /channels/{channelId}/continue": 1, "POST /channels/{channelId}/redirect": 1}
	// External media needs the real engine (external_test.go tries it); the fake has none.
	needsEngine := map[string]bool{"POST /channels/externalMedia": true}
	order := func(o specOp) int { return last[o.method+" "+o.path] }
	// The live recording's operations need it going: start it before them, and the channel record op
	// makes it. Run the spec in order, with the endings last.
	sort.SliceStable(ops, func(i, j int) bool { return order(ops[i]) < order(ops[j]) })
	why := map[string]string{}
	for _, o := range ops {
		key := o.method + " " + o.path
		if strings.HasPrefix(o.path, "/recordings/live/") && order(o) == 0 {
			// make sure the live recording exists for its operations
			do("POST", "/channels/callmore0001-1/record?name=rec2&maxDurationSeconds=30&ifExists=overwrite", nil)
		}
		path := o.path
		for k, v := range ids {
			if s, ok := special[key][k]; ok {
				v = s
			}
			path = strings.ReplaceAll(path, k, v)
		}
		if q := query[key]; q != "" {
			path += "?" + q
		}
		if o.path == "/events" {
			continue // the WebSocket, used above
		}
		code, b := do(o.method, path, bodies[key])
		if needsEngine[key] {
			continue
		}
		if code == http.StatusNotImplemented {
			var e map[string]string
			json.Unmarshal(b, &e)
			if notOfferedFor(o.method, strings.TrimPrefix(strings.Split(path, "?")[0], "/")) == "" {
				t.Errorf("%s answered 501 but is not listed as not offered: %s", key, e["message"])
			}
			why[key] = e["message"]
			continue
		}
		if code >= 500 || code == 404 && !strings.Contains(o.path, "{") {
			t.Errorf("%s: %d %s", key, code, b)
			continue
		}
		if code == 404 || code == 405 {
			t.Errorf("%s (%s) did not reach its object: %d %s", key, path, code, b)
			continue
		}
		// What it answered has the model's required fields.
		model := strings.TrimSuffix(strings.TrimPrefix(o.model, "List["), "]")
		req := models[model]
		if code >= 300 || len(req) == 0 || o.model == "binary" {
			continue
		}
		var items []map[string]any
		if strings.HasPrefix(o.model, "List[") {
			if err := json.Unmarshal(b, &items); err != nil {
				t.Errorf("%s: not a list: %s", key, b)
				continue
			}
		} else {
			var one map[string]any
			if err := json.Unmarshal(b, &one); err != nil {
				t.Errorf("%s: not an object: %s", key, b)
				continue
			}
			items = []map[string]any{one}
		}
		for _, it := range items {
			for _, field := range req {
				if _, ok := it[field]; !ok {
					t.Errorf("%s: %s lacks %q: %s", key, model, field, b)
				}
			}
		}
	}
	if len(why) != len(notOffered) {
		t.Errorf("%d operations answered 501, %d are listed as not offered: %v", len(why), len(notOffered), why)
	}
}

// The rest of the ARI behaves: mute and hold reach the channel, a playback pauses and resumes,
// device states and user events reach a subscribed application, a filter keeps an event out, a
// snoop records the caller, and a created channel is dialled.
func TestARIMoreBehaviour(t *testing.T) {
	f := &morePBXFake{fakePBX: newFake()}
	f.fakePBX.cfg.System.Compat.Apps = []config.ARIApp{{Name: "ivr", Fallback: config.Destination{Type: "hangup"}}, {Name: "spy", Fallback: config.Destination{Type: "hangup"}}}
	dir := t.TempDir()
	a := &ARI{PBX: f, Bus: events.New(), Started: time.Now(), Recordings: filepath.Join(dir, "rec"), Grace: time.Second}
	srv := httptest.NewServer(a)
	defer srv.Close()
	app := dialApp(t, srv, "ivr")
	spy := dialApp(t, srv, "spy")
	for _, n := range []string{"ivr", "spy"} {
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			a.st().mu.Lock()
			k := len(a.st().conns[n])
			a.st().mu.Unlock()
			if k > 0 {
				break
			}
			if time.Now().After(end) {
				t.Fatal("not connected")
			}
		}
	}
	do := func(method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+"/ari"+path, rd)
		req.SetBasicAuth("crm", "a long crm secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	ch := &moreFakeChannel{fakeChannel: newFakeChannel("callmore0009")}
	a.enter("ivr", ch, nil)
	app.next("StasisStart")
	id := "callmore0009-1"

	do("POST", "/channels/"+id+"/mute?direction=in", nil)
	if in, out := ch.Muted(); !in || out {
		t.Fatalf("mute in: %v %v", in, out)
	}
	do("DELETE", "/channels/"+id+"/mute", nil)
	if in, out := ch.Muted(); in || out {
		t.Fatal("not unmuted")
	}
	do("POST", "/channels/"+id+"/hold", nil)
	if ev := app.next("ChannelHold"); ev["channel"].(map[string]any)["id"] != id || !ch.music {
		t.Fatalf("hold %v", ev)
	}
	do("DELETE", "/channels/"+id+"/hold", nil)
	app.next("ChannelUnhold")

	// A playback pauses, resumes, and is stopped.
	do("POST", "/channels/"+id+"/play?media=sound:long&playbackId=p9", nil)
	app.next("PlaybackStarted")
	if code, _ := do("POST", "/playbacks/p9/control?operation=pause", nil); code != 204 {
		t.Fatal(code)
	}
	waitState := func(want string) {
		for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, pb := do("GET", "/playbacks/p9", nil); pb["state"] == want {
				return
			}
			if time.Now().After(end) {
				t.Fatalf("playback never %s", want)
			}
		}
	}
	waitState("paused")
	do("POST", "/playbacks/p9/control?operation=unpause", nil)
	waitState("playing")
	do("DELETE", "/playbacks/p9", nil)
	if ev := app.next("PlaybackFinished"); ev["playback"].(map[string]any)["id"] != "p9" {
		t.Fatal(ev)
	}

	// Device states: a subscribed application hears a custom device change; a filter keeps
	// ChannelVarset out.
	do("POST", "/applications/ivr/subscription?eventSource=deviceState:Custom:lamp", nil)
	if code, _ := do("PUT", "/deviceStates/Custom:lamp?deviceState=BUSY", nil); code != 204 {
		t.Fatal(code)
	}
	if ev := app.next("DeviceStateChanged"); ev["device_state"].(map[string]any)["state"] != "BUSY" {
		t.Fatal(ev)
	}
	if code, _ := do("PUT", "/deviceStates/PJSIP/100?deviceState=BUSY", nil); code != 409 {
		t.Fatalf("a phone's state set by a program: %d", code)
	}
	_, appInfo := do("PUT", "/applications/ivr/eventFilter", map[string]any{"disallowed": []map[string]string{{"type": "ChannelVarset"}}})
	if len(appInfo["events_disallowed"].([]any)) != 1 || len(appInfo["device_names"].([]any)) != 1 {
		t.Fatalf("application %v", appInfo)
	}
	do("POST", "/channels/"+id+"/variables", map[string]any{"variables": map[string]string{"A": "1"}})
	do("POST", "/events/user/ping?application=ivr&source=channel:"+id, map[string]any{"variables": map[string]string{"k": "v"}})
	ev := app.next("ChannelUserevent") // ChannelVarset, before it, was filtered out
	if ev["eventname"] != "ping" || ev["userevent"].(map[string]any)["k"] != "v" || ev["channel"] == nil {
		t.Fatal(ev)
	}

	// A snoop channel in another application records what the caller says.
	if code, sn := do("POST", "/channels/"+id+"/snoop?spy=in&app=spy&snoopId=s1", nil); code != 200 || sn["id"] != "s1" {
		t.Fatalf("snoop %d %v", code, sn)
	}
	spy.next("StasisStart")
	if code, _ := do("POST", "/channels/s1/record?name=spied&maxDurationSeconds=30", nil); code != 201 {
		t.Fatal(code)
	}
	spy.next("RecordingStarted")
	time.Sleep(150 * time.Millisecond)
	do("POST", "/recordings/live/spied/stop", nil)
	if ev := spy.next("RecordingFinished"); ev["recording"].(map[string]any)["state"] != "done" {
		t.Fatal(ev)
	}
	if fi, err := os.Stat(filepath.Join(dir, "rec", "spied.wav")); err != nil || fi.Size() <= 44 {
		t.Fatalf("the snoop's recording %v %v", fi, err)
	}
	if code, _ := do("POST", "/channels/"+id+"/snoop?whisper=both&app=spy", nil); code != 400 {
		t.Fatalf("whisper: %d", code)
	}

	// A created channel waits for dial, then rings.
	if code, c := do("POST", "/channels/create?endpoint=PJSIP/101&app=ivr&channelId=new1", nil); code != 200 || c["state"] != "Down" {
		t.Fatalf("create %d %v", code, c)
	}
	if code, _ := do("POST", "/channels/new1-1/dial?timeout=10", nil); code != 204 {
		t.Fatal(code)
	}
	if ev := app.next("Dial"); ev["dialstatus"] != "RINGING" {
		t.Fatal(ev)
	}
	if len(f.dialled) != 1 || f.dialled[0] != "101" {
		t.Fatalf("dialled %v", f.dialled)
	}

	// Mailboxes: an extension's own counts, and one a program keeps.
	if _, mb := do("GET", "/mailboxes/100@default", nil); mb["new_messages"] != float64(2) {
		t.Fatal(mb)
	}
	if code, _ := do("PUT", "/mailboxes/100@default?oldMessages=0&newMessages=0", nil); code != 409 {
		t.Fatalf("an extension's mailbox set by a program: %d", code)
	}
	// Moving the channel to an application nobody runs fails, and says so.
	if code, _ := do("POST", "/channels/"+id+"/move?app=nobody", nil); code != 409 {
		t.Fatal(code)
	}
	if ev := app.next("ApplicationMoveFailed"); ev["destination"] != "nobody" {
		t.Fatal(ev)
	}
	do("POST", "/channels/"+id+"/move?app=spy", nil)
	app.next("StasisEnd")
	if ev := spy.next("StasisStart"); ev["channel"].(map[string]any)["id"] != id {
		t.Fatal(ev)
	}
	if _, x := do("GET", "/applications/spy", nil); len(x["channel_ids"].([]any)) != 1 {
		t.Fatalf("after the move %v", x)
	}
}

// ari-client, the ARI library for JavaScript, reads BitVoice's /ari/api-docs and runs an application:
// BITVOICE_TEST_ARICLIENT is the folder of ~/.bvtest/ari/client (run.js and ari-client installed).
func TestARIWithAriClient(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_ARICLIENT")
	if dir == "" {
		t.Skip("set BITVOICE_TEST_ARICLIENT to the ari-client test folder")
	}
	f := &morePBXFake{fakePBX: newFake()}
	f.fakePBX.cfg.System.Compat.Apps = []config.ARIApp{{Name: "ivr", Fallback: config.Destination{Type: "hangup"}}}
	a := &ARI{PBX: f, Bus: events.New(), Started: time.Now(), Recordings: filepath.Join(t.TempDir(), "rec"), Grace: time.Second}
	srv := httptest.NewServer(a)
	defer srv.Close()
	cmd := exec.Command("node", "run.js", srv.URL)
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	wait := func(want string) {
		t.Helper()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("the client stopped; it said %q", got)
				}
				got = append(got, l)
				if strings.HasPrefix(l, "ERROR") {
					t.Fatalf("the client failed: %q", got)
				}
				if strings.HasPrefix(l, want) {
					return
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("waiting for %q; the client said %q", want, got)
			}
		}
	}
	wait("started")
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		a.st().mu.Lock()
		n := len(a.st().conns["ivr"])
		a.st().mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("ari-client never opened its events")
		}
	}
	ch := &moreFakeChannel{fakeChannel: newFakeChannel("clientcall01")}
	if !a.enter("ivr", ch, nil) {
		t.Fatal("not entered")
	}
	wait("DONE")
	want := []string{"StasisStart clientcall01-1", "playing", "PlaybackFinished", "bridge clientroom", "deviceState BUSY", "version 20.0.0-bitvoice", "apps ivr", "var blue", "DONE"}
	joined := strings.Join(got, "|")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("the client never said %q: %q", w, got)
		}
	}
	if !ch.Ended() {
		t.Fatal("the client did not hang up the call")
	}
}
