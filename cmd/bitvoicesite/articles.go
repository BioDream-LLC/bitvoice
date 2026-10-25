package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The technology articles: one Markdown file per technology in site/articles, each a tile on the
// home page and the technology index, and a page of its own at /technology/SLUG/.

type article struct {
	slug, title, tile, icon, area, description, manual string
	body                                               string
	words                                              int
}

// areas is the order the articles are grouped in, with the anchor each group has.
var areas = []struct{ name, id, intro string }{
	{"Phone system", "phone-system", "The calls themselves: how they are set up, routed, answered and carried to the rest of the world."},
	{"Media", "media", "What travels once a call is up: sound, video, fax and the codecs between them."},
	{"Contact centre", "contact-centre", "Queues, dialling out, recording and every channel customers use."},
	{"Developers", "developers", "For the people who build on a phone system: Asterisk's interfaces, scripts and the API."},
	{"AI", "ai", "Speech to text on the server, assistants you choose, and facts that need no model."},
	{"Operations", "operations", "Running it: security, the company directory, several servers, updates and moving in."},
	{"Public safety", "public-safety", "The lab and design-partner track: 911 call handling, radio dispatch and NG911 Core Services. Not certified."},
}

func loadArticles(root string) ([]article, error) {
	files, err := filepath.Glob(filepath.Join(root, "site/articles/*.md"))
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, a := range areas {
		known[a.name] = true
	}
	var out []article
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		s := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if !strings.HasPrefix(s, "---\n") {
			return nil, fmt.Errorf("%s: no front matter", f)
		}
		end := strings.Index(s[4:], "\n---\n")
		if end < 0 {
			return nil, fmt.Errorf("%s: front matter not closed", f)
		}
		a := article{slug: strings.TrimSuffix(filepath.Base(f), ".md"), body: s[4+end+5:]}
		for _, l := range strings.Split(s[4:4+end], "\n") {
			k, v, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
			switch strings.TrimSpace(k) {
			case "title":
				a.title = v
			case "tile":
				a.tile = v
			case "icon":
				a.icon = v
			case "area":
				a.area = v
			case "description":
				a.description = v
			case "manual":
				a.manual = v
			}
		}
		if a.title == "" || a.tile == "" || a.description == "" || !known[a.area] {
			return nil, fmt.Errorf("%s: front matter needs title, tile, description and a known area", f)
		}
		if _, ok := icons[a.icon]; !ok {
			return nil, fmt.Errorf("%s: no icon %q", f, a.icon)
		}
		a.words = len(strings.Fields(a.body))
		out = append(out, a)
	}
	order := map[string]int{}
	for i, a := range areas {
		order[a.name] = i
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].area] < order[out[j].area] })
	return out, nil
}

// tiles is the grid of small squares, one per article; grouped puts a heading over each area.
func tiles(arts []article, grouped bool) string {
	var b strings.Builder
	tile := func(a article) {
		fmt.Fprintf(&b, `<a class="tile" href="/technology/%s/"><span class="tile-icon">%s</span><span class="tile-area">%s</span><h3>%s</h3><p>%s</p><span class="tile-more">Read the article · %d min</span></a>`+"\n",
			a.slug, icon(a.icon), html.EscapeString(a.area), html.EscapeString(shortTitle(a.title)), html.EscapeString(a.tile), minutes(a.words))
	}
	if !grouped {
		b.WriteString(`<div class="tiles">` + "\n")
		for _, a := range arts {
			tile(a)
		}
		b.WriteString("</div>\n")
		return b.String()
	}
	for _, ar := range areas {
		var in []article
		for _, a := range arts {
			if a.area == ar.name {
				in = append(in, a)
			}
		}
		if len(in) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<section class=\"area-group\" id=\"%s\"><div class=\"group-head\"><h2>%s</h2><p>%s</p></div>\n<div class=\"tiles\">\n", ar.id, html.EscapeString(ar.name), html.EscapeString(ar.intro))
		for _, a := range in {
			tile(a)
		}
		b.WriteString("</div></section>\n")
	}
	return b.String()
}

// shortTitle is an article's title up to its colon, for a tile.
func shortTitle(t string) string {
	if i := strings.Index(t, ":"); i > 0 {
		return t[:i]
	}
	return t
}

func minutes(words int) int { return (words + 219) / 220 }

// articlePage is an article's own page: its title, a contents list, the text, and beside it the
// manual chapter, the other articles of its area and the download.
func articlePage(a article, all []article, manualTitles map[string]string, version string) string {
	body, toc := markdown(a.body)
	var b strings.Builder
	areaID := ""
	for _, ar := range areas {
		if ar.name == a.area {
			areaID = ar.id
		}
	}
	lab := ""
	if a.area == "Public safety" {
		lab = `<p class="lab-note"><b>Lab and design-partner track.</b> Behind Settings › Public-safety profiles (lab); not certified, not run against live public-safety systems. <a href="/public-safety/">What that means</a>.</p>`
	}
	fmt.Fprintf(&b, `<section class="hero article-hero"><div class="wrap"><p class="crumbs"><a href="/technology/">Technology</a> <span>/</span> <a href="/technology/#%s">%s</a></p>
<div class="article-title"><span class="tile-icon big">%s</span><div><h1>%s</h1><p class="lede">%s</p><p class="meta">%d min read · %s words</p></div></div>%s</div></section>
`, areaID, html.EscapeString(a.area), icon(a.icon), html.EscapeString(a.title), html.EscapeString(a.description), minutes(a.words), thousands(a.words), lab)
	b.WriteString(`<section class="band"><div class="wrap article-layout"><article class="prose">` + "\n" + body + "</article>\n<aside class=\"article-side\">")
	if len(toc) > 1 {
		b.WriteString(`<div class="side-box"><b>On this page</b><ol>`)
		for _, h := range toc {
			fmt.Fprintf(&b, `<li><a href="#%s">%s</a></li>`, h.id, mdInline(h.text))
		}
		b.WriteString("</ol></div>")
	}
	if t, ok := manualTitles[a.manual]; ok {
		fmt.Fprintf(&b, `<div class="side-box"><b>In the manual</b><a class="side-link" href="/manual/#%s">%s%s</a></div>`, a.manual, icon("book"), html.EscapeString(t))
	}
	b.WriteString(`<div class="side-box"><b>More in ` + html.EscapeString(a.area) + `</b><ul>`)
	for _, o := range all {
		if o.area == a.area && o.slug != a.slug {
			fmt.Fprintf(&b, `<li><a href="/technology/%s/">%s</a></li>`, o.slug, html.EscapeString(shortTitle(o.title)))
		}
	}
	b.WriteString(`</ul></div><div class="side-box accent"><b>Try it yourself</b><p>One file for Linux, macOS or Windows. A phone rings a few minutes after you start it.</p><a class="btn btn-primary" href="/download/">` + icon("download") + `Download ` + version + `</a></div></aside></div></section>` + "\n")
	return b.String()
}

func thousands(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}
