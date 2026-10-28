package main

import (
	"os"
	"sync"
)

// logLimit is how large the log file grows before it moves to FILE.1 (replacing the one before) and a
// new one starts: at most twice this on disk.
const logLimit = 10 << 20

// logFile is the -log file: everything the program prints, for a computer where nobody sees its
// window (a Windows scheduled task has none).
type logFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openLog(path string) (*logFile, error) {
	l := &logFile{path: path}
	return l, l.open()
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	if l.size+int64(len(p)) > logLimit && l.size > 0 {
		// On Windows a file cannot be renamed while open: close it first.
		l.f.Close()
		_ = os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			l.f = nil
			return len(p), nil
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
