package compat

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

type fakePBX struct {
	mu        sync.Mutex
	cfg       config.Config
	calls     []pbx.CallInfo
	hung      []string
	originate [][2]string
	redirect  [][3]string
}

func (f *fakePBX) Config() *config.Config { f.mu.Lock(); defer f.mu.Unlock(); c := f.cfg; return &c }
func (f *fakePBX) Calls() []pbx.CallInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pbx.CallInfo(nil), f.calls...)
}
func (f *fakePBX) Hangup(id, by string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hung = append(f.hung, id)
	return true
}
func (f *fakePBX) Originate(ext, num string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ext == "999" {
		return "", errors.New("no extension 999")
	}
	f.originate = append(f.originate, [2]string{ext, num})
	return "newcall", nil
}
func (f *fakePBX) Redirect(id, leaving, num string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirect = append(f.redirect, [3]string{id, leaving, num})
	return nil
}
func (f *fakePBX) Registrations() []pbx.Binding { return []pbx.Binding{{Extension: "100"}} }
func (f *fakePBX) Queues() []pbx.QueueStatus {
	return []pbx.QueueStatus{{Number: "700", Name: "Support", Agents: []pbx.AgentStatus{{Extension: "100", Name: "Reception", State: "available"}}}}
}

func newFake() *fakePBX {
	cfg := config.Default()
	cfg.Extensions = []config.Extension{{Number: "100", Name: "Reception"}, {Number: "101", Name: "Dr Lee"}}
	cfg.System.Compat = config.Compat{AMI: true, AMIListen: "127.0.0.1:0", ARI: true, Users: []config.CompatUser{
		{Username: "crm", Secret: "a long crm secret", Write: true}, {Username: "board", Secret: "a long board secret"}}}
	return &fakePBX{cfg: cfg, calls: []pbx.CallInfo{{ID: "abcdef123456", State: "answered", Direction: "internal",
		From: "100", FromName: "Reception", To: "101", ToName: "Dr Lee", Start: time.Now(), Legs: make([]pbx.LegInfo, 2)}}}
}

type amiClient struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func dialAMI(t *testing.T, a *AMI) *amiClient {
	c, err := net.Dial("tcp", a.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	r := bufio.NewReader(c)
	if banner, _ := r.ReadString('\n'); !strings.HasPrefix(banner, "Asterisk Call Manager/") {
		t.Fatalf("banner %q", banner)
	}
	return &amiClient{t, c, r}
}

func (c *amiClient) do(kv ...string) amiMsg {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(kv[i] + ": " + kv[i+1] + "\r\n")
	}
	b.WriteString("\r\n")
	_, _ = c.c.Write([]byte(b.String()))
	return c.next()
}

// next returns the next message that is not an unrelated event.
func (c *amiClient) next() amiMsg {
	_ = c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		m, err := readMsg(c.r)
		if err != nil {
			c.t.Fatal(err)
		}
		if m.get("Event") == "FullyBooted" {
			continue
		}
		return m
	}
}

