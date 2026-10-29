package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// errAlreadyRunning: another BitVoice holds this data folder. Two on one folder would write one
// database, and a second start (the Start menu while the scheduled task runs it) means "show me".
var errAlreadyRunning = errors.New("another BitVoice is running with this data folder")

// instance is this program's hold on its data folder, kept until it stops. The file holds the web
// interface's address, for a second start to open.
type instance struct{ f *os.File }

func claimData(dir string) (*instance, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := lockFile(filepath.Join(dir, "bitvoice.lock"))
	if err != nil {
		return nil, err
	}
	return &instance{f: f}, nil
}

// SetURL records where this BitVoice's web interface is.
func (i *instance) SetURL(url string) {
	_ = i.f.Truncate(0)
	_, _ = i.f.WriteAt([]byte(url+"\n"), 0)
}

func (i *instance) Close() { _ = i.f.Close() }

// runningURL is the web address the BitVoice holding dir wrote, or "".
func runningURL(dir string) string {
	b, err := readShared(filepath.Join(dir, "bitvoice.lock"))
	if err != nil {
		return ""
	}
	u := strings.TrimSpace(string(b))
	if !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}
