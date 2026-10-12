package fax

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxPages is the most pages one fax may have.
const MaxPages = 50

// Helpers are the programs that turn documents into pictures, when they are installed: pdftoppm
// (from poppler) draws PDF pages; LibreOffice turns office documents into PDF first. BitVoice runs
// them as separate programs, with a time limit, never through a shell.
type Helpers struct {
	PDF    string // path to pdftoppm, or ""
	Office string // path to soffice, or ""
}

// FindHelpers looks for the helper programs on the search path and in the usual places.
func FindHelpers() Helpers {
	look := func(names ...string) string {
		for _, n := range names {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
			if filepath.IsAbs(n) {
				if st, err := os.Stat(n); err == nil && !st.IsDir() {
					return n
				}
			}
		}
		return ""
	}
	return Helpers{
		PDF:    look("pdftoppm", "/opt/homebrew/bin/pdftoppm", "/usr/local/bin/pdftoppm"),
		Office: look("soffice", "libreoffice", "/Applications/LibreOffice.app/Contents/MacOS/soffice"),
	}
}

// Kinds says which documents can be sent with these helpers, in words for the screen.
func (h Helpers) Kinds() []string {
	k := []string{"text", "PNG", "JPEG", "GIF"}
	if h.PDF != "" {
		k = append(k, "PDF")
		if h.Office != "" {
			k = append(k, "Word", "Excel", "OpenDocument", "RTF")
		}
	}
	return k
}

var officeExt = map[string]bool{".doc": true, ".docx": true, ".odt": true, ".rtf": true, ".xls": true,
	".xlsx": true, ".ods": true, ".ppt": true, ".pptx": true, ".odp": true}

// Convert turns one document into fax pages. The kind comes from the file name's extension, or
// failing that from its content type.
func (h Helpers) Convert(name, contentType string, data []byte, paper string) ([]Bitmap, error) {
	ext := strings.ToLower(filepath.Ext(name))
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch {
	case ext == ".txt" || ext == "" && strings.HasPrefix(ct, "text/plain"):
		return TextPages(string(data), paper), nil
	case ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || strings.HasPrefix(ct, "image/") && ext != ".tif" && ext != ".tiff":
		img, err := decodePicture(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return []Bitmap{ImagePage(img, paper)}, nil
	case ext == ".pdf" || ct == "application/pdf":
		return h.pdf(name, data, paper)
	case officeExt[ext]:
		if h.Office == "" || h.PDF == "" {
			return nil, fmt.Errorf("%s: office documents need LibreOffice and poppler installed on the server; send a PDF instead", name)
		}
		pdf, err := h.office(name, data)
		if err != nil {
			return nil, err
		}
		return h.pdf(name, pdf, paper)
	case ext == ".tif" || ext == ".tiff":
		return nil, fmt.Errorf("%s: TIFF files cannot be sent yet; send a PDF or a picture", name)
	}
	return nil, fmt.Errorf("%s: this kind of file cannot be faxed; send a PDF, a picture or text", name)
}

func decodePicture(data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	for _, dec := range []func() (image.Image, error){
		func() (image.Image, error) { r.Seek(0, 0); return png.Decode(r) },
		func() (image.Image, error) { r.Seek(0, 0); return jpeg.Decode(r) },
		func() (image.Image, error) { r.Seek(0, 0); return gif.Decode(r) },
	} {
		if img, err := dec(); err == nil {
			if b := img.Bounds(); b.Dx()*b.Dy() > 100_000_000 {
				return nil, errors.New("the picture is too large")
			}
			return img, nil
		}
	}
	return nil, errors.New("not a PNG, JPEG or GIF picture")
}

// run runs a helper in its own folder with a time limit.
func run(dir string, timeout time.Duration, prog string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "HOME=" + dir}
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("the document took too long to convert")
		}
		msg := strings.TrimSpace(errOut.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("the document could not be read (%s)", msg)
	}
	return nil
}

// pdf draws each page of a PDF in grey at fax resolution and fits it to the page.
func (h Helpers) pdf(name string, data []byte, paper string) ([]Bitmap, error) {
	if h.PDF == "" {
		return nil, fmt.Errorf("%s: PDF needs poppler's pdftoppm installed on the server (apk add poppler-utils, apt install poppler-utils, brew install poppler)", name)
	}
	dir, err := os.MkdirTemp("", "bitvoice-fax-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "in.pdf"), data, 0o600); err != nil {
		return nil, err
	}
	// 200 dots per inch in grey: a little more than a fax line, so fitting only ever shrinks.
	if err := run(dir, 2*time.Minute, h.PDF, "-gray", "-r", "200", "-l", strconv.Itoa(MaxPages), "in.pdf", "page"); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "page-*.pgm"))
	sort.Slice(files, func(i, j int) bool { return pageNo(files[i]) < pageNo(files[j]) })
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: the PDF has no pages", name)
	}
	var pages []Bitmap
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		img, err := parsePGM(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		pages = append(pages, ImagePage(img, paper))
	}
	return pages, nil
}

func pageNo(path string) int {
	s := strings.TrimSuffix(filepath.Base(path), ".pgm")
	n, _ := strconv.Atoi(s[strings.LastIndex(s, "-")+1:])
	return n
}

// office turns an office document into PDF with LibreOffice.
func (h Helpers) office(name string, data []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "bitvoice-fax-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := "in" + strings.ToLower(filepath.Ext(name))
	if err := os.WriteFile(filepath.Join(dir, in), data, 0o600); err != nil {
		return nil, err
	}
	if err := run(dir, 3*time.Minute, h.Office, "--headless", "--norestore", "--convert-to", "pdf", "--outdir", dir, in); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return os.ReadFile(filepath.Join(dir, "in.pdf"))
}

// parsePGM reads a binary greymap (P5), as pdftoppm writes them.
func parsePGM(b []byte) (*image.Gray, error) {
	r := bufio.NewReader(bytes.NewReader(b))
	var vals [4]int
	var magic string
	tok := func() (string, error) {
		var sb strings.Builder
		for {
			c, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			if c == '#' {
				r.ReadString('\n')
				continue
			}
			if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
				if sb.Len() > 0 {
					return sb.String(), nil
				}
				continue
			}
			sb.WriteByte(c)
		}
	}
	var err error
	if magic, err = tok(); err != nil || magic != "P5" {
		return nil, errors.New("not a greymap")
	}
	for i := 1; i < 4; i++ {
		s, err := tok()
		if err != nil {
			return nil, errors.New("a short greymap")
		}
		if vals[i], err = strconv.Atoi(s); err != nil || vals[i] <= 0 {
			return nil, errors.New("a bad greymap")
		}
	}
	w, h, maxv := vals[1], vals[2], vals[3]
	if maxv > 255 || w > 20000 || h > 20000 {
		return nil, errors.New("an unusual greymap")
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	if _, err := io.ReadFull(r, img.Pix); err != nil {
		return nil, errors.New("a short greymap")
	}
	if maxv != 255 {
		for i, v := range img.Pix {
			img.Pix[i] = byte(int(v) * 255 / maxv)
		}
	}
	return img, nil
}
