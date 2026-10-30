package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The stdio bridge passes each line to /mcp with the token, writes the answer as one line, writes
// nothing for a notification, and trusts BitVoice's own certificate only when pinned.
func TestMCPBridge(t *testing.T) {
	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		if strings.Contains(string(b), `"notifications/`) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\n\"result\":{}}\n"))
	}))
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	pin := hex.EncodeToString(sum[:])
	token := "bv_" + strings.Repeat("a", 64)
	t.Setenv("BITVOICE_TOKEN", token)
	in := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n")
	var out, errs bytes.Buffer
	if rc := mcpBridge([]string{"-url", srv.URL, "-pin", pin}, in, &out, &errs); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errs.String())
	}
	if out.String() != "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n" {
		t.Fatalf("output %q", out.String())
	}
	if len(seen) != 2 || !strings.HasPrefix(seen[0], "/mcp Bearer "+token) {
		t.Fatalf("server saw %q", seen)
	}
	// Without the pin, a self-signed certificate is refused and its pin printed.
	out.Reset()
	errs.Reset()
	mcpBridge([]string{"-url", srv.URL}, strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"ping\"}\n"), &out, &errs)
	if !strings.Contains(errs.String(), "-pin "+pin) || !strings.Contains(out.String(), `"id":7`) || !strings.Contains(out.String(), "cannot be reached") {
		t.Fatalf("unpinned: out %q err %q", out.String(), errs.String())
	}
	// A wrong pin is refused.
	errs.Reset()
	mcpBridge([]string{"-url", srv.URL, "-pin", strings.Repeat("0", 64)}, strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"ping\"}\n"), &out, &errs)
	if !strings.Contains(errs.String(), "not the pinned one") {
		t.Fatalf("wrong pin: %q", errs.String())
	}
}
