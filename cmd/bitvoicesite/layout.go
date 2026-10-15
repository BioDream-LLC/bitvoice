package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// The shared layout: head, menu across the top, footer. Every page is wrapped here, so all of them
// have the same menu and nothing drifts.

// cssVersion is a hash of the stylesheet, put in its URL: a changed stylesheet is a new URL, so
// nobody sees new pages drawn with yesterday's styles.
var cssVersion = "dev"

// icons are 24-unit stroke icons drawn for this site, coloured by currentColor.
var icons = map[string]string{
	"phone":    `<path d="M5 4h4l2 5-2.5 1.5a11 11 0 0 0 5 5L15 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2"/>`,
	"queue":    `<rect x="3" y="4" width="18" height="4" rx="1"/><rect x="3" y="10" width="18" height="4" rx="1"/><rect x="3" y="16" width="18" height="4" rx="1"/>`,
	"browser":  `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01"/>`,
	"swap":     `<path d="M4 8h13l-3-3M20 16H7l3 3"/>`,
	"shield":   `<path d="M12 3l7 3v5c0 5-3.2 8.4-7 10-3.8-1.6-7-5-7-10V6l7-3z"/><path d="M9 12l2 2 4-4"/>`,
	"check":    `<circle cx="12" cy="12" r="9"/><path d="M8 12.5l2.8 2.8L16.5 9"/>`,
	"book":     `<path d="M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2V5z"/><path d="M8 7h7"/>`,
	"download": `<path d="M12 3v12M7 10l5 5 5-5M4 20h16"/>`,
	"github":   `<path d="M9 19c-4 1.3-4-2-6-2.5M15 21v-3.5c0-1 .1-1.4-.5-2 2.8-.3 5.5-1.4 5.5-6a4.6 4.6 0 0 0-1.3-3.2 4.3 4.3 0 0 0-.1-3.2s-1-.3-3.4 1.3a11.7 11.7 0 0 0-6.2 0C6.6 2.8 5.6 3.1 5.6 3.1a4.3 4.3 0 0 0-.1 3.2A4.6 4.6 0 0 0 4.2 9.5c0 4.6 2.7 5.7 5.5 6-.6.6-.6 1.2-.5 2V21"/>`,
	"chart":    `<path d="M4 20V4M4 20h16"/><path d="M8 16l3-4 3 2 5-7"/>`,
	"code":     `<path d="M9 8l-5 4 5 4M15 8l5 4-5 4"/>`,
	"server":   `<rect x="4" y="3" width="16" height="7" rx="1.5"/><rect x="4" y="14" width="16" height="7" rx="1.5"/><path d="M8 6.5h.01M8 17.5h.01"/>`,
	"spark":    `<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6"/>`,
	"chat":     `<path d="M4 5h16v11H9l-5 4z"/><path d="M8 10h8M8 13h5"/>`,
	"users":    `<circle cx="9" cy="8" r="3.2"/><path d="M3 20c.8-3.4 3.2-5 6-5s5.2 1.6 6 5"/><circle cx="17" cy="9" r="2.5"/><path d="M16 14.5c2.3.2 4.2 1.7 5 4.5"/>`,
	"lab":      `<path d="M7 3h10M8 3v4l-3 9a4 4 0 0 0 3.8 5h6.4a4 4 0 0 0 3.8-5l-3-9V3"/>`,
	"layers":   `<path d="M12 3l9 5-9 5-9-5 9-5z"/><path d="M3 13l9 5 9-5"/>`,
	"menu":     `<path d="M4 7h16M4 12h16M4 17h16"/>`,
}

func icon(name string) string {
	return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
		icons[name] + `</svg>`
}

var iconRE = regexp.MustCompile(`ICON:[a-z]+`)

type navItem struct{ key, title, href string }

// menu is the top menu; key matches a page's section, so the menu marks where the reader is.
var menu = []navItem{
	{"features", "Features", "/features/"},
	{"freepbx", "Leaving FreePBX", "/freepbx-alternative/"},
	{"proof", "Proof", "/proof/"},
	{"manual", "Manual", "/manual/"},
	{"lab", "Public safety (lab)", "/public-safety/"},
}

