// Package evidence reads what a photo or video file says about itself: when it was taken and
// where (EXIF in JPEG and HEIC-free JPEG photos; the movie header and the location box of MP4 and
// QuickTime videos). It reads only; the file is kept as it came, with its hash.
package evidence

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"time"
)

// Meta is what a file says about itself. Taken is zero, and HasGPS false, when it does not say.
type Meta struct {
	Kind  string    `json:"kind"` // photo, video or file
	Taken time.Time `json:"taken,omitzero"`
	// TakenLocal is a photo's time as its camera wrote it, with no zone (EXIF has none unless the
	// camera adds an offset): "2026-10-15 14:05:30". Taken is then that time read as UTC, for sorting.
	TakenLocal string   `json:"takenLocal,omitempty"`
	HasGPS     bool     `json:"hasGps"`
	Lat        float64  `json:"lat,omitempty"`
	Lon        float64  `json:"lon,omitempty"`
	keys       []string // QuickTime metadata keys, while reading
}

// Read reads a file's metadata from its first bytes (and, for an MP4 whose header is at the end,
// from the end): r may be large; only the boxes and segments needed are read.
func Read(r io.ReaderAt, size int64) Meta {
	head := make([]byte, min(size, 12))
	if _, err := r.ReadAt(head, 0); err != nil && err != io.EOF {
		return Meta{Kind: "file"}
	}
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8:
		m := Meta{Kind: "photo"}
		if ex, err := jpegEXIF(r, size); err == nil {
			parseEXIF(ex, &m)
		}
		return m
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		m := Meta{Kind: "video"}
		mp4Meta(r, 0, size, 0, &m)
		return m
	case len(head) >= 4 && (string(head[:4]) == "\x89PNG"):
		return Meta{Kind: "photo"}
	}
	return Meta{Kind: "file"}
}

// jpegEXIF finds the APP1 Exif segment.
func jpegEXIF(r io.ReaderAt, size int64) ([]byte, error) {
	off := int64(2)
	hdr := make([]byte, 4)
	for off+4 <= size && off < 1<<20 {
		if _, err := r.ReadAt(hdr, off); err != nil {
			return nil, err
		}
		if hdr[0] != 0xFF {
			return nil, errors.New("not a JPEG segment")
		}
		marker := hdr[1]
		if marker == 0xDA || marker == 0xD9 { // image data: no EXIF before it
			return nil, errors.New("no EXIF")
		}
		n := int64(binary.BigEndian.Uint16(hdr[2:]))
		if n < 2 || off+2+n > size {
			return nil, errors.New("a broken segment")
		}
		if marker == 0xE1 && n > 8 {
			seg := make([]byte, n-2)
			if _, err := r.ReadAt(seg, off+4); err != nil {
				return nil, err
			}
			if bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
				return seg[6:], nil
			}
		}
		off += 2 + n
	}
	return nil, errors.New("no EXIF")
}

// parseEXIF reads DateTimeOriginal (or DateTime) and the GPS position from a TIFF structure.
func parseEXIF(b []byte, m *Meta) {
	if len(b) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	if bo.Uint16(b[2:]) != 42 {
		return
	}
	type entry struct {
		tag, typ uint16
		count    uint32
		val      []byte // the value, or its 4 bytes of offset
	}
	ifd := func(off uint32) map[uint16]entry {
		out := map[uint16]entry{}
		if int(off)+2 > len(b) {
			return out
		}
		n := int(bo.Uint16(b[off:]))
		for i := 0; i < n && i < 512; i++ {
			p := int(off) + 2 + 12*i
			if p+12 > len(b) {
				break
			}
			out[bo.Uint16(b[p:])] = entry{bo.Uint16(b[p:]), bo.Uint16(b[p+2:]), bo.Uint32(b[p+4:]), b[p+8 : p+12]}
		}
		return out
	}
	data := func(e entry, size int) []byte {
		total := size * int(e.count)
		if total <= 4 {
			return e.val[:total]
		}
		o := int(bo.Uint32(e.val))
		if o < 0 || o+total > len(b) {
			return nil
		}
		return b[o : o+total]
	}
	ascii := func(e entry) string { return string(bytes.TrimRight(data(e, 1), "\x00")) }
	rational := func(e entry) []float64 {
		d := data(e, 8)
		var out []float64
		for i := 0; i+8 <= len(d); i += 8 {
			num, den := bo.Uint32(d[i:]), bo.Uint32(d[i+4:])
			if den == 0 {
				return nil
			}
			out = append(out, float64(num)/float64(den))
		}
		return out
	}
	root := ifd(bo.Uint32(b[4:]))
	when := ""
	if e, ok := root[0x0132]; ok { // DateTime
		when = ascii(e)
	}
	if e, ok := root[0x8769]; ok { // the EXIF IFD
		sub := ifd(bo.Uint32(e.val))
		if d, ok := sub[0x9003]; ok { // DateTimeOriginal
			when = ascii(d)
		}
	}
	if t, err := time.Parse("2006:01:02 15:04:05", when); err == nil {
		m.Taken, m.TakenLocal = t, t.Format("2006-01-02 15:04:05") // the camera's own clock, no zone
	}
	if e, ok := root[0x8825]; ok { // the GPS IFD
		g := ifd(bo.Uint32(e.val))
		lat, lon := rational(g[2]), rational(g[4])
		if len(lat) == 3 && len(lon) == 3 {
			la := lat[0] + lat[1]/60 + lat[2]/3600
			lo := lon[0] + lon[1]/60 + lon[2]/3600
			if r, ok := g[1]; ok && ascii(r) == "S" {
				la = -la
			}
			if r, ok := g[3]; ok && ascii(r) == "W" {
				lo = -lo
			}
			if math.Abs(la) <= 90 && math.Abs(lo) <= 180 && (la != 0 || lo != 0) {
				m.HasGPS, m.Lat, m.Lon = true, la, lo
			}
		}
	}
}

