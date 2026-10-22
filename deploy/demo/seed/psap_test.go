package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The CAD file may be KEY=VALUE (shell style) or key: value (as a CAD hands it over); a URL's own
// colon stays in the value.
func TestReadCAD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cad")
	_ = os.WriteFile(p, []byte("# comment\neido_url: https://cad.example/api/ng911/eido\neido_token: tok\nRADIO_URL=https://cad.example/r\nRADIO_SECRET=\"s\"\n"), 0o600)
	cad, err := readCAD(p)
	if err != nil {
		t.Fatal(err)
	}
	if cad["EIDO_URL"] != "https://cad.example/api/ng911/eido" || cad["EIDO_TOKEN"] != "tok" || cad["RADIO_URL"] != "https://cad.example/r" || cad["RADIO_SECRET"] != "s" {
		t.Fatalf("%v", cad)
	}
	if c, err := readCAD(filepath.Join(t.TempDir(), "none")); err != nil || len(c) != 0 {
		t.Fatal("a missing file is no CAD")
	}
}
