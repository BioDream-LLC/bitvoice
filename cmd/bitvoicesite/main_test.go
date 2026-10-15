package main

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The site builds from the checkout, every page and asset it links to exists, every feature row of
// the README reaches the features page, and nothing personal or unfilled slips into it.
func TestBuild(t *testing.T) {
	out := t.TempDir()
	if err := build("../..", out, "https://bitvoice.org"); err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile("../../README.md")
	feat, _ := section(string(readme), "## Everything it does", "## Public safety")
	want := strings.Count(feat, "<tr><td")
	page, err := os.ReadFile(filepath.Join(out, "features/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(page), `<li>`) - strings.Count(string(page), `<li><a`); got < want {
		t.Errorf("features page has %d rows, README %d", got, want)
	}
	link := regexp.MustCompile(`(?:href|src)="(/[^"#?]*)`)
	// Nothing of the machine that built it: its user name, home folder or host name, nor a placeholder.
	words := []string{`/Users/`, `/home/`, `\{\{`}
	if u, err := user.Current(); err == nil && len(u.Username) > 2 {
		words = append(words, regexp.QuoteMeta(u.Username))
	}
	if h, err := os.Hostname(); err == nil && len(h) > 2 {
		words = append(words, regexp.QuoteMeta(strings.Split(h, ".")[0]))
	}
	leak := regexp.MustCompile(`(?i)` + strings.Join(words, "|"))
	err = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".html") || strings.Contains(filepath.ToSlash(path), "/manual/") {
			return err
		}
		b, _ := os.ReadFile(path)
		if m := leak.Find(b); m != nil {
			t.Errorf("%s holds %q", path, m)
		}
		for _, m := range link.FindAllStringSubmatch(string(b), -1) {
			p := filepath.Join(out, filepath.FromSlash(m[1]))
			if strings.HasSuffix(m[1], "/") {
				p = filepath.Join(p, "index.html")
			}
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s links to %s, which is not in the site", strings.TrimPrefix(path, out), m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
