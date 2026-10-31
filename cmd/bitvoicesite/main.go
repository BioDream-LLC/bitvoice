// Command bitvoicesite builds the bitvoice.org website into a directory of static files.
//
// The site is made from what this repository already publishes, so it cannot drift from it: the
// feature tables come out of README.md, the version from its release badge, the manual is the one
// docs/manual holds, and the screenshots are docs/assets/screens. The pages themselves are HTML
// fragments in site/pages, wrapped in one layout (menu, footer, head) here.
//
//	go run ./cmd/bitvoicesite -out dist-site -base https://bitvoice.org
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// page is one page of the site: a fragment in site/pages, wrapped in the layout.
type page struct {
	src, path, section, title, description, priority string
}

var pages = []page{
	{"home.html", "/", "", "BitVoice: open-source alternative to FreePBX and Asterisk",
		"A complete Apache-2.0 phone system for offices and contact centres: one file, run entirely from its web interface. No Asterisk underneath, no paid modules.", "1.0"},
	{"technology.html", "/technology/", "technology", "The technology inside BitVoice",
		"The technologies a phone system rests on, from SIP and codecs to fax, ISDN, SS7, WebRTC and NG911: how BitVoice does each, and how it is tested.", "0.9"},
	{"features.html", "/features/", "features", "Everything BitVoice does",
		"Every BitVoice feature: extensions, queues, menus, voicemail, recording, conferences, trunks, desk phones, WebRTC, AI receptionists, a dialer and an inbox.", "0.9"},
	{"mobile.html", "/mobile/", "mobile", "BitVoice for Android and iPhone: your extension in your pocket",
		"The free BitVoice app: calls ring on the phone's call screen even when it is closed, with voicemail transcripts, live transcripts, chat and video rooms.", "0.9"},
	{"freepbx.html", "/freepbx-alternative/", "freepbx", "A FreePBX and Asterisk alternative you run from the browser",
		"How BitVoice compares with FreePBX and Asterisk, what moves over with the import, and what BitVoice does not do yet.", "0.9"},
	{"proof.html", "/proof/", "proof", "How BitVoice is tested, and what has not been tested",
		"The tests every change must pass, the real software BitVoice is checked against, and the equipment it has not been tried with yet.", "0.8"},
	{"lab.html", "/public-safety/", "lab", "Public safety: the lab and design-partner track",
		"NG911 call handling, a P25 radio console and NG911 Core Services on the same engine. Not certified or run on live systems yet; design partners wanted.", "0.6"},
	{"download.html", "/download/", "download", "Download BitVoice",
		"Download BitVoice for Linux, macOS and Windows, on Intel and Arm. One file, nothing to install, signed releases, free under Apache 2.0.", "0.9"},
	{"privacy.html", "/privacy/", "privacy", "Privacy: the BitVoice apps",
		"What the BitVoice phone apps for Android and iPhone collect, where it goes, and how to remove it. No advertising, analytics or tracking.", "0.3"},
}

func main() {
	out := flag.String("out", "dist-site", "directory to write the site into (emptied first)")
	base := flag.String("base", "https://bitvoice.org", "the site's canonical origin, without a trailing slash")
	root := flag.String("root", ".", "the repository checkout")
	manuals := flag.String("manuals", "", "a directory of translated manuals, LANG.html each (make manuals); none when empty")
	flag.Parse()
	manualDir = *manuals
	if err := build(*root, *out, strings.TrimSuffix(*base, "/")); err != nil {
		fmt.Fprintln(os.Stderr, "bitvoicesite:", err)
		os.Exit(1)
	}
}

// manualDir holds the translated manuals; manualNames names their languages.
var manualDir string

var manualNames = map[string]string{"de": "Deutsch", "fr": "Français", "es": "Español", "it": "Italiano", "pt": "Português", "nl": "Nederlands",
	"pl": "Polski", "sv": "Svenska", "da": "Dansk", "nb": "Norsk bokmål", "fi": "Suomi", "ja": "日本語", "zh": "简体中文"}