// layout wraps a page body in the head, menu and footer.
func layout(base, path, section, title, description, body, version string) string {
	if pngFallback {
		body = strings.ReplaceAll(body, ".webp", ".png")
	}
	canonical := base + path
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n<meta name=\"description\" content=\"%s\">\n", html.EscapeString(title), html.EscapeString(description))
	fmt.Fprintf(&b, "<link rel=\"canonical\" href=\"%s\">\n", html.EscapeString(canonical))
	b.WriteString("<link rel=\"icon\" type=\"image/svg+xml\" href=\"/assets/bitvoice-logo.svg\">\n")
	fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"/site.css?v=%s\">\n", cssVersion)
	fmt.Fprintf(&b, "<meta property=\"og:type\" content=\"website\">\n<meta property=\"og:title\" content=\"%s\">\n<meta property=\"og:description\" content=\"%s\">\n<meta property=\"og:url\" content=\"%s\">\n",
		html.EscapeString(title), html.EscapeString(description), html.EscapeString(canonical))
	fmt.Fprintf(&b, "<meta property=\"og:image\" content=\"%s/assets/screens/dashboard.%s\">\n", base, map[bool]string{true: "png", false: "webp"}[pngFallback])
	b.WriteString("<meta name=\"author\" content=\"BioDream Developer\">\n</head>\n<body>\n<a class=\"skip\" href=\"#main\">Skip to content</a>\n")

	b.WriteString("<header class=\"top\"><div class=\"wrap\">\n<a class=\"brand\" href=\"/\"><img src=\"/assets/bitvoice-logo.svg\" alt=\"\">BitVoice</a>\n<ul class=\"menu\">\n")
	for _, m := range menu {
		cur := ""
		if m.key == section {
			cur = ` aria-current="page"`
		}
		fmt.Fprintf(&b, "<li><a href=\"%s\"%s>%s</a></li>\n", m.href, cur, m.title)
	}
	b.WriteString("</ul>\n<div class=\"cta\">")
	b.WriteString(`<a class="btn btn-ghost" href="https://github.com/BioDream-LLC/bitvoice">` + icon("github") + `GitHub</a>`)
	b.WriteString(`<a class="btn btn-primary" href="/download/">` + icon("download") + `Download ` + version + `</a></div>` + "\n")
	b.WriteString(`<details class="mobile"><summary aria-label="Menu"><svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">` + icons["menu"] + `</svg></summary><nav>`)
	for _, m := range menu {
		fmt.Fprintf(&b, "<a href=\"%s\">%s</a>", m.href, m.title)
	}
	b.WriteString(`<a href="/download/">Download</a><a href="https://github.com/BioDream-LLC/bitvoice">GitHub</a></nav></details>` + "\n</div></header>\n")

	b.WriteString("<main id=\"main\">\n" + body + "\n</main>\n")
	b.WriteString(footer)
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

const footer = `<footer class="site"><div class="wrap">
<div class="cols">
<div><a class="brand" href="/"><img src="/assets/bitvoice-logo.svg" alt="">BitVoice</a>
<p>An open-source phone system from BioDream LLC. Apache License 2.0.</p></div>
<div><b>Product</b><a href="/features/">Features</a><a href="/freepbx-alternative/">Leaving FreePBX</a><a href="/public-safety/">Public safety (lab)</a></div>
<div><b>Evidence</b><a href="/proof/">How it is tested</a><a href="https://github.com/BioDream-LLC/bitvoice/actions/workflows/ci.yml">Continuous integration</a></div>
<div><b>Help</b><a href="/manual/">Manual</a><a href="/manual/bitvoice-manual.pdf">Manual (PDF)</a><a href="/download/">Download</a><a href="https://github.com/BioDream-LLC/bitvoice">GitHub</a></div>
</div>
<div class="legal">© BioDream LLC. BitVoice is free software under the Apache License 2.0. FreePBX and Asterisk are trademarks of Sangoma Technologies; other names belong to their owners. BitVoice is not affiliated with them.</div>
</div></footer>
`
