package fax

import (
	"os"
	"testing"
)

// testPage is a page with every kind of run: long and short, both colours, lines that start black,
// lines all white or all black, and a run past 2560 is not possible at 1728 wide but 1728 is.
func testPage() Bitmap {
	p := Bitmap{Width: 1728}
	for y := 0; y < 120; y++ {
		row := make([]bool, 1728)
		switch {
		case y == 0: // all white
		case y == 1:
			for x := range row {
				row[x] = true // all black
			}
		default:
			for x := range row {
				// stripes of growing width, shifted each line, so runs of 1 to 200 of both colours
				row[x] = ((x+y*7)/(1+(x+y)%200))%2 == 1
			}
			row[0] = y%3 == 0 // some lines start black
		}
		p.Rows = append(p.Rows, row)
	}
	return p
}

func TestEncodeMHRows(t *testing.T) {
	p := testPage()
	data := EncodeMH(p, 0)
	if got := rows(data); got != len(p.Rows) {
		t.Fatalf("rows = %d, want %d", got, len(p.Rows))
	}
	if out := os.Getenv("BITVOICE_T4_OUT"); out != "" {
		os.WriteFile(out, data, 0o644)
	}
}

func TestRenderTextAndPDF(t *testing.T) {
	text := "To: Accounts\nFrom: BitVoice\n\nThis line is long enough that it has to wrap at a space somewhere near the right margin of the page, twice over if need be.\n\fPage two starts here."
	pages := TextPages(text, "letter")
	if len(pages) != 2 {
		t.Fatalf("%d pages, want 2 (a form feed starts a page)", len(pages))
	}
	for i := range pages {
		pages[i].Stamp("12-Oct-2026 09:00", "Office fax", "+15551230000", i+1, len(pages))
		if got := rows(EncodeMH(pages[i], 0)); got != letterRows {
			t.Fatalf("page %d: %d rows", i, got)
		}
	}
	h := FindHelpers()
	if out := os.Getenv("BITVOICE_FAX_OUT"); out != "" {
		all := pages
		if h.PDF != "" && os.Getenv("BITVOICE_FAX_PDF") != "" {
			b, _ := os.ReadFile(os.Getenv("BITVOICE_FAX_PDF"))
			pp, err := h.Convert("in.pdf", "", b, "letter")
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, pp...)
		}
		var ps []Page
		for _, p := range all {
			ps = append(ps, Page{Data: EncodeMH(p, 0), Params: Params{Fine: true, Width: 1728}})
		}
		os.WriteFile(out, TIFF(ps), 0o644)
		os.WriteFile(out+".pdf", PDF(ps), 0o644)
	}
}
