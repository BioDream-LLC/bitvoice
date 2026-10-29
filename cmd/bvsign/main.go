// Command bvsign makes and uses BioDream's release signing key (ed25519, from the standard library).
//
//	bvsign -new release.key        writes a new private key and prints the public key for RELEASE_KEY
//	bvsign -key release.key FILE   writes FILE.sig, which BitVoice checks before installing FILE, and
//	                               FILE.sshsig, the same key's signature in OpenSSH's format, which
//	                               the Windows installer checks with Windows' own ssh-keygen
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	newKey := flag.String("new", "", "write a new private key to this file")
	keyFile := flag.String("key", "", "private key file to sign with")
	flag.Parse()
	if err := run(*newKey, *keyFile, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "bvsign:", err)
		os.Exit(1)
	}
}

func run(newKey, keyFile string, files []string) error {
	if newKey != "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.WriteFile(newKey, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return nil
	}
	if keyFile == "" || len(files) == 0 {
		return fmt.Errorf("use -new KEYFILE, or -key KEYFILE FILE...")
	}
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%s is not a signing key", keyFile)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(priv), data))
		if err := os.WriteFile(f+".sig", []byte(sig+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(f+".sshsig", sshSignature(ed25519.PrivateKey(priv), data), 0o644); err != nil {
			return err
		}
		fmt.Println("signed", f)
	}
	return nil
}

// sshNamespace is what the signatures are for, in OpenSSH's words: ssh-keygen -Y verify -n file.
const sshNamespace = "file"

// sshSignature is an OpenSSH signature of data (PROTOCOL.sshsig: "ssh-keygen -Y sign -n file"), so a
// computer with OpenSSH 8.1 or later, as Windows 10 and 11 have, can check a download without
// OpenSSL 3. The signed blob holds data's SHA-512, not data.
func sshSignature(priv ed25519.PrivateKey, data []byte) []byte {
	str := func(b []byte) []byte { return binary.BigEndian.AppendUint32(nil, uint32(len(b))) }
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, str(p)...)
			out = append(out, p...)
		}
		return out
	}
	h := sha512.Sum512(data)
	signed := append([]byte("SSHSIG"), cat([]byte(sshNamespace), nil, []byte("sha512"), h[:])...)
	sig := cat([]byte("ssh-ed25519"), ed25519.Sign(priv, signed))
	pub := cat([]byte("ssh-ed25519"), priv.Public().(ed25519.PublicKey))
	blob := append([]byte("SSHSIG"), binary.BigEndian.AppendUint32(nil, 1)...)
	blob = append(blob, cat(pub, []byte(sshNamespace), nil, []byte("sha512"), sig)...)
	b64 := base64.StdEncoding.EncodeToString(blob)
	out := "-----BEGIN SSH SIGNATURE-----\n"
	for len(b64) > 70 {
		out, b64 = out+b64[:70]+"\n", b64[70:]
	}
	return []byte(out + b64 + "\n-----END SSH SIGNATURE-----\n")
}
