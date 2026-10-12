package fax

import (
	"image"
	"image/color"
	"strings"
	"unicode/utf8"
)

// Page geometry: 1728 pixels across (215 mm at 8 per mm), fine resolution (196 lines per inch).
const (
	PageWidth  = 1728
	letterRows = 2156 // 11 inches
	a4Rows     = 2287 // 297 mm
	margin     = 96   // left and right, about 12 mm
	topMargin  = 120  // below the header line
)

// PageRows is the height of a page for a paper size: "letter" (North America) or "a4".
func PageRows(paper string) int {
	if paper == "a4" {
		return a4Rows
	}
	return letterRows
}

// NewBitmap is a white page.
func NewBitmap(rows int) Bitmap {
	b := Bitmap{Width: PageWidth, Fine: true, Rows: make([][]bool, rows)}
	for y := range b.Rows {
		b.Rows[y] = make([]bool, PageWidth)
	}
	return b
}

// DrawText writes one line of text with its top left corner at x, y, each font pixel scale pixels
// wide (and scale lines tall, a little stretched to keep the shape at fine resolution), and returns
// where the text ended.
func (b Bitmap) DrawText(x, y, scale int, s string) int {
	for _, r := range s {
		if x+6*scale > b.Width {
			break
		}
		g := glyph(r)
		for gy := 0; gy < 13; gy++ {
			for gx := 0; gx < 6; gx++ {
				if g[gy]>>uint(5-gx)&1 == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					row := y + gy*scale + dy
					if row < 0 || row >= len(b.Rows) {
						continue
					}
					for dx := 0; dx < scale; dx++ {
						b.Rows[row][x+gx*scale+dx] = true
					}
				}
			}
		}
		x += 6 * scale
	}
	return x
}

const (
	textScale  = 3                // 18 pixels a character: about 11 characters an inch
	lineHeight = 13*textScale + 6 // rows from one line of text to the next
	perLine    = (PageWidth - 2*margin) / (6 * textScale)
)

// TextPages sets plain text on as many pages as it needs, wrapping long lines at spaces.
func TextPages(text, paper string) []Bitmap {
	rows := PageRows(paper)
	var lines []string
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    ")
	for _, para := range strings.Split(text, "\n") {
		lines = append(lines, wrap(strings.TrimRight(para, " "), perLine)...)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var pages []Bitmap
	page := NewBitmap(rows)
	y := topMargin
	for i, l := range lines {
		if y+lineHeight > rows-topMargin/2 {
			pages = append(pages, page)
			page, y = NewBitmap(rows), topMargin
		}
		if l == "\f" { // a form feed starts a new page
			if i > 0 {
				pages = append(pages, page)
				page, y = NewBitmap(rows), topMargin
			}
			continue
		}
		page.DrawText(margin, y, textScale, l)
		y += lineHeight
	}
	return append(pages, page)
}

// wrap breaks a line into pieces of at most n characters, at spaces where it can.
func wrap(s string, n int) []string {
	if s == "\f" || strings.HasPrefix(s, "\f") {
		return append([]string{"\f"}, wrap(strings.TrimPrefix(s, "\f"), n)...)
	}
	var out []string
	for utf8.RuneCountInString(s) > n {
		r := []rune(s)
		cut := n
		for i := n; i > n/2; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(r[:cut]), " "))
		s = strings.TrimLeft(string(r[cut:]), " ")
	}
	return append(out, s)
}

// ImagePage fits a picture to a page (no larger than the page, the shape kept, centred at the top).
// A picture that is nearly all black and white (a scanned document) is cut at mid-grey; a photo is
// dithered so its greys survive as dots.
func ImagePage(img image.Image, paper string) Bitmap {
	rows := PageRows(paper)
	page := NewBitmap(rows)
	r := img.Bounds()
	if r.Dx() == 0 || r.Dy() == 0 {
		return page
	}
	areaW, areaH := PageWidth-margin, rows-topMargin // pictures get half the text margins
	// Fax pixels are almost square (204 by 196 per inch): scale both ways alike.
	sc := min(float64(areaW)/float64(r.Dx()), float64(areaH)/float64(r.Dy()))
	w, h := int(float64(r.Dx())*sc), int(float64(r.Dy())*sc)
	if w < 1 || h < 1 {
		return page
	}
	x0, y0 := (PageWidth-w)/2, topMargin
	// Sample the picture into grey levels at page resolution (an area average when shrinking).
	grey := make([][]float64, h)
	mid := 0
	for y := 0; y < h; y++ {
		grey[y] = make([]float64, w)
		sy0, sy1 := r.Min.Y+y*r.Dy()/h, r.Min.Y+(y+1)*r.Dy()/h
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < w; x++ {
			sx0, sx1 := r.Min.X+x*r.Dx()/w, r.Min.X+(x+1)*r.Dx()/w
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var sum float64
			n := 0
			for sy := sy0; sy < sy1; sy += max(1, (sy1-sy0)/4) {
				for sx := sx0; sx < sx1; sx += max(1, (sx1-sx0)/4) {
					sum += luminance(img.At(sx, sy))
					n++
				}
			}
			v := sum / float64(n)
			grey[y][x] = v
			if v > 0.2 && v < 0.8 {
				mid++
			}
		}
	}
	photo := mid*10 > w*h // more than a tenth of the picture is grey
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := grey[y][x]
			black := v < 0.5
			if photo { // Floyd-Steinberg: pass each pixel's error on to its neighbours
				out := 1.0
				if black {
					out = 0
				}
				e := v - out
				if x+1 < w {
					grey[y][x+1] += e * 7 / 16
				}
				if y+1 < h {
					if x > 0 {
						grey[y+1][x-1] += e * 3 / 16
					}
					grey[y+1][x] += e * 5 / 16
					if x+1 < w {
						grey[y+1][x+1] += e * 1 / 16
					}
				}
			}
			if black && y0+y < rows {
				page.Rows[y0+y][x0+x] = true
			}
		}
	}
	return page
}

// luminance is a colour's brightness, 0 black to 1 white; transparent counts as white paper.
func luminance(c color.Color) float64 {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return 1
	}
	// Composite over white, then Rec. 601 weights.
	wr := float64(r) + float64(0xffff-a)
	wg := float64(g) + float64(0xffff-a)
	wb := float64(b) + float64(0xffff-a)
	return (0.299*wr + 0.587*wg + 0.114*wb) / 0xffff
}

// Stamp prints the header line fax machines put at the top of each page: the time, who sent it,
// their number and the page count.
func (b Bitmap) Stamp(when, name, number string, page, of int) {
	for y := 0; y < 48 && y < len(b.Rows); y++ {
		for x := range b.Rows[y] {
			b.Rows[y][x] = false
		}
	}
	b.DrawText(40, 8, 2, when+"  "+name)
	right := number + "   p." + itoa(page) + "/" + itoa(of)
	b.DrawText(PageWidth-40-len(right)*12, 8, 2, right)
}
