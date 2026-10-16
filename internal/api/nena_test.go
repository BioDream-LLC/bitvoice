package api

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nenaCheck validates a request or response body against NENA's published OpenAPI definition of
// an i3 web service (the NENA911 repositories), by a validator kept outside the repo:
// BITVOICE_TEST_NENA names a directory holding oapi.js (ajv), with the NENA repositories in
// ../nena beside it. status is the response code, or "request". Skipped when it is not set.
func nenaCheck(t *testing.T, spec, path, method, status string, body []byte, contentType ...string) {
	t.Helper()
	dir := os.Getenv("BITVOICE_TEST_NENA")
	if dir == "" {
		return
	}
	args := []string{filepath.Join(dir, "oapi.js"), filepath.Join(dir, "..", "nena", spec), path, method, status}
	args = append(args, contentType...)
	cmd := exec.Command("node", args...)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("%s %s %s %s against NENA's definition: %v\n%s\nbody: %s", spec, method, path, status, err, out, body)
	}
}
