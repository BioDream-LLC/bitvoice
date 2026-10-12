package upgrade

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeProgram is a script that answers -version like BitVoice.
func fakeProgram(ver string) []byte {
	return []byte("#!/bin/sh\necho 'bitvoice " + ver + "'\n")
}

func TestStageSwitchAndRollBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the program")
	}
	data := t.TempDir()
	self := filepath.Join(t.TempDir(), "bitvoice")
	_ = os.WriteFile(self, fakeProgram("1.0.0"), 0o755)
	ctx := context.Background()

	if _, err := Stage(ctx, data, []byte("not a program"), nil, ""); err == nil {
		t.Fatal("a non-program was accepted")
	}
	s, err := Stage(ctx, data, fakeProgram("1.1.0"), nil, "")
	if err != nil || s.StagedVersion != "1.1.0" || s.StagedSigned {
		t.Fatalf("stage: %+v %v", s, err)
	}
	if a := Decide(data, self, false); a.Exec != "" {
		t.Fatal("a staged version must not start before the switch")
	}
	if s, err = Switch(data, self, "1.0.0"); err != nil || s.CurrentVersion != "1.1.0" || s.PreviousVersion != "1.0.0" || !s.Pending {
		t.Fatalf("switch: %+v %v", s, err)
	}
	// The installed program (the launcher) starts the new one, which serves and counts the attempt.
	a := Decide(data, self, false)
	if a.Exec != s.Current {
		t.Fatalf("launcher: %+v", a)
	}
	if a := Decide(data, a.Exec, true); a.Exec != "" || Load(data).Attempts != 1 {
		t.Fatalf("first start: %+v %+v", a, Load(data))
	}
	// It crashes; the service manager starts the launcher again, twice.
	Decide(data, self, false)
	Decide(data, s.Current, true)
	Decide(data, self, false)
	back := Decide(data, s.Current, true)
	if back.Exec != self || back.Reason != "rolled back" {
		t.Fatalf("no automatic way back: %+v %+v", back, Load(data))
	}
	if st := Load(data); st.CurrentVersion != "1.0.0" || st.Pending {
		t.Fatalf("after rollback %+v", st)
	}

	// Again, and this time it stays up: confirmed, then rolled back by hand.
	if _, err := Stage(ctx, data, fakeProgram("1.2.0"), nil, ""); err != nil {
		t.Fatal(err)
	}
	s, _ = Switch(data, self, "1.0.0")
	Decide(data, s.Current, true)
	Confirm(data, "1.2.0")
	if st := Load(data); st.Pending || st.Attempts != 0 {
		t.Fatalf("confirm %+v", st)
	}
	s, err = Rollback(data)
	if err != nil || s.CurrentVersion != "1.0.0" || s.PreviousVersion != "1.2.0" {
		t.Fatalf("rollback %+v %v", s, err)
	}
	if a := Decide(data, self, false); a.Exec != "" {
		t.Fatalf("after rolling back to the installed program: %+v", a)
	}
}

func TestSignedReleases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the program")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	prog := fakeProgram("2.0.0")
	data := t.TempDir()
	ctx := context.Background()
	if _, err := Stage(ctx, data, prog, nil, key); err == nil {
		t.Fatal("unsigned program accepted by a signed build")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Stage(ctx, data, prog, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, prog))), key); err == nil {
		t.Fatal("program signed by someone else accepted")
	}
	s, err := Stage(ctx, data, prog, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, prog))+"\n"), key)
	if err != nil || !s.StagedSigned {
		t.Fatalf("signed program refused: %v", err)
	}
}
