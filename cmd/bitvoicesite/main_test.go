package main

import (
	"html"
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

// Every page keeps to what search engines check (Bing Webmaster Tools flags these): a title of at most
// 70 characters, a description of 25 to 160, and alt text on every image.
func TestSearchEngineLimits(t *testing.T) {
	out := t.TempDir()
	if err := build("../..", out, "https://bitvoice.org"); err != nil {
		t.Fatal(err)
	}
	title := regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	desc := regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	img := regexp.MustCompile(`<img\b[^>]*>`)
	alt := regexp.MustCompile(`alt="[^"]+"`)
	filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, _ := os.ReadFile(path)
		page := string(b)
		rel, _ := filepath.Rel(out, path)
		if m := title.FindStringSubmatch(page); m == nil || len([]rune(html.UnescapeString(m[1]))) > 70 {
			t.Errorf("%s: title missing or over 70 characters: %v", rel, m)
		}
		if m := desc.FindStringSubmatch(page); m == nil {
			t.Errorf("%s: no description", rel)
		} else if n := len([]rune(html.UnescapeString(m[1]))); n < 25 || n > 160 {
			t.Errorf("%s: description is %d characters (25 to 160)", rel, n)
		}
		for _, i := range img.FindAllString(page, -1) {
			if !alt.MatchString(i) {
				t.Errorf("%s: image without alt text: %s", rel, i)
			}
		}
		return nil
	})
}