func startAMI(t *testing.T, f *fakePBX) (*AMI, *events.Bus) {
	bus := events.New()
	a := &AMI{PBX: f, Bus: bus, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.Apply(f.cfg.System.Compat)
	t.Cleanup(a.Close)
	if a.Addr() == "" {
		t.Fatal("not listening")
	}
	return a, bus
}

func TestAMI(t *testing.T) {
	f := newFake()
	a, bus := startAMI(t, f)
	c := dialAMI(t, a)
	if m := c.do("Action", "Ping"); m.get("Response") != "Error" {
		t.Fatalf("ping before login: %v", m)
	}
	if m := c.do("Action", "Login", "Username", "crm", "Secret", "wrong"); m.get("Response") != "Error" {
		t.Fatalf("bad login accepted: %v", m)
	}
	if m := c.do("Action", "Login", "Username", "crm", "Secret", "a long crm secret", "ActionID", "1"); m.get("Response") != "Success" || m.get("ActionID") != "1" {
		t.Fatalf("login: %v", m)
	}
	if m := c.do("Action", "Ping"); m.get("Ping") != "Pong" {
		t.Fatalf("ping: %v", m)
	}
	c.do("Action", "CoreShowChannels", "ActionID", "7")
	var names []string
	for {
		m := c.next()
		if m.get("Event") == "CoreShowChannelsComplete" {
			if m.get("ListItems") != "2" {
				t.Fatalf("complete %v", m)
			}
			break
		}
		names = append(names, m.get("Channel")+"/"+m.get("ChannelStateDesc"))
	}
	if strings.Join(names, ",") != "PJSIP/100-abcdef121/Up,PJSIP/101-abcdef122/Up" {
		t.Fatalf("channels %v", names)
	}
	if m := c.do("Action", "Originate", "Channel", "PJSIP/100", "Exten", "5551234", "Context", "from-internal", "Priority", "1"); m.get("Response") != "Success" {
		t.Fatalf("originate %v", m)
	}
	if m := c.do("Action", "Redirect", "Channel", "PJSIP/101-abcdef122", "Exten", "102"); m.get("Response") != "Success" {
		t.Fatalf("redirect %v", m)
	}
	if m := c.do("Action", "Hangup", "Channel", "PJSIP/100-abcdef121"); m.get("Response") != "Success" {
		t.Fatalf("hangup %v", m)
	}
	if m := c.do("Action", "Hangup", "Channel", "PJSIP/nobody"); m.get("Response") != "Error" {
		t.Fatalf("hangup of nothing %v", m)
	}
	f.mu.Lock()
	if len(f.originate) != 1 || f.originate[0] != [2]string{"100", "5551234"} || len(f.redirect) != 1 ||
		f.redirect[0] != [3]string{"abcdef123456", "caller", "102"} || len(f.hung) != 1 {
		t.Fatalf("actions %v %v %v", f.originate, f.redirect, f.hung)
	}
	f.mu.Unlock()

	// Events: a new call becomes Newchannel, its answer Newstate, its end Hangup.
	call := pbx.CallInfo{ID: "zzzz99990000", State: "ringing", From: "101", To: "100", Start: time.Now()}
	bus.Publish("call.started", call)
	if m := c.next(); m.get("Event") != "Newchannel" || m.get("Channel") != "PJSIP/101-zzzz99991" {
		t.Fatalf("newchannel %v", m)
	}
	call.State, call.Legs = "answered", make([]pbx.LegInfo, 2)
	bus.Publish("call.updated", call)
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		m := c.next()
		got[m.get("Event")+" "+m.get("Channel")] = true
	}
	if !got["Newstate PJSIP/101-zzzz99991"] || !got["Newchannel PJSIP/100-zzzz99992"] {
		t.Fatalf("answer events %v", got)
	}
	bus.Publish("call.ended", call)
	for i := 0; i < 2; i++ {
		if m := c.next(); m.get("Event") != "Hangup" {
			t.Fatalf("hangup event %v", m)
		}
	}
	if m := c.do("Action", "Logoff"); m.get("Response") != "Goodbye" {
		t.Fatalf("logoff %v", m)
	}
}

func TestAMIReadOnlyAndMD5(t *testing.T) {
	f := newFake()
	a, _ := startAMI(t, f)
	c := dialAMI(t, a)
	ch := c.do("Action", "Challenge", "AuthType", "MD5").get("Challenge")
	sum := md5.Sum([]byte(ch + "a long board secret"))
	if m := c.do("Action", "Login", "AuthType", "MD5", "Username", "board", "Key", hex.EncodeToString(sum[:])); m.get("Response") != "Success" {
		t.Fatalf("md5 login %v", m)
	}
	if m := c.do("Action", "Originate", "Channel", "PJSIP/100", "Exten", "101"); m.get("Response") != "Error" || !strings.Contains(m.get("Message"), "read only") {
		t.Fatalf("read-only originate %v", m)
	}
	// 100 is on the fake's call; 101 has no phone registered.
	if m := c.do("Action", "ExtensionState", "Exten", "100", "Context", "x"); m.get("StatusText") != "InUse" {
		t.Fatalf("state %v", m)
	}
	if m := c.do("Action", "ExtensionState", "Exten", "101", "Context", "x"); m.get("StatusText") != "Unavailable" {
		t.Fatalf("state %v", m)
	}
}

