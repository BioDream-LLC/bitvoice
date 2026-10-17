package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/totp"
)

func TestVoicemailAPI(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	cfg, _ := s.Store.Config()
	if x := cfg.FindExtension("100"); !x.Voicemail.Enabled || x.NoAnswer == nil || x.NoAnswer.Type != "voicemail" {
		t.Fatalf("setup did not switch on voicemail: %+v", x)
	}

	put := func(body []byte) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/voicemail/100/greeting", bytes.NewReader(body))
		req.Header.Set("X-BitVoice", "1")
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put([]byte("not audio")); code != 422 {
		t.Fatalf("garbage greeting: %d", code)
	}
	if code := put(media.EncodeWAV(make([]int16, media.Rate*2))); code != 200 {
		t.Fatalf("greeting: %d", code)
	}
	resp, _ := c.http.Get(ts.URL + "/api/voicemail/100/greeting")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("get greeting %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp.Body.Close()

	v, _ := s.Store.AddVoicemail(store.Voicemail{Box: "100", From: "5551234", At: time.Now()}, media.EncodeWAV(make([]int16, 800)))
	code, out := c.do("GET", "/api/voicemail", nil)
	if code != 200 || len(out["items"].([]any)) != 1 || len(out["greetings"].([]any)) != 1 {
		t.Fatalf("list %d %v", code, out)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/voicemail/100/"+v.ID+"/audio", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, _ = c.http.Do(req)
	if resp.StatusCode != 206 {
		t.Fatalf("range %d", resp.StatusCode)
	}
	resp.Body.Close()
	if code, out := c.do("PATCH", "/api/voicemail/100/"+v.ID, map[string]any{"heard": true}); code != 200 || out["heard"] != true {
		t.Fatalf("patch %d %v", code, out)
	}
	if code, _ := c.do("DELETE", "/api/voicemail/100/"+v.ID, nil); code != 204 {
		t.Fatalf("delete %d", code)
	}
	if code, _ := c.do("GET", "/api/voicemail/..%2f..%2fetc/x/audio", nil); code != 404 {
		t.Fatalf("path escape %d", code)
	}
}

func TestQueueAPI(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	code, out := c.do("POST", "/api/queues", map[string]any{"number": "700", "name": "Support", "agents": []string{"100", "101"}})
	if code != 201 && code != 200 {
		t.Fatalf("create %d %v", code, out)
	}
	if code, out := c.do("POST", "/api/queues", map[string]any{"number": "700", "name": "Dup", "agents": []string{"100"}}); code != 422 {
		t.Fatalf("duplicate number %d %v", code, out)
	}
	code, out = c.do("GET", "/api/queues/status", nil)
	items, _ := out["items"].([]any)
	if code != 200 || len(items) != 1 {
		t.Fatalf("status %d %v", code, out)
	}
	q := items[0].(map[string]any)
	if agents := q["agents"].([]any); len(agents) != 2 || agents[0].(map[string]any)["state"] != "offline" {
		t.Fatalf("agents %v", q)
	}
}

func TestProvisioning(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	if _, _, err := s.Store.Update("test", "phones", 0, func(cfg *config.Config) error {
		cfg.System.Provisioning = config.Provisioning{Enabled: true, Username: "phones", Password: "a-long-provisioning-pw"}
		cfg.Devices = []config.Device{{MAC: "80:5E:C0:11:22:33", Vendor: "yealink", Extension: "100"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	get := func(user, pass, file string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+"/provision/"+file, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := get("", "", "805ec0112233.cfg"); code != 401 {
		t.Fatalf("no auth: %d", code)
	}
	if code, _ := get("phones", "wrong", "805ec0112233.cfg"); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	code, body := get("phones", "a-long-provisioning-pw", "805ec0112233.cfg")
	if code != 200 || !strings.Contains(body, "account.1.user_name = 100") {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := get("phones", "a-long-provisioning-pw", "000000000000.cfg"); code != 404 {
		t.Fatalf("unknown phone: %d", code)
	}
}

func TestImport(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	csv := "extension,name,secret\n100,Taken,abcdefghijklmnop\n150,New Person,abcdefghijklmnop\n"
	req, _ := http.NewRequest("POST", ts.URL+"/api/import/preview?name=ext.csv", strings.NewReader(csv))
	req.Header.Set("X-BitVoice", "1")
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("preview %v %d", err, resp.StatusCode)
	}
	var pv struct {
		Result struct {
			Extensions []config.Extension `json:"extensions"`
		} `json:"result"`
		Exists map[string]bool `json:"exists"`
	}
	json.NewDecoder(resp.Body).Decode(&pv)
	resp.Body.Close()
	if len(pv.Result.Extensions) != 2 || !pv.Exists["100"] || pv.Exists["150"] {
		t.Fatalf("%+v", pv)
	}
	code, out := c.do("POST", "/api/import/apply", map[string]any{"extensions": pv.Result.Extensions})
	if code != 200 || out["added"] != float64(1) || out["skipped"] != float64(1) {
		t.Fatalf("%d %v", code, out)
	}
	cfg, _ := s.Store.Config()
	if x := cfg.FindExtension("150"); x == nil || x.Secret != "abcdefghijklmnop" {
		t.Fatalf("%+v", x)
	}
}

func TestVoicemailTranscript(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		answer := "Sam needs a call back."
		if strings.Contains(string(body), "input_audio") {
			answer = "Hi it's Sam, please call me back."
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer model.Close()
	s, _ := newServer(t)
	s.Log = slog.Default()
	if _, _, err := s.Store.Update("test", "ai", 0, func(c *config.Config) error {
		c.Extensions = []config.Extension{{Number: "100", Name: "A", Enabled: true}}
		c.System.AI = config.AI{Enabled: true, BaseURL: "http://127.0.0.1" + model.URL[len("http://127.0.0.1"):], Model: "m", Voicemail: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed(t, s, func() { go s.RunAI(ctx) })
	v, _ := s.Store.AddVoicemail(store.Voicemail{Box: "100", At: time.Now()}, media.EncodeWAV(make([]int16, 8000)))
	s.Bus.Publish("voicemail.new", v)
	for range 100 {
		list, _ := s.Store.Voicemails("100")
		if len(list) == 1 && list[0].Summary != "" {
			if !strings.Contains(list[0].Transcript, "Sam") {
				t.Fatalf("%+v", list[0])
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("no transcript")
}

func TestArchiveToS3(t *testing.T) {
	var mu sync.Mutex
	got := map[string]int{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path]++
		mu.Unlock()
	}))
	defer fake.Close()
	s, _ := newServer(t)
	if _, _, err := s.Store.Update("test", "archive", 0, func(c *config.Config) error {
		c.System.Archive = config.Archive{Target: "s3", Recordings: true, Voicemail: true,
			S3: config.ArchiveS3{Region: "us-east-1", Bucket: "acme-pbx", Prefix: "site", AccessKey: "AK", SecretKey: "SK", Endpoint: fake.URL}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	name, _ := s.Store.AddRecording("call1", time.Now(), []byte("RIFF"))
	s.archiveOnce(context.Background())
	s.archiveOnce(context.Background()) // a second run sends nothing already sent
	mu.Lock()
	defer mu.Unlock()
	if got["/acme-pbx/site/recordings/"+name+".wav"] != 1 {
		t.Fatalf("%v", got)
	}
	configs := 0
	for p, n := range got {
		if strings.HasPrefix(p, "/acme-pbx/site/config/") {
			configs += n
		}
	}
	if configs != 1 {
		t.Fatalf("config backups: %v", got)
	}
	if st := s.archiveState().status; st.Error != "" || st.Uploaded != 2 {
		t.Fatalf("%+v", st)
	}
}

// fakeSMTP accepts one message without TLS or sign-in and hands back what it received.
func fakeSMTP(t *testing.T) (int, <-chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 fake\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(c, "250 ok\r\n")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
			case "EHLO", "HELO":
				fmt.Fprint(c, "250 fake\r\n")
			case "DATA":
				inData = true
				fmt.Fprint(c, "354 go\r\n")
			case "QUIT":
				fmt.Fprint(c, "221 bye\r\n")
				return
			default:
				fmt.Fprint(c, "250 ok\r\n")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestVoicemailEmail(t *testing.T) {
	port, got := fakeSMTP(t)
	s, _ := newServer(t)
	s.Log = slog.Default()
	if _, _, err := s.Store.Update("test", "email", 0, func(c *config.Config) error {
		c.Extensions = []config.Extension{{Number: "100", Name: "Front desk", Email: "desk@example.com", Enabled: true,
			Voicemail: config.Voicemail{Enabled: true, Email: true}}}
		c.System.Email = config.Email{Host: "127.0.0.1", Port: port, From: "BitVoice <pbx@example.com>"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed(t, s, func() { go s.RunEmail(ctx) })
	v, _ := s.Store.AddVoicemail(store.Voicemail{Box: "100", From: "2125550100", At: time.Now(), Seconds: 3}, media.EncodeWAV(make([]int16, 800)))
	s.Bus.Publish("voicemail.new", v)
	select {
	case m := <-got:
		if !strings.Contains(m, "To: desk@example.com") || !strings.Contains(m, "audio/wav") || !strings.Contains(m, "2125550100") {
			t.Fatalf("%s", m)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no email")
	}
}

func TestTwoStepSignIn(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal(code)
	}
	code, out := c.do("POST", "/api/auth/mfa/start", nil)
	if code != 200 || !strings.HasPrefix(out["uri"].(string), "otpauth://totp/") {
		t.Fatalf("%d %v", code, out)
	}
	secret := out["secret"].(string)
	if code, _ := c.do("POST", "/api/auth/mfa/confirm", map[string]string{"code": "000000"}); code != 422 {
		t.Fatalf("wrong code accepted: %d", code)
	}
	now, _ := totp.Code(secret, time.Now())
	if code, out := c.do("POST", "/api/auth/mfa/confirm", map[string]string{"code": now}); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	fresh := newClient(t, ts.URL)
	creds := map[string]string{"username": "owner", "password": "a long enough password"}
	if code, out := fresh.do("POST", "/api/auth/login", creds); code != 401 || out["mfa"] != true {
		t.Fatalf("no code asked: %d %v", code, out)
	}
	creds["code"] = now // the code used to confirm cannot be used again
	if code, _ := fresh.do("POST", "/api/auth/login", creds); code != 401 {
		t.Fatalf("replayed code accepted: %d", code)
	}
	next, _ := totp.Code(secret, time.Now().Add(30*time.Second))
	creds["code"] = next
	if code, out := fresh.do("POST", "/api/auth/login", creds); code != 200 || out["mfa"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := fresh.do("POST", "/api/auth/mfa/off", map[string]string{"password": "wrong"}); code != 422 {
		t.Fatal("turned off without the password")
	}
	if code, _ := fresh.do("POST", "/api/auth/mfa/off", map[string]string{"password": "a long enough password"}); code != 200 {
		t.Fatal("could not turn off")
	}
}
