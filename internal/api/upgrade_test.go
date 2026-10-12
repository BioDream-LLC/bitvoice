package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/upgrade"
)

func TestUpgradeFromTheInterface(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the program")
	}
	s, ts := newServer(t)
	s.DataDir = t.TempDir()
	restarted := make(chan struct{}, 1)
	s.Restart = func() { restarted <- struct{}{} }
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	upload := func(program string) (int, map[string]any) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("program", "bitvoice")
		fw.Write([]byte(program))
		mw.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/api/system/upgrade", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-BitVoice", "1")
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := upload("garbage"); code != 422 {
		t.Fatalf("garbage: %d %v", code, out)
	}
	if code, out := upload("#!/bin/sh\necho 'bitvoice 9.0.0'\n"); code != 200 || out["state"].(map[string]any)["stagedVersion"] != "9.0.0" {
		t.Fatalf("upload: %d %v", code, out)
	}
	if code, _ := c.do("POST", "/api/system/upgrade/apply", map[string]string{"when": "now"}); code != 200 {
		t.Fatal("apply", code)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("never restarted")
	}
	if st := upgrade.Load(s.DataDir); st.CurrentVersion != "9.0.0" || st.PreviousVersion != "test" || !st.Pending {
		t.Fatalf("state %+v", st)
	}
	if code, _ := c.do("POST", "/api/system/upgrade/rollback", map[string]string{"when": "idle"}); code != 200 {
		t.Fatal("rollback", code)
	}
	<-restarted
	if st := upgrade.Load(s.DataDir); st.CurrentVersion != "test" {
		t.Fatalf("after rollback %+v", st)
	}
}
