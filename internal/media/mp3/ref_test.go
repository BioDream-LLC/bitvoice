package mp3

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func decodeAll(b []byte) []int16 {
	var d Decoder
	pcm := make([]int16, MaxSamplesPerFrame)
	var out []int16
	for p := 0; p < len(b); {
		n, info := d.DecodeFrame(b[p:], pcm)
		if info.FrameBytes == 0 {
			break
		}
		p += info.FrameBytes
		out = append(out, pcm[:n*info.Channels]...)
	}
	return out
}

// Against minimp3's own C code (BITVOICE_TEST_MP3REF names a directory of x.mp3 and x.ref.raw).
func TestAgainstReference(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_MP3REF")
	if dir == "" {
		t.Skip("set BITVOICE_TEST_MP3REF")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.mp3"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		ref, _ := os.ReadFile(f[:len(f)-4] + ".ref.raw")
		got := decodeAll(b)
		t.Logf("%s: %d samples", filepath.Base(f), len(got))
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, got)
		if !bytes.Equal(buf.Bytes(), ref) {
			g := buf.Bytes()
			first := -1
			for i := 0; i < min(len(g), len(ref)); i++ {
				if g[i] != ref[i] {
					first = i / 2
					break
				}
			}
			t.Errorf("%s: %d samples, reference %d; first difference at sample %d", filepath.Base(f), len(g)/2, len(ref)/2, first)
		}
	}
}