var chapterRE = regexp.MustCompile(`<section class="chapter" id="([a-z0-9-]+)"><div class="chapno">[^<]*</div><h2>([^<]+)</h2>`)

// build writes the whole site.
func build(root, out, base string) error {
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return err
	}
	version := releaseVersion(string(readme))
	if version == "" {
		return errors.New("README.md has no release badge to read the version from")
	}
	features, err := section(string(readme), "## Everything it does", "## Public safety")
	if err != nil {
		return err
	}
	lab, err := section(string(readme), "## Public safety", "## What it looks like")
	if err != nil {
		return err
	}
	maturity, err := section(string(readme), "### Feature maturity", "### Capacity")
	if err != nil {
		return err
	}
	capacity, err := section(string(readme), "### Capacity", "## Documentation")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	css, err := os.ReadFile(filepath.Join(root, "site/static/site.css"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(css)
	cssVersion = hex.EncodeToString(sum[:])[:10]
	if err := os.WriteFile(filepath.Join(out, "site.css"), css, 0o644); err != nil {
		return err
	}
	if err := copyAssets(root, out); err != nil {
		return err
	}
	arts, err := loadArticles(root)
	if err != nil {
		return err
	}
	man, err := os.ReadFile(filepath.Join(root, "docs/manual/index.html"))
	if err != nil {
		return err
	}
	chapters := map[string]string{}
	for _, m := range chapterRE.FindAllStringSubmatch(string(man), -1) {
		chapters[m[1]] = html.UnescapeString(m[2])
	}
	for _, a := range arts {
		if _, ok := chapters[a.manual]; !ok {
			return fmt.Errorf("article %s names manual chapter %q, which the manual does not have", a.slug, a.manual)
		}
	}
	manualLangs = nil
	if manualDir != "" {
		for _, code := range sortedKeys(manualNames) {
			f := filepath.Join(manualDir, code+".html")
			if _, err := os.Stat(f); err != nil {
				continue
			}
			if err := copyFile(f, filepath.Join(out, "manual", code, "index.html")); err != nil {
				return err
			}
			manualLangs = append(manualLangs, [2]string{code, manualNames[code]})
		}
	}
	words := 0
	for _, a := range arts {
		words += a.words
	}

	fill := strings.NewReplacer(
		"{{VERSION}}", version,
		"{{RELEASED}}", released(root, version),
		"{{NEWER_FEATURES}}", newer(root, version, `<p class="hero-note">This page follows the main branch: some of it is newer than the `+
			version+` download (released `+released(root, version)+`) and comes in the next release.</p>`),
		"{{NEWER_DOWNLOAD}}", newer(root, version, `<p class="lab-note"><b>Newer than `+version+`.</b> This site and its manual describe the main branch, `+
			`which has changes since `+version+` was released (`+released(root, version)+`). They come in the next release; until then, build from source (below) to have them.</p>`),
		"{{FEATURES}}", tables(features),
		"{{LAB}}", tables(lab),
		"{{MATURITY}}", "<h2>How mature each part is</h2>\n"+rendered(maturity),
		"{{CAPACITY}}", rendered(capacity),
		"{{TILES}}", tiles(arts, false),
		"{{TECH}}", tiles(arts, true),
		"{{ARTICLES}}", fmt.Sprint(len(arts)),
		"{{ARTICLE_WORDS}}", thousands(words),
	)
	var sitemap strings.Builder
	sitemap.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	for _, p := range pages {
		raw, err := os.ReadFile(filepath.Join(root, "site/pages", p.src))
		if err != nil {
			return err
		}
		body := iconRE.ReplaceAllStringFunc(fill.Replace(string(raw)), func(s string) string { return icon(strings.TrimPrefix(s, "ICON:")) })
		if strings.Contains(body, "{{") {
			return fmt.Errorf("%s: a placeholder was not filled", p.src)
		}
		if err := write(out, p.path, layout(base, p.path, p.section, p.title, p.description, body, version)); err != nil {
			return err
		}
		fmt.Fprintf(&sitemap, "<url><loc>%s%s</loc><priority>%s</priority></url>\n", base, p.path, p.priority)
	}

	for _, a := range arts {
		path := "/technology/" + a.slug + "/"
		if err := write(out, path, layout(base, path, "technology", a.title+" · BitVoice", a.description, articlePage(a, arts, chapters, version), version)); err != nil {
			return err
		}
		fmt.Fprintf(&sitemap, "<url><loc>%s%s</loc><priority>0.7</priority></url>\n", base, path)
	}
	for _, l := range manualLangs {
		fmt.Fprintf(&sitemap, "<url><loc>%s/manual/%s/</loc><priority>0.5</priority></url>\n", base, l[0])
	}

	// The manual, as it is in docs/manual: one HTML file with its own styles, and the PDF.
	for _, f := range []string{"index.html", "bitvoice-manual.pdf"} {
		if err := copyFile(filepath.Join(root, "docs/manual", f), filepath.Join(out, "manual", f)); err != nil {
			return err
		}
	}
	fmt.Fprintf(&sitemap, "<url><loc>%s/manual/</loc><priority>0.8</priority></url>\n</urlset>\n", base)
	if err := os.WriteFile(filepath.Join(out, "sitemap.xml"), []byte(sitemap.String()), 0o644); err != nil {
		return err
	}
	robots := "User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"
	if err := os.WriteFile(filepath.Join(out, "robots.txt"), []byte(robots), 0o644); err != nil {
		return err
	}
	notFound := `<section class="hero small"><div class="wrap"><p class="eyebrow">404</p><h1>That page is not here</h1>
<p class="lede">It may have moved when the site was rebuilt.</p>
<div class="actions"><a class="btn btn-primary" href="/">Home</a><a class="btn btn-ghost" href="/manual/">The manual</a></div></div></section>`
	return os.WriteFile(filepath.Join(out, "404.html"), []byte(layout(base, "/404.html", "", "Page not found · BitVoice", "This page does not exist.", notFound, version)), 0o644)
}

// write puts a page at its path: "/features/" is features/index.html.
func write(out, path, content string) error {
	f := filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(path, "/")), "index.html")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte(content), 0o644)
}

