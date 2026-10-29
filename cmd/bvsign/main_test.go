package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// OpenSSH's own ssh-keygen accepts the signature for the signed bytes, and refuses it for others: what
// the Windows installer relies on.
func TestSSHSignatureVerifiesWithSSHKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen here")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	data := []byte("BitVoice\x00program bytes\n")
	f := filepath.Join(dir, "bitvoice.exe")
	os.WriteFile(f, data, 0o644)
	os.WriteFile(f+".sshsig", sshSignature(priv, data), 0o644)
	// The key as OpenSSH writes it: "ssh-ed25519 " + base64(string "ssh-ed25519", string key).
	blob := binary.BigEndian.AppendUint32(nil, 11)
	blob = append(blob, "ssh-ed25519"...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	blob = append(blob, pub...)
	signers := filepath.Join(dir, "signers")
	os.WriteFile(signers, []byte("biodream ssh-ed25519 "+base64.StdEncoding.EncodeToString(blob)+"\n"), 0o644)
	verify := func(path string) (string, error) {
		in, _ := os.Open(path)
		defer in.Close()
		cmd := exec.Command(keygen, "-Y", "verify", "-f", signers, "-I", "biodream", "-n", "file", "-s", f+".sshsig")
		cmd.Stdin = in
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := verify(f); err != nil || !strings.Contains(out, "Good") {
		t.Fatalf("ssh-keygen refused it: %v %s", err, out)
	}
	other := filepath.Join(dir, "other")
	os.WriteFile(other, append(data, 'x'), 0o644)
	if out, err := verify(other); err == nil {
		t.Fatalf("ssh-keygen accepted other bytes: %s", out)
	}
}
