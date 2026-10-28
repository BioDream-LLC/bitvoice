package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The log file appends across starts, and past its limit the older part moves to FILE.1.
func TestLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bitvoice.log")
	l, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("first start\n"))
	l.Close()
	if l, err = openLog(path); err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("second start\n"))
	if b, _ := os.ReadFile(path); string(b) != "first start\nsecond start\n" {
		t.Fatalf("log %q", b)
	}
	line := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 11; i++ {
		if _, err := l.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	l.Write([]byte("latest\n"))
	l.Close()
	old, _ := os.ReadFile(path + ".1")
	cur, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(old), "first start\n") || len(old) > logLimit || !strings.HasSuffix(string(cur), "latest\n") || len(cur) > logLimit {
		t.Fatalf("old %d bytes, current %d bytes", len(old), len(cur))
	}
}

// -log takes the banner and the setup code, which a Windows scheduled task has no window to show.
func TestLogFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bitvoice.log")
	var out, errs bytes.Buffer
	if code := run([]string{"-log", path, "-version"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	// -version prints before the log opens; a bad path is refused plainly.
	if code := run([]string{"-log", filepath.Join(dir, "no", "such", "dir", "x.log"), "-data", dir, "-no-browser"}, &out, &errs); code != 1 ||
		!strings.Contains(errs.String(), "cannot write the log") {
		t.Fatalf("bad path: exit %d, %q", code, errs.String())
	}
}
