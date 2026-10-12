package fax

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// rows counts the lines of a page of T.4 data (most significant bit first, as T.38 carries it): each line starts with
// an end-of-line code (eleven or more zeros and a one); the six in a row that end a page (RTC) are
// not lines.
func rows(data []byte) int {
	n, zeros, eolRun, runAtLast := 0, 0, 0, 0
	for _, b := range data {
		for i := 0; i < 8; i++ {
			if b>>uint(7-i)&1 == 0 {
				zeros++
				continue
			}
			if zeros >= 11 {
				n++
				eolRun++
				runAtLast = eolRun // what follows the last EOL (some senders add ones) does not count
			} else {
				eolRun = 0
			}
			zeros = 0
		}
	}
	// A page ends with RTC: six EOLs with nothing between them, five of which add no line.
	if runAtLast >= 6 {
		n -= runAtLast - 1
	}
	if n > 0 {
		n-- // the last EOL begins no line
	}
	return n
}

func yres(p Params) int {
	if p.Fine {
		return 196
	}
	return 98
}

// TIFF writes the pages as a TIFF Class F file (one image per page, as the fax sent them).
func TIFF(pages []Page) []byte {
	le := binary.LittleEndian
	var b bytes.Buffer
	b.Write([]byte{'I', 'I', 42, 0, 0, 0, 0, 0}) // the first directory's offset is set below
	prevNext := 4
	for i, p := range pages {
		for b.Len()%2 != 0 {
			b.WriteByte(0)
		}
		dataAt := b.Len()
		b.Write(p.Data)
		for b.Len()%2 != 0 {
			b.WriteByte(0)
		}
		resAt := b.Len()
		binary.Write(&b, le, [4]uint32{204, 1, uint32(yres(p.Params)), 1})
		dirAt := b.Len()
		buf := b.Bytes()
		le.PutUint32(buf[prevNext:], uint32(dirAt))
		width := p.Params.Width
		if width == 0 {
			width = 1728
		}
		t4opts := uint32(0)
		if p.Params.TwoD {
			t4opts = 1
		}
		type entry struct {
			tag, typ uint16
			count    uint32
			value    uint32
		}
		const short, long, rational = 3, 4, 5
		es := []entry{
			{254, long, 1, 2},                                   // NewSubfileType: a page
			{256, long, 1, uint32(width)},                       // ImageWidth
			{257, long, 1, uint32(rows(p.Data))},                // ImageLength
			{258, short, 1, 1},                                  // BitsPerSample
			{259, short, 1, 3},                                  // Compression: T.4
			{262, short, 1, 0},                                  // Photometric: white is zero
			{266, short, 1, 1},                                  // FillOrder: most significant bit first
			{273, long, 1, uint32(dataAt)},                      // StripOffsets
			{277, short, 1, 1},                                  // SamplesPerPixel
			{278, long, 1, uint32(rows(p.Data))},                // RowsPerStrip
			{279, long, 1, uint32(len(p.Data))},                 // StripByteCounts
			{282, rational, 1, uint32(resAt)},                   // XResolution 204
			{283, rational, 1, uint32(resAt + 8)},               // YResolution 98 or 196
			{292, long, 1, t4opts},                              // T4Options
			{296, short, 1, 2},                                  // ResolutionUnit: inch
			{297, short, 2, uint32(i) | uint32(len(pages))<<16}, // PageNumber
		}
		binary.Write(&b, le, uint16(len(es)))
		for _, e := range es {
			binary.Write(&b, le, e.tag)
			binary.Write(&b, le, e.typ)
			binary.Write(&b, le, e.count)
			binary.Write(&b, le, e.value)
		}
		prevNext = b.Len()
		binary.Write(&b, le, uint32(0))
	}
	return b.Bytes()
}

func reverseBits(data []byte) []byte {
	out := make([]byte, len(data))
	for i, x := range data {
		var r byte
		for j := 0; j < 8; j++ {
			r = r<<1 | x>>uint(j)&1
		}
		out[i] = r
	}
	return out
}

// PDF writes the pages as a PDF, each page an image in the fax's own coding (CCITTFaxDecode), at
// its real size.
func PDF(pages []Page) []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s", len(offsets), body)
		if stream != nil {
			b.WriteString("\nstream\n")
			b.Write(stream)
			b.WriteString("\nendstream")
		}
		b.WriteString("\nendobj\n")
	}
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(pages)
	// Objects: 1 catalog, 2 pages, then per page: page, image, content.
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+3*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, n), nil)
	for i, p := range pages {
		width := p.Params.Width
		if width == 0 {
			width = 1728
		}
		h := rows(p.Data)
		if h < 1 {
			h = 1
		}
		wpt := float64(width) / 204 * 72
		hpt := float64(h) / float64(yres(p.Params)) * 72
		k := 0
		if p.Params.TwoD {
			k = 2
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im %d 0 R >> >> /Contents %d 0 R >>",
			wpt, hpt, 4+3*i, 5+3*i), nil)
		img := p.Data
		obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ImageMask false /ColorSpace /DeviceGray /BitsPerComponent 1 "+
			"/Filter /CCITTFaxDecode /DecodeParms << /K %d /Columns %d /Rows %d /EndOfLine true /EncodedByteAlign false /BlackIs1 false /EndOfBlock true >> /Length %d >>",
			width, h, k, width, h, len(img)), img)
		content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im Do Q", wpt, hpt)
		obj(fmt.Sprintf("<< /Length %d >>", len(content)), []byte(content))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}
