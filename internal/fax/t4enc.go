package fax

// Bitmap is one fax page, one bit per pixel: Rows[y][x] is true for black. Every row is Width wide.
type Bitmap struct {
	Width int
	Rows  [][]bool
	Fine  bool // 196 lines per inch, else 98
}

// t4Writer collects bits most significant bit first, as T.38 carries page data.
type t4Writer struct {
	b   []byte
	cur byte
	n   uint
}

func (w *t4Writer) code(s string) {
	for i := 0; i < len(s); i++ {
		w.cur <<= 1
		if s[i] == '1' {
			w.cur |= 1
		}
		if w.n++; w.n == 8 {
			w.b, w.cur, w.n = append(w.b, w.cur), 0, 0
		}
	}
}

func (w *t4Writer) bytes() []byte {
	if w.n > 0 {
		return append(w.b, w.cur<<(8-w.n))
	}
	return w.b
}

const eol = "000000000001"

// run writes one run of a colour: make-up codes for the 64s, then a terminating code.
func (w *t4Writer) run(n int, black bool) {
	term, makeup := whiteTerm[:], whiteMakeup[:]
	if black {
		term, makeup = blackTerm[:], blackMakeup[:]
	}
	for n >= 64 {
		m := n / 64
		if m > len(makeup) {
			m = len(makeup)
		}
		w.code(makeup[m-1])
		n -= m * 64
	}
	w.code(term[n])
}

// EncodeMH codes a page in T.4 one-dimensional coding (Modified Huffman): each line starts with an
// end-of-line code and alternates white and black runs, starting white; the page ends with six
// end-of-line codes (RTC). minBits pads each line to the receiver's minimum scan line time.
func EncodeMH(p Bitmap, minBits int) []byte {
	w := &t4Writer{}
	for _, row := range p.Rows {
		w.code(eol)
		start := len(w.b)*8 + int(w.n)
		px := func(x int) bool { return x < len(row) && row[x] } // missing pixels are white
		black, x := false, 0
		for x < p.Width {
			n := 0
			for x < p.Width && px(x) == black {
				n++
				x++
			}
			w.run(n, black) // a line may start with a black pixel: then the first white run is 0
			black = !black
		}
		for len(w.b)*8+int(w.n)-start < minBits {
			w.code("0")
		}
	}
	for i := 0; i < 6; i++ {
		w.code(eol)
	}
	return w.bytes()
}
