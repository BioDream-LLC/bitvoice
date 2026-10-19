//go:build linux

package dahdi

import (
	"os"
	"testing"
	"time"
)

// TestDevFileCloseWhileReading: a read waiting for data that never comes (a quiet D-channel) ends
// when the file is closed, and Close returns only once it has, so the device is free at once.
func TestDevFileCloseWhileReading(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	d := newDevFile(r)
	done := make(chan error, 1)
	go func() {
		_, err := d.read(make([]byte, 16))
		done <- err
	}()
	time.Sleep(3 * pollEvery) // the read is waiting
	start := time.Now()
	if err := d.close(nil); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*pollEvery+50*time.Millisecond {
		t.Errorf("Close took %v", took)
	}
	// Close has waited for the read's system call; the read itself returns straight after.
	select {
	case err := <-done:
		if err != errClosed {
			t.Fatalf("read ended with %v, want errClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the read did not end after Close")
	}
	if _, err := d.write([]byte{1}); err != errClosed {
		t.Fatalf("write after Close: %v", err)
	}
}

// TestDevFileReads: data waiting is read, through poll.
func TestDevFileReads(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	d := newDevFile(r)
	defer d.close(nil)
	go func() { time.Sleep(150 * time.Millisecond); _, _ = w.Write([]byte("frame")) }()
	b := make([]byte, 16)
	n, err := d.read(b)
	if err != nil || string(b[:n]) != "frame" {
		t.Fatalf("read %q, %v", b[:n], err)
	}
}