var badgeRE = regexp.MustCompile(`badge/release-(v\d+\.\d+\.\d+)-`)

// releaseVersion is the version on the README's release badge, the one its download links name.
func releaseVersion(readme string) string {
	if m := badgeRE.FindStringSubmatch(readme); m != nil {
		return m[1]
	}
	return ""
}

// released is the day the version was tagged, from git; "recently" when git cannot say.
func released(root, version string) string {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%cs", version).Output()
	if err != nil || len(strings.TrimSpace(string(out))) != 10 {
		return "recently"
	}
	return strings.TrimSpace(string(out))
}

// newer is note when the checkout has code changes since the release (anything outside the
// README, docs and the site itself), and nothing when it is the release.
func newer(root, version, note string) string {
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", version, "HEAD", "--", ".",
		":(exclude)README.md", ":(exclude)docs", ":(exclude)site", ":(exclude)cmd/bitvoicesite").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return ""
	}
	return note
}

// section is the README between two headings.
func section(readme, from, to string) (string, error) {
	i := strings.Index(readme, from)
	if i < 0 {
		return "", fmt.Errorf("README.md has no %q", from)
	}
	rest := readme[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		return "", fmt.Errorf("README.md has no %q after %q", to, from)
	}
	return rest[:j], nil
}

var (
	tableRE = regexp.MustCompile(`(?s)<table[^>]*>\s*<thead><tr><th[^>]*><img src="docs/assets/chips/([a-z]+)\.svg"[^>]*alt="([^"]+)"[^>]*></th><th[^>]*>(.*?)</th></tr></thead>\s*<tbody>(.*?)</tbody></table>`)
	rowRE   = regexp.MustCompile(`(?s)<tr><td[^>]*>(.*?)</td></tr>`)
	hrefRE  = regexp.MustCompile(`href="#([a-z0-9-]+)"`)
)

