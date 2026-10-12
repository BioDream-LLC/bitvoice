// Command bvsign makes and uses BioDream's release signing key (ed25519, from the standard library).
//
//	bvsign -new release.key        writes a new private key and prints the public key for RELEASE_KEY
//	bvsign -key release.key FILE   writes FILE.sig, which BitVoice checks before installing FILE
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
		fmt.Println("signed", f)
	}
	return nil
}