// ISO 6709 position as QuickTime and MP4 keep it: +40.2633-076.8823+100.0/
var iso6709 = regexp.MustCompile(`^([+-]\d+(?:\.\d+)?)([+-]\d+(?:\.\d+)?)`)

// mp4Meta walks the boxes: mvhd's creation time (seconds since 1904, UTC) and the ©xyz location.
func mp4Meta(r io.ReaderAt, off, end int64, depth int, m *Meta) {
	if depth > 6 {
		return
	}
	hdr := make([]byte, 16)
	for off+8 <= end {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return
		}
		n := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		body := off + 8
		switch n {
		case 1:
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return
			}
			n = int64(binary.BigEndian.Uint64(hdr[8:16]))
			body = off + 16
		case 0:
			n = end - off
		}
		if n < 8 || off+n > end {
			return
		}
		switch typ {
		case "moov", "udta", "meta", "trak", "mdia":
			start := body
			if typ == "meta" {
				// MP4's meta is a full box (4 bytes of version and flags); QuickTime's is not.
				var peek [8]byte
				if _, err := r.ReadAt(peek[:], body); err == nil && string(peek[4:8]) != "hdlr" {
					start += 4
				}
			}
			mp4Meta(r, start, off+n, depth+1, m)
		case "mvhd":
			var v [16]byte
			if _, err := r.ReadAt(v[:], body); err == nil {
				var secs uint64
				if v[0] == 1 {
					secs = binary.BigEndian.Uint64(v[4:12])
				} else {
					secs = uint64(binary.BigEndian.Uint32(v[4:8]))
				}
				if secs > 0 {
					m.Taken = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs) * time.Second)
				}
			}
		case "loci": // 3GPP location (TS 26.244): version, language, name, role, then 16.16 lon, lat, alt
			if n-8 > 10 && n < 4096 {
				v := make([]byte, n-8)
				if _, err := r.ReadAt(v, body); err == nil {
					p := 6 // version and flags, then the language
					if i := bytes.IndexByte(v[p:], 0); i >= 0 {
						p += i + 1 // the place's name
						p++        // its role
						if p+8 <= len(v) {
							lo := float64(int32(binary.BigEndian.Uint32(v[p:]))) / 65536
							la := float64(int32(binary.BigEndian.Uint32(v[p+4:]))) / 65536
							if math.Abs(la) <= 90 && math.Abs(lo) <= 180 && (la != 0 || lo != 0) && !m.HasGPS {
								m.HasGPS, m.Lat, m.Lon = true, la, lo
							}
						}
					}
				}
			}
		case "keys": // QuickTime metadata keys: com.apple.quicktime.location.ISO6709 and the rest
			if n < 64<<10 {
				v := make([]byte, n-8)
				if _, err := r.ReadAt(v, body); err == nil && len(v) >= 8 {
					cnt := int(binary.BigEndian.Uint32(v[4:]))
					m.keys = nil
					for p, i := 8, 0; i < cnt && p+8 <= len(v); i++ {
						kl := int(binary.BigEndian.Uint32(v[p:]))
						if kl < 8 || p+kl > len(v) {
							break
						}
						m.keys = append(m.keys, string(v[p+8:p+kl]))
						p += kl
					}
				}
			}
		case "ilst":
			if n < 1<<20 {
				v := make([]byte, n-8)
				if _, err := r.ReadAt(v, body); err == nil {
					for p := 0; p+8 <= len(v); {
						il := int(binary.BigEndian.Uint32(v[p:]))
						if il < 8 || p+il > len(v) {
							break
						}
						idx := int(binary.BigEndian.Uint32(v[p+4:]))
						item := v[p+8 : p+il]
						if idx >= 1 && idx <= len(m.keys) && m.keys[idx-1] == "com.apple.quicktime.location.ISO6709" &&
							len(item) > 16 && string(item[4:8]) == "data" {
							if g := iso6709.FindStringSubmatch(string(item[16:])); g != nil {
								la, _ := strconv.ParseFloat(g[1], 64)
								lo, _ := strconv.ParseFloat(g[2], 64)
								if math.Abs(la) <= 90 && math.Abs(lo) <= 180 {
									m.HasGPS, m.Lat, m.Lon = true, la, lo
								}
							}
						}
						p += il
					}
				}
			}
		case "\xa9xyz":
			if n-8 > 4 && n < 1024 {
				v := make([]byte, n-8)
				if _, err := r.ReadAt(v, body); err == nil {
					if len(v) > 4 {
						v = v[4:] // the string's length and language
					}
					if g := iso6709.FindStringSubmatch(string(v)); g != nil {
						la, _ := strconv.ParseFloat(g[1], 64)
						lo, _ := strconv.ParseFloat(g[2], 64)
						if math.Abs(la) <= 90 && math.Abs(lo) <= 180 {
							m.HasGPS, m.Lat, m.Lon = true, la, lo
						}
					}
				}
			}
		}
		off += n
	}
}