// tables turns the README's feature tables into the site's cards: one per area, its chip, its
// heading and every row as a line.
func tables(md string) string {
	var b strings.Builder
	for _, m := range tableRE.FindAllStringSubmatch(md, -1) {
		chip, title, sub, rows := m[1], m[2], m[3], m[4]
		fmt.Fprintf(&b, "<section class=\"area\" id=\"%s\">\n<div class=\"area-head\"><img src=\"/assets/chips/%s.svg\" width=\"168\" height=\"40\" alt=\"%s icon\"><div><h2>%s</h2><p>%s</p></div></div>\n<ul class=\"ticks\">\n",
			chip, chip, html.EscapeString(title), html.EscapeString(title), sub)
		for _, r := range rowRE.FindAllStringSubmatch(rows, -1) {
			// Links inside the README point at its own headings; on the site those are other pages.
			line := hrefRE.ReplaceAllStringFunc(r[1], func(h string) string {
				switch hrefRE.FindStringSubmatch(h)[1] {
				case "how-it-is-tested":
					return `href="/proof/"`
				case "public-safety-lab-and-design-partner-track":
					return `href="/public-safety/"`
				}
				return `href="/manual/"`
			})
			fmt.Fprintf(&b, "<li>%s</li>\n", line)
		}
		b.WriteString("</ul>\n</section>\n")
	}
	return b.String()
}

// copyAssets copies the logo, artwork and chips, and the screenshots: as WebP at the size they are
// shown when cwebp is installed (a fifth of the bytes), else the PNGs as they are.
func copyAssets(root, out string) error {
	for _, glob := range []string{"docs/assets/*.svg", "docs/assets/*.png", "docs/assets/chips/*.svg", "docs/assets/*.mp4", "docs/assets/mobile/*.png"} {
		files, _ := filepath.Glob(filepath.Join(root, glob))
		for _, f := range files {
			rel, _ := filepath.Rel(filepath.Join(root, "docs"), f)
			if err := copyFile(f, filepath.Join(out, rel)); err != nil {
				return err
			}
		}
	}
	shots, _ := filepath.Glob(filepath.Join(root, "docs/assets/screens/*.png"))
	if len(shots) == 0 {
		return errors.New("no screenshots in docs/assets/screens")
	}
	cwebp, _ := exec.LookPath("cwebp")
	for _, f := range shots {
		name := strings.TrimSuffix(filepath.Base(f), ".png")
		dst := filepath.Join(out, "assets/screens", name+".webp")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if cwebp != "" {
			if b, err := exec.Command(cwebp, "-quiet", "-q", "82", "-resize", "1600", "0", f, "-o", dst).CombinedOutput(); err != nil {
				return fmt.Errorf("cwebp %s: %v %s", name, err, b)
			}
			continue
		}
		// No cwebp: the PNG under the WebP name would be served as the wrong type, so keep it a PNG
		// and point the pages at it.
		if err := copyFile(f, filepath.Join(out, "assets/screens", name+".png")); err != nil {
			return err
		}
		pngFallback = true
	}
	return nil
}

// pngFallback is set when the screenshots were copied as PNGs; the layout then rewrites the pages' .webp references.
var pngFallback bool

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	o, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(o, in); err != nil {
		o.Close()
		return err
	}
	return o.Close()
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// rendered is a piece of the README as HTML, its links to its own headings sent to the site's pages.
func rendered(md string) string {
	h, _ := markdown(md)
	return hrefRE.ReplaceAllStringFunc(h, func(s string) string {
		switch hrefRE.FindStringSubmatch(s)[1] {
		case "capacity":
			return `href="/proof/#capacity"`
		case "how-it-is-tested":
			return `href="/proof/"`
		}
		return `href="/manual/"`
	})
}