func TestAMISwitchedOff(t *testing.T) {
	f := newFake()
	a, _ := startAMI(t, f)
	f.mu.Lock()
	f.cfg.System.Compat.AMI = false
	f.mu.Unlock()
	a.Apply(f.cfg.System.Compat)
	if a.Addr() != "" {
		t.Fatal("still listening")
	}
}

func TestARI(t *testing.T) {
	f := newFake()
	bus := events.New()
	srv := httptest.NewServer(&ARI{PBX: f, Bus: bus, Started: time.Now()})
	defer srv.Close()
	get := func(path, user, secret string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if user != "" {
			req.SetBasicAuth(user, secret)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, _ := get("/ari/channels", "", ""); code != 401 {
		t.Fatalf("no login: %d", code)
	}
	code, body := get("/ari/channels", "board", "a long board secret")
	var chans []ariChannel
	_ = json.Unmarshal([]byte(body), &chans)
	if code != 200 || len(chans) != 2 || chans[0].State != "Up" || chans[1].Caller.Number != "101" {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := get("/ari/endpoints?api_key=board:a+long+board+secret", "", ""); code != 200 || !strings.Contains(body, `"state":"online"`) {
		t.Fatalf("endpoints %d %s", code, body)
	}
	post := func(path, user, secret string) int {
		req, _ := http.NewRequest("POST", srv.URL+path, nil)
		req.SetBasicAuth(user, secret)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := post("/ari/channels?endpoint=PJSIP/100&extension=5551234", "board", "a long board secret"); code != 403 {
		t.Fatalf("read-only originate %d", code)
	}
	if code := post("/ari/channels?endpoint=PJSIP/100&extension=5551234", "crm", "a long crm secret"); code != 200 {
		t.Fatalf("originate %d", code)
	}
	if code := post("/ari/channels?endpoint=PJSIP/999&extension=1", "crm", "a long crm secret"); code != 400 {
		t.Fatalf("bad originate %d", code)
	}
	if code := post("/ari/channels/abcdef123456-2/redirect?endpoint=PJSIP/102", "crm", "a long crm secret"); code != 204 {
		t.Fatalf("redirect %d", code)
	}

	// The event WebSocket.
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	_, _ = conn.Write([]byte("GET /ari/events?app=wallboard&api_key=board:a%20long%20board%20secret HTTP/1.1\r\nHost: x\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"))
	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(r, nil)
	sum := sha1.Sum([]byte(key + wsGUID))
	if err != nil || resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Fatalf("upgrade %v %v", resp, err)
	}
	time.Sleep(50 * time.Millisecond)
	bus.Publish("call.started", pbx.CallInfo{ID: "newcall00001", State: "ringing", From: "100", To: "101", Start: time.Now()})
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil || hdr[0] != 0x81 {
		t.Fatalf("frame %v %v", hdr, err)
	}
	n := int(hdr[1] & 0x7f)
	if n == 126 {
		var l [2]byte
		_, _ = io.ReadFull(r, l[:])
		n = int(l[0])<<8 | int(l[1])
	}
	msg := make([]byte, n)
	_, _ = io.ReadFull(r, msg)
	var ev map[string]any
	_ = json.Unmarshal(msg, &ev)
	if ev["type"] != "ChannelCreated" || ev["application"] != "wallboard" {
		t.Fatalf("event %s", msg)
	}
}

// A port that is busy at start is taken as soon as it is free.
func TestAMIRetriesBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.cfg.System.Compat.AMIListen = busy.Addr().String()
	a := &AMI{PBX: f, Bus: events.New(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.Apply(f.cfg.System.Compat)
	defer a.Close()
	if a.Addr() != "" {
		t.Fatal("listening on a busy port?")
	}
	busy.Close()
	deadline := time.Now().Add(8 * time.Second)
	for a.Addr() == "" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if a.Addr() == "" {
		t.Fatal("never took the port once it was free")
	}
}
